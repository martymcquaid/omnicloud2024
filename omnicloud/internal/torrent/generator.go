package torrent

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/google/uuid"
)

// Generator handles torrent file creation for DCPs
type Generator struct {
	db              *sql.DB
	trackerURL      string
	workersNum      int
	checkpointBatch int // pieces per checkpoint (default 1000)
}

// GenerationProgress tracks torrent generation progress
type GenerationProgress struct {
	PackageID       string
	Status          string
	ProgressPercent float64
	CurrentFile     string
	ErrorMessage    string
}

// NewGenerator creates a new torrent generator
func NewGenerator(db *sql.DB, trackerURL string, workers int) *Generator {
	if workers <= 0 {
		workers = 4
	}
	return &Generator{
		db:              db,
		trackerURL:      trackerURL,
		workersNum:      workers,
		checkpointBatch: 1000, // checkpoint every 1000 pieces (~16GB for 16MB pieces)
	}
}

// GenerateTorrent creates a single .torrent file for one DCP package (all files in the package directory
// are included in one torrent; we do not create separate torrents per file).
func (g *Generator) GenerateTorrent(ctx context.Context, packagePath, packageID, serverID string) (*metainfo.MetaInfo, string, error) {
	// Calculate total size to determine piece size
	totalSize, _, err := g.calculateDirectorySize(packagePath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to calculate directory size: %w", err)
	}

	// Calculate optimal piece size (16MB for <100GB, 32MB for larger)
	pieceSize := g.calculatePieceSize(totalSize)

	// Update queue status and total size for ETA/speed calculation
	if err := g.updateQueueStatus(packageID, serverID, "generating", 0, "Starting torrent generation", totalSize); err != nil {
		return nil, "", fmt.Errorf("failed to update queue status: %w", err)
	}

	// Create MetaInfo builder
	info := metainfo.Info{
		PieceLength: int64(pieceSize),
	}

	// Get all files in the DCP directory
	files, err := g.collectFiles(packagePath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to collect files: %w", err)
	}

	// Build file list for torrent
	for _, f := range files {
		relPath, err := filepath.Rel(packagePath, f)
		if err != nil {
			return nil, "", fmt.Errorf("failed to get relative path: %w", err)
		}
		
		fileInfo, err := os.Stat(f)
		if err != nil {
			return nil, "", fmt.Errorf("failed to stat file %s: %w", f, err)
		}

		info.Files = append(info.Files, metainfo.FileInfo{
			Path:   []string{relPath},
			Length: fileInfo.Size(),
		})
	}

	// Set torrent name to package directory name
	info.Name = filepath.Base(packagePath)

	// Generate pieces by hashing files (pass totalSize for within-file progress)
	err = g.generatePieces(&info, packagePath, packageID, serverID, totalSize)
	if err != nil {
		g.updateQueueStatus(packageID, serverID, "failed", 0, fmt.Sprintf("Failed to generate pieces: %v", err), 0)
		return nil, "", fmt.Errorf("failed to generate pieces: %w", err)
	}

	// Create MetaInfo
	mi := &metainfo.MetaInfo{
		Announce:     g.trackerURL,
		CreatedBy:    "OmniCloud",
		CreationDate: time.Now().Unix(),
	}
	
	// Marshal info and store
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		return nil, "", fmt.Errorf("failed to marshal info: %w", err)
	}
	mi.InfoBytes = infoBytes

	// Calculate info hash
	infoHash := mi.HashInfoBytes().HexString()

	// Pre-populate torrent_piece_completion so StartSeeding sees 100% immediately.
	// Without this, the anacrolix library would re-verify every piece from disk
	// (reads the full DCP again), which for a 200+ GB DCP takes hours.
	totalPiecesForCompletion := len(info.Pieces) / 20
	if err := g.prePopulatePieceCompletion(infoHash, totalPiecesForCompletion); err != nil {
		log.Printf("Warning: failed to pre-populate piece completion for %s: %v (seeding will re-verify from disk)", infoHash, err)
		// Don't fail — seeding will still work, just slower on first startup
	} else {
		log.Printf("Pre-populated %d pieces as complete for %s", totalPiecesForCompletion, infoHash)
	}

	// Update queue status to completed
	if err := g.updateQueueStatus(packageID, serverID, "completed", 100, "", 0); err != nil {
		return nil, "", fmt.Errorf("failed to update completion status: %w", err)
	}

	// Clean up checkpoint data on success
	if err := g.cleanupCheckpoints(packageID, serverID); err != nil {
		log.Printf("Warning: failed to cleanup checkpoints for %s: %v", packageID, err)
		// Don't fail the entire generation for cleanup errors
	}

	return mi, infoHash, nil
}

