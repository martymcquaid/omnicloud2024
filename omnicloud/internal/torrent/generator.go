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

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/bencode"
	"github.com/google/uuid"
)

// Generator handles torrent file creation for DCPs
type Generator struct {
	db         *sql.DB
	trackerURL string
	workersNum int
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
		db:         db,
		trackerURL: trackerURL,
		workersNum: workers,
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

	// Update queue status to completed
	if err := g.updateQueueStatus(packageID, serverID, "completed", 100, "", 0); err != nil {
		return nil, "", fmt.Errorf("failed to update completion status: %w", err)
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

// Piece represents a chunk of data to be hashed
type Piece struct {
	Index int
	Data  []byte
}

// generatePieces hashes all files and generates piece hashes with parallel worker pool
func (g *Generator) generatePieces(info *metainfo.Info, basePath, packageID, serverID string, totalSize int64) error {
	pieceLength := info.PieceLength

	totalFiles := len(info.Files)
	log.Printf("Generating pieces for %d files (piece size: %d bytes, using %d hash workers)", totalFiles, pieceLength, g.workersNum)

	// Step 1: Read all files and build pieces list
	var allPieces []*Piece
	var bytesRead int64
	const readBufferSize = 512 * 1024 // 512KB buffer for better I/O
	const progressUpdateInterval = 10 * 1024 * 1024
	var lastProgressUpdate int64
	var lastStatusTime time.Time
	currentPiece := make([]byte, 0, pieceLength)
	pieceIndex := 0

	for _, file := range info.Files {
		fileName := file.DisplayPath(info)
		statusMsg := fmt.Sprintf("Reading %s", fileName)
		if time.Since(lastStatusTime) > 2*time.Second {
			progress := float64(0)
			if totalSize > 0 {
				progress = float64(bytesRead) / float64(totalSize) * 100
			}
			if progress > 100 {
				progress = 100
			}
			g.updateQueueStatus(packageID, serverID, "generating", progress, statusMsg, 0)
			lastStatusTime = time.Now()
		}

		filePath := filepath.Join(basePath, file.DisplayPath(info))
		f, err := os.Open(filePath)
		if err != nil {
			return fmt.Errorf("failed to open file %s: %w", filePath, err)
		}

		buf := make([]byte, readBufferSize)
		for {
			n, err := f.Read(buf)
			if err != nil && err != io.EOF {
				f.Close()
				return fmt.Errorf("failed to read file %s: %w", filePath, err)
			}
			if n == 0 {
				break
			}

			data := buf[:n]
			for len(data) > 0 {
				space := int(pieceLength) - len(currentPiece)
				if space > len(data) {
					space = len(data)
				}

				currentPiece = append(currentPiece, data[:space]...)
				data = data[space:]

				// Complete piece: add to list for hashing
				if len(currentPiece) == int(pieceLength) {
					pieceCopy := make([]byte, len(currentPiece))
					copy(pieceCopy, currentPiece)
					allPieces = append(allPieces, &Piece{Index: pieceIndex, Data: pieceCopy})
					pieceIndex++
					currentPiece = currentPiece[:0]
				}
			}

			bytesRead += int64(n)
			// Update progress periodically
			if totalSize > 0 && (bytesRead-lastProgressUpdate) >= progressUpdateInterval {
				lastProgressUpdate = bytesRead
				progress := float64(bytesRead) / float64(totalSize) * 100
				if progress > 100 {
					progress = 100
				}
				g.updateQueueStatus(packageID, serverID, "generating", progress, statusMsg, 0)
			}
		}
		f.Close()
	}

	// Remaining partial piece
	if len(currentPiece) > 0 {
		pieceCopy := make([]byte, len(currentPiece))
		copy(pieceCopy, currentPiece)
		allPieces = append(allPieces, &Piece{Index: pieceIndex, Data: pieceCopy})
	}

	totalPieces := len(allPieces)
	log.Printf("Read complete: %d pieces to hash (%d bytes)", totalPieces, bytesRead)

	// Step 2: Hash pieces in parallel using worker pool
	if totalPieces == 0 {
		info.Pieces = []byte{}
		return nil
	}

	// Create results array (pre-allocated)
	results := make([][]byte, totalPieces)
	resultsMutex := sync.Mutex{}
	var processedCount int64

	// Worker pool: spawn workers to hash pieces
	numWorkers := g.workersNum
	if numWorkers > totalPieces {
		numWorkers = totalPieces // no point having more workers than pieces
	}
	if numWorkers < 1 {
		numWorkers = 1
	}

	pieceChan := make(chan *Piece, numWorkers*2)
	var wg sync.WaitGroup

	// Start workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for piece := range pieceChan {
				// Hash the piece
				hash := sha1.Sum(piece.Data)
				hashCopy := make([]byte, 20)
				copy(hashCopy, hash[:])

				// Store result
				resultsMutex.Lock()
				results[piece.Index] = hashCopy
				resultsMutex.Unlock()

				// Update progress
				processed := atomic.AddInt64(&processedCount, 1)
				if processed%100 == 0 || processed == int64(totalPieces) {
					progress := float64(processed) / float64(totalPieces) * 100
					statusMsg := fmt.Sprintf("Hashing piece %d/%d", processed, totalPieces)
					g.updateQueueStatus(packageID, serverID, "generating", progress, statusMsg, 0)
					log.Printf("Progress: %.1f%% - %s", progress, statusMsg)
				}
			}
		}()
	}

	// Send pieces to workers
	for _, piece := range allPieces {
		pieceChan <- piece
	}
	close(pieceChan)

	// Wait for all workers
	wg.Wait()

	// Step 3: Combine results into final pieces byte slice
	var pieces []byte
	for _, hash := range results {
		pieces = append(pieces, hash...)
	}

	log.Printf("Hashing complete: generated %d piece hashes", len(results))
	info.Pieces = pieces
	return nil
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

	if status == "generating" && progress == 0 {
		startedAt.Time = time.Now()
		startedAt.Valid = true
	}

	if status == "completed" || status == "failed" {
		completedAt.Time = time.Now()
		completedAt.Valid = true
	}

	query := `
		UPDATE torrent_queue
		SET status = $1, progress_percent = $2, current_file = $3, error_message = $4,
		    started_at = COALESCE($5, started_at),
		    completed_at = COALESCE($6, completed_at),
		    total_size_bytes = CASE WHEN ($9::bigint) > 0 THEN ($9::bigint) ELSE total_size_bytes END
		WHERE package_id = $7 AND server_id = $8
	`

	_, err := g.db.Exec(query, status, progress, currentFile, errorMsg, startedAt, completedAt, packageID, serverID, totalSizeBytes)
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