// calculateDirectorySize returns total size and file count
func (g *Generator) calculateDirectorySize(path string) (int64, int, error) {
	var totalSize int64
	var fileCount int

	err := filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			totalSize += info.Size()
			fileCount++
		}
		return nil
	})

	return totalSize, fileCount, err
}

// calculatePieceSize returns optimal piece size based on total size
func (g *Generator) calculatePieceSize(totalSize int64) int {
	const (
		mb100    = 100 * 1024 * 1024 * 1024 // 100GB
		size16MB = 16 * 1024 * 1024
		size32MB = 32 * 1024 * 1024
	)

	if totalSize < mb100 {
		return size16MB
	}
	return size32MB
}

// collectFiles returns all files in directory recursively
func (g *Generator) collectFiles(root string) ([]string, error) {
	var files []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			files = append(files, path)
		}
		return nil
	})

	return files, err
}

// pieceJob represents a chunk of data sent to a hash worker
type pieceJob struct {
	Index int
	Data  []byte
}

// pieceResult stores a completed piece hash for checkpointing
type pieceResult struct {
	Index int
	Hash  []byte
}

// loadCheckpoint retrieves existing piece hashes from database for resume
func (g *Generator) loadCheckpoint(packageID, serverID string) (map[int][]byte, error) {
	query := `
		SELECT piece_index, piece_hash
		FROM torrent_generation_checkpoints
		WHERE package_id = $1 AND server_id = $2
		ORDER BY piece_index
	`
	rows, err := g.db.Query(query, packageID, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	checkpoints := make(map[int][]byte)
	for rows.Next() {
		var index int
		var hash []byte
		if err := rows.Scan(&index, &hash); err != nil {
			return nil, err
		}
		// Validate hash length (must be exactly 20 bytes for SHA1)
		if len(hash) != 20 {
			log.Printf("Warning: invalid checkpoint hash length for piece %d: %d bytes (expected 20)", index, len(hash))
			continue
		}
		checkpoints[index] = hash
	}

	return checkpoints, rows.Err()
}

// saveCheckpointBatch writes a batch of piece hashes to database using efficient COPY
func (g *Generator) saveCheckpointBatch(packageID, serverID string, pieces []pieceResult) error {
	if len(pieces) == 0 {
		return nil
	}

	// Use transaction with multiple INSERT statements (PostgreSQL COPY via lib/pq requires special handling)
	txn, err := g.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer txn.Rollback()

	// Prepare batch insert using VALUES
	stmt, err := txn.Prepare(`
		INSERT INTO torrent_generation_checkpoints (package_id, server_id, piece_index, piece_hash)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (package_id, server_id, piece_index) DO UPDATE SET piece_hash = EXCLUDED.piece_hash
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, p := range pieces {
		if _, err := stmt.Exec(packageID, serverID, p.Index, p.Hash); err != nil {
			return fmt.Errorf("failed to insert checkpoint piece %d: %w", p.Index, err)
		}
	}

	if err := txn.Commit(); err != nil {
		return fmt.Errorf("failed to commit checkpoint batch: %w", err)
	}

	return nil
}

// cleanupCheckpoints removes checkpoint data after successful generation
func (g *Generator) cleanupCheckpoints(packageID, serverID string) error {
	_, err := g.db.Exec(`
		DELETE FROM torrent_generation_checkpoints
		WHERE package_id = $1 AND server_id = $2
	`, packageID, serverID)
	return err
}

// updateCheckpointMetrics updates torrent_queue with checkpoint tracking info
func (g *Generator) updateCheckpointMetrics(packageID, serverID string, checkpointPieces int) error {
	_, err := g.db.Exec(`
		UPDATE torrent_queue
		SET checkpoint_pieces = $1, last_checkpoint_at = NOW()
		WHERE package_id = $2 AND server_id = $3
	`, checkpointPieces, packageID, serverID)
	return err
}

// generatePieces streams file data through hash workers without buffering entire DCP in memory.
// Memory usage is bounded to approximately: pieceSize * numWorkers * 3 (channel buffer + in-flight)
// generatePieces streams file data through hash workers without buffering entire DCP in memory.
// Memory usage is bounded to approximately: pieceSize * numWorkers * 3 (channel buffer + in-flight)
// CHECKPOINT SUPPORT: Loads existing checkpoints and resumes from last completed piece
func (g *Generator) generatePieces(info *metainfo.Info, basePath, packageID, serverID string, totalSize int64) error {
	pieceLength := int(info.PieceLength)

	totalFiles := len(info.Files)
	numWorkers := g.workersNum
	if numWorkers < 1 {
		numWorkers = 1
	}
	// Cap workers to avoid excessive goroutine/memory overhead
	if numWorkers > 16 {
		numWorkers = 16
	}

	log.Printf("Generating pieces for %d files (piece size: %d bytes, using %d hash workers)", totalFiles, pieceLength, numWorkers)

	// Estimate total pieces for pre-allocating results and progress reporting
	estimatedPieces := int(totalSize/int64(pieceLength)) + 1

	// Load existing checkpoints for resume support
	checkpoints, err := g.loadCheckpoint(packageID, serverID)
	if err != nil {
		log.Printf("Warning: failed to load checkpoints: %v (starting fresh)", err)
		checkpoints = make(map[int][]byte)
	}

	resumeFromPiece := len(checkpoints)
	if resumeFromPiece > 0 {
		log.Printf("RESUME: Found %d checkpointed pieces, resuming from piece %d", resumeFromPiece, resumeFromPiece)
		// Update queue to track resume
		g.db.Exec(`UPDATE torrent_queue SET resumed_from_piece = $1 WHERE package_id = $2 AND server_id = $3`,
			resumeFromPiece, packageID, serverID)
	} else {
		log.Printf("Starting fresh torrent generation (no checkpoints found)")
	}

	// Pre-allocate results array for piece hashes (20 bytes each — trivial memory)
	results := make([][]byte, estimatedPieces)

	// Pre-populate results with checkpoint data
	for idx, hash := range checkpoints {
		if idx < len(results) {
			results[idx] = hash
		}
	}

	var resultsMu sync.Mutex
	var processedCount int64 = int64(resumeFromPiece) // Start from checkpoint count
	// Use estimate initially so progress shows "X/~Y" instead of "X/0"
	var totalPieces int64 = int64(estimatedPieces)

	// Buffered channel: only numWorkers*2 pieces in flight at once.
	// This bounds memory to ~pieceSize * numWorkers * 2 (e.g. 16MB * 16 * 2 = 512MB max)
	pieceChan := make(chan pieceJob, numWorkers*2)

	// Checkpoint tracking
	var pendingCheckpoint []pieceResult
	var checkpointMu sync.Mutex
	checkpointsSaved := 0

	// Cancellation tracking (use Int32 for compatibility with Go 1.13)
	var cancelled int32 // 0 = not cancelled, 1 = cancelled
	var cancelErr error
	var cancelErrMu sync.Mutex

	// Start hash workers
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range pieceChan {
				hash := sha1.Sum(job.Data)

				resultsMu.Lock()
				// Grow results slice if needed (estimate may be slightly off)
				for job.Index >= len(results) {
					results = append(results, nil)
				}
				results[job.Index] = hash[:]
				resultsMu.Unlock()

				// Track for checkpointing
				checkpointMu.Lock()
				pendingCheckpoint = append(pendingCheckpoint, pieceResult{
					Index: job.Index,
					Hash:  hash[:],
				})
				needsCheckpoint := len(pendingCheckpoint) >= g.checkpointBatch
				var batchToSave []pieceResult
				if needsCheckpoint {
					batchToSave = make([]pieceResult, len(pendingCheckpoint))
					copy(batchToSave, pendingCheckpoint)
					pendingCheckpoint = pendingCheckpoint[:0]
				}
				checkpointMu.Unlock()

				// Save checkpoint batch if needed (outside of lock)
				if needsCheckpoint {
					if err := g.saveCheckpointBatch(packageID, serverID, batchToSave); err != nil {
						log.Printf("Warning: failed to save checkpoint batch: %v", err)
					} else {
						checkpointsSaved += len(batchToSave)
						if err := g.updateCheckpointMetrics(packageID, serverID, checkpointsSaved); err != nil {
							log.Printf("Warning: failed to update checkpoint metrics: %v", err)
						}
						log.Printf("Checkpoint: saved %d pieces (total checkpointed: %d)", len(batchToSave), checkpointsSaved)
					}

					// Check if hashing was cancelled after checkpoint
					var status, cancelledBy sql.NullString
					err := g.db.QueryRow(`
						SELECT status, cancelled_by FROM torrent_queue
						WHERE package_id = $1 AND server_id = $2
					`, packageID, serverID).Scan(&status, &cancelledBy)
					if err == nil && status.Valid && status.String == "cancelled" {
						reason := "user request"
						if cancelledBy.Valid {
							reason = cancelledBy.String
						}
						log.Printf("Hashing cancelled by %s, stopping generation for package %s", reason, packageID)
						// Signal cancellation to all workers
						atomic.StoreInt32(&cancelled, 1)
						cancelErrMu.Lock()
						cancelErr = fmt.Errorf("hashing cancelled by %s", reason)
						cancelErrMu.Unlock()
						return
					}
				}

				processed := atomic.AddInt64(&processedCount, 1)
				tp := atomic.LoadInt64(&totalPieces)
				if tp > 0 && (processed%100 == 0 || processed == tp) {
					progress := float64(processed) / float64(tp) * 100
					if progress > 100 {
						progress = 100
					}
					statusMsg := fmt.Sprintf("Hashing piece %d/%d", processed, tp)
					g.updateQueueStatus(packageID, serverID, "generating", progress, statusMsg, 0)
					log.Printf("Progress: %.1f%% - %s", progress, statusMsg)
				}
			}
		}()
	}

	// Calculate bytes to skip for resume
	bytesToSkip := int64(resumeFromPiece) * int64(pieceLength)

	// Set piece index to resume point
	pieceIndex := resumeFromPiece

	// Stream files into pieces and send to workers as each piece completes.
	// Only one piece buffer is held by the reader at a time.
	var bytesRead int64 = bytesToSkip // Start from resumed position
	const readBufferSize = 512 * 1024 // 512KB I/O buffer
	const progressUpdateInterval int64 = 50 * 1024 * 1024
	var lastProgressUpdate int64
	var lastStatusTime time.Time
	currentPiece := make([]byte, 0, pieceLength)
	var readErr error

	for _, file := range info.Files {
		fileName := file.DisplayPath(info)

		filePath := filepath.Join(basePath, fileName)
		f, err := os.Open(filePath)
		if err != nil {
			readErr = fmt.Errorf("failed to open file %s: %w", filePath, err)
			break
		}

		// Get file size for seek logic
		fileInfo, err := f.Stat()
		if err != nil {
			f.Close()
			readErr = fmt.Errorf("failed to stat file %s: %w", filePath, err)
			break
		}
		fileSize := fileInfo.Size()

		// If we need to skip bytes and this file contains bytes to skip
		if bytesToSkip > 0 {
			if bytesToSkip >= fileSize {
				// Skip entire file
				bytesToSkip -= fileSize
				f.Close()
				continue
			} else {
				// Seek to position within this file
				_, err := f.Seek(bytesToSkip, 0)
				if err != nil {
					f.Close()
					readErr = fmt.Errorf("failed to seek in file %s: %w", filePath, err)
					break
				}
				bytesToSkip = 0
			}
		}

		buf := make([]byte, readBufferSize)
		for {
			n, err := f.Read(buf)
			if err != nil && err != io.EOF {
				f.Close()
				readErr = fmt.Errorf("failed to read file %s: %w", filePath, err)
				break
			}
			if n == 0 {
				break
			}

			data := buf[:n]

			for len(data) > 0 {
				space := pieceLength - len(currentPiece)
				if space > len(data) {
					space = len(data)
				}

				currentPiece = append(currentPiece, data[:space]...)
				data = data[space:]

				// Piece complete — send to hash workers immediately
				if len(currentPiece) == pieceLength {
					// Skip if we already have this piece from checkpoint
					if _, exists := checkpoints[pieceIndex]; !exists {
						// Copy piece data for the worker (currentPiece buffer is reused)
						pieceData := make([]byte, pieceLength)
						copy(pieceData, currentPiece)
						pieceChan <- pieceJob{Index: pieceIndex, Data: pieceData}
					}
					pieceIndex++
					currentPiece = currentPiece[:0]
				}
			}

			bytesRead += int64(n)
			if totalSize > 0 && (bytesRead-lastProgressUpdate) >= progressUpdateInterval {
				lastProgressUpdate = bytesRead
				if time.Since(lastStatusTime) > 2*time.Second {
					progress := float64(bytesRead) / float64(totalSize) * 50 // reading is first 50%
					if progress > 50 {
						progress = 50
					}
					statusMsg := fmt.Sprintf("Reading %s", fileName)
					g.updateQueueStatus(packageID, serverID, "generating", progress, statusMsg, 0)
					lastStatusTime = time.Now()
				}
			}
		}
		f.Close()
		if readErr != nil {
			break
		}
	}

	// Flush remaining partial piece
	if readErr == nil && len(currentPiece) > 0 {
		if _, exists := checkpoints[pieceIndex]; !exists {
			pieceData := make([]byte, len(currentPiece))
			copy(pieceData, currentPiece)
			pieceChan <- pieceJob{Index: pieceIndex, Data: pieceData}
		}
		pieceIndex++
	}

	// Store final piece count so workers can report completion accurately
	atomic.StoreInt64(&totalPieces, int64(pieceIndex))

	// Signal workers that no more pieces are coming
	close(pieceChan)

	// Wait for all hashing to finish
	wg.Wait()

	// Check if cancelled during hashing
	if atomic.LoadInt32(&cancelled) == 1 {
		cancelErrMu.Lock()
		err := cancelErr
		cancelErrMu.Unlock()
		if err != nil {
			return err
		}
		return fmt.Errorf("hashing cancelled")
	}

	// Save any remaining checkpoint pieces
	checkpointMu.Lock()
	finalBatch := pendingCheckpoint
	checkpointMu.Unlock()

	if len(finalBatch) > 0 {
		if err := g.saveCheckpointBatch(packageID, serverID, finalBatch); err != nil {
			log.Printf("Warning: failed to save final checkpoint batch: %v", err)
		} else {
			checkpointsSaved += len(finalBatch)
			log.Printf("Checkpoint: saved final %d pieces (total checkpointed: %d)", len(finalBatch), checkpointsSaved)
		}
	}

	if readErr != nil {
		return readErr
	}

	if pieceIndex == 0 {
		info.Pieces = []byte{}
		return nil
	}

	// Trim results to actual piece count and combine into final byte slice
	results = results[:pieceIndex]
	pieces := make([]byte, 0, pieceIndex*20)
	for _, hash := range results {
		pieces = append(pieces, hash...)
	}

	log.Printf("Hashing complete: generated %d piece hashes (%d bytes read, %d pieces resumed from checkpoint)", pieceIndex, bytesRead, resumeFromPiece)
	info.Pieces = pieces
	return nil
}

// prePopulatePieceCompletion marks all pieces as complete in torrent_piece_completion.
// Called after torrent generation succeeds, since we just hashed every piece and know
// they're all valid. This avoids a full re-verification on StartSeeding.
func (g *Generator) prePopulatePieceCompletion(infoHash string, totalPieces int) error {
	if totalPieces == 0 {
		return nil
	}

	txn, err := g.db.Begin()
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer txn.Rollback()

	stmt, err := txn.Prepare(`
		INSERT INTO torrent_piece_completion (info_hash, piece_index, completed, verified_at)
		VALUES ($1, $2, true, CURRENT_TIMESTAMP)
		ON CONFLICT (info_hash, piece_index)
		DO UPDATE SET completed = true, verified_at = CURRENT_TIMESTAMP
	`)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	for i := 0; i < totalPieces; i++ {
		if _, err := stmt.Exec(infoHash, i); err != nil {
			return fmt.Errorf("insert piece %d: %w", i, err)
		}
	}

	return txn.Commit()
}

// updateQueueStatus updates the torrent queue status in database.
// totalSizeBytes is optional; when > 0 and status is generating, it sets total_size_bytes for speed/ETA calculation.
func (g *Generator) updateQueueStatus(packageID, serverID, status string, progress float64, currentFile string, totalSizeBytes int64) error {
	var errorMsg sql.NullString
	if status == "failed" && currentFile != "" {
		errorMsg.String = currentFile
		errorMsg.Valid = true
	}

	var startedAt sql.NullTime
	var completedAt sql.NullTime

	now := time.Now()

	if status == "generating" && progress == 0 {
		startedAt.Time = now
		startedAt.Valid = true
	}

	if status == "completed" || status == "failed" {
		completedAt.Time = now
		completedAt.Valid = true
	}

	query := `
		UPDATE torrent_queue
		SET status = $1, progress_percent = $2, current_file = $3, error_message = $4,
		    started_at = COALESCE($5, started_at),
		    completed_at = COALESCE($6, completed_at),
		    total_size_bytes = CASE WHEN $9::bigint > 0 THEN $9::bigint ELSE total_size_bytes END,
		    synced_at = $10,
		    hashing_speed_bps = $11
		WHERE package_id = $7 AND server_id = $8
	`

	// Calculate hashing speed: read started_at from DB if we don't have it locally
	var hashingSpeed sql.NullInt64
	if status == "generating" && progress > 0 && totalSizeBytes > 0 {
		// Look up started_at from the DB for speed calculation
		var dbStartedAt sql.NullTime
		_ = g.db.QueryRow("SELECT started_at FROM torrent_queue WHERE package_id = $1 AND server_id = $2", packageID, serverID).Scan(&dbStartedAt)
		if dbStartedAt.Valid {
			elapsed := now.Sub(dbStartedAt.Time).Seconds()
			if elapsed > 0 {
				bytesSoFar := int64(progress / 100.0 * float64(totalSizeBytes))
				hashingSpeed.Int64 = int64(float64(bytesSoFar) / elapsed)
				hashingSpeed.Valid = true
			}
		}
	}

	_, err := g.db.Exec(query, status, progress, currentFile, errorMsg, startedAt, completedAt, packageID, serverID, totalSizeBytes, now, hashingSpeed)
	return err
}

// SaveTorrentToDatabase stores the generated torrent in the database
func (g *Generator) SaveTorrentToDatabase(mi *metainfo.MetaInfo, infoHash, packageID, serverID string) error {
	// Marshal torrent to bytes
	torrentBytes, err := bencode.Marshal(mi)
	if err != nil {
		return fmt.Errorf("failed to marshal torrent: %w", err)
	}

	// Get torrent info
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return fmt.Errorf("failed to unmarshal info: %w", err)
	}

	totalSize := info.TotalLength()
	fileCount := len(info.Files)
	if fileCount == 0 {
		fileCount = 1 // Single file torrent
	}
	pieceSize := int(info.PieceLength)
	totalPieces := len(info.Pieces) / 20 // Each piece hash is 20 bytes

	// Insert into database
	query := `
		INSERT INTO dcp_torrents (id, package_id, info_hash, torrent_file, piece_size, total_pieces, 
		                          created_by_server_id, file_count, total_size_bytes, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (info_hash) DO NOTHING
	`

	id := uuid.New().String()
	_, err = g.db.Exec(query, id, packageID, infoHash, torrentBytes, pieceSize, totalPieces,
		serverID, fileCount, totalSize, time.Now())

	return err
}

// WriteTorrentFile writes the torrent to a .torrent file
func (g *Generator) WriteTorrentFile(mi *metainfo.MetaInfo, outputPath string) error {
	torrentBytes, err := bencode.Marshal(mi)
	if err != nil {
		return fmt.Errorf("failed to marshal torrent: %w", err)
	}

	return ioutil.WriteFile(outputPath, torrentBytes, 0644)
}
