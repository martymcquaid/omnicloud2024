package torrent

import (
	"bytes"
	"context"
	"crypto/sha1"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strconv"
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

// GenerateTorrent creates a .torrent file for a DCP package
func (g *Generator) GenerateTorrent(ctx context.Context, packagePath, packageID, serverID string) (*metainfo.MetaInfo, string, error) {
	// Calculate total size to determine piece size
	totalSize, _, err := g.calculateDirectorySize(packagePath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to calculate directory size: %w", err)
	}

	// Calculate optimal piece size (16MB for <100GB, 32MB for larger)
	pieceSize := g.calculatePieceSize(totalSize)

	// Update queue status
	if err := g.updateQueueStatus(packageID, serverID, "generating", 0, "Starting torrent generation"); err != nil {
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

	// Generate pieces by hashing files
	err = g.generatePieces(&info, packagePath, packageID, serverID)
	if err != nil {
		g.updateQueueStatus(packageID, serverID, "failed", 0, fmt.Sprintf("Failed to generate pieces: %v", err))
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
	if err := g.updateQueueStatus(packageID, serverID, "completed", 100, ""); err != nil {
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

// generatePieces hashes all files and generates piece hashes
func (g *Generator) generatePieces(info *metainfo.Info, basePath, packageID, serverID string) error {
	var pieces []byte
	pieceLength := info.PieceLength
	currentPiece := make([]byte, 0, pieceLength)

	totalFiles := len(info.Files)
	log.Printf("Generating pieces for %d files (piece size: %d bytes)", totalFiles, pieceLength)

	// Calculate total size across all files
	var totalSize int64
	for _, file := range info.Files {
		totalSize += file.Length
	}
	var bytesHashed int64

	// Start a ticker to update progress every 10 seconds
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	dcpName := filepath.Base(basePath)
	var currentFileName string
	var currentFileIdx int

	// Goroutine to update progress and speed every 10 seconds
	stopTicker := make(chan struct{})
	defer close(stopTicker)
	go func() {
		for {
			select {
			case <-ticker.C:
				progress := float64(bytesHashed) / float64(totalSize) * 100
				statusMsg := fmt.Sprintf("DCP (%s) — file %d/%d: %s", dcpName, currentFileIdx+1, totalFiles, currentFileName)
				g.updateQueueStatus(packageID, serverID, "generating", progress, statusMsg)
				g.updateHashingSpeed(packageID, serverID)
			case <-stopTicker:
				return
			}
		}
	}()

	for fileIdx, file := range info.Files {
		currentFileIdx = fileIdx
		currentFileName = file.DisplayPath(info)

		// Update progress at start of each file
		progress := float64(bytesHashed) / float64(totalSize) * 100
		statusMsg := fmt.Sprintf("DCP (%s) — file %d/%d: %s", dcpName, fileIdx+1, totalFiles, currentFileName)

		log.Printf("Progress: %.1f%% - %s", progress, statusMsg)
		g.updateQueueStatus(packageID, serverID, "generating", progress, statusMsg)
		g.updateHashingSpeed(packageID, serverID)

		filePath := filepath.Join(basePath, file.DisplayPath(info))

		f, err := os.Open(filePath)
		if err != nil {
			return fmt.Errorf("failed to open file %s: %w", filePath, err)
		}

		// Read and hash file in chunks
		buf := make([]byte, 64*1024) // 64KB buffer
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
			bytesHashed += int64(n)

			for len(data) > 0 {
				space := int(pieceLength) - len(currentPiece)
				if space > len(data) {
					space = len(data)
				}

				currentPiece = append(currentPiece, data[:space]...)
				data = data[space:]

				if len(currentPiece) == int(pieceLength) {
					// Hash completed piece
					hash := sha1.Sum(currentPiece)
					pieces = append(pieces, hash[:]...)
					currentPiece = currentPiece[:0]
				}
			}
		}
		f.Close()
	}

	// Hash remaining partial piece
	if len(currentPiece) > 0 {
		hash := sha1.Sum(currentPiece)
		pieces = append(pieces, hash[:]...)
	}

	info.Pieces = pieces
	return nil
}

// updateQueueStatus updates the torrent queue status in database
func (g *Generator) updateQueueStatus(packageID, serverID, status string, progress float64, currentFile string) error {
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
		    completed_at = COALESCE($6, completed_at)
		WHERE package_id = $7 AND server_id = $8
	`

	_, err := g.db.Exec(query, status, progress, currentFile, errorMsg, startedAt, completedAt, packageID, serverID)
	return err
}

// updateHashingSpeed calculates and updates hashing speed based on progress and elapsed time
func (g *Generator) updateHashingSpeed(packageID, serverID string) error {
	query := `
		UPDATE torrent_queue
		SET hashing_speed_bps = CASE 
		        WHEN status = 'generating' AND started_at IS NOT NULL AND progress_percent > 0 THEN
		            CASE WHEN EXTRACT(EPOCH FROM (NOW() - started_at)) > 0 THEN
		                CAST(ROUND((COALESCE(total_size_bytes, 0) * progress_percent / 100.0) / EXTRACT(EPOCH FROM (NOW() - started_at))) AS bigint)
		            ELSE 0 END
		        ELSE hashing_speed_bps
		    END
		WHERE package_id = $1 AND server_id = $2 AND status = 'generating'
	`
	_, err := g.db.Exec(query, packageID, serverID)
	return err
}
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

// extractRawInfoBytes returns the exact raw "info" dict bytes from a .torrent file.
// This preserves bencode key ordering so the info hash matches; unmarshaling can re-encode
// with different key order and cause "info bytes have wrong hash".
func extractRawInfoBytes(torrentBytes []byte) ([]byte, error) {
	if len(torrentBytes) == 0 || torrentBytes[0] != 'd' {
		return nil, errors.New("invalid bencode: expected dict")
	}
	pos := 1
	for pos < len(torrentBytes) && torrentBytes[pos] != 'e' {
		// key: <len>:<bytes>
		n, digits := 0, 0
		for pos+digits < len(torrentBytes) && torrentBytes[pos+digits] >= '0' && torrentBytes[pos+digits] <= '9' {
			n = n*10 + int(torrentBytes[pos+digits]-'0')
			digits++
		}
		if digits == 0 || pos+digits >= len(torrentBytes) || torrentBytes[pos+digits] != ':' {
			return nil, errors.New("invalid bencode key")
		}
		pos += digits + 1
		key := string(torrentBytes[pos : pos+n])
		pos += n
		if pos >= len(torrentBytes) {
			return nil, errors.New("invalid bencode: truncated")
		}
		if key == "info" {
			// Value can be a bencode dict 'd'...'e' or a bencode string <len>:<bytes> (how metainfo stores raw InfoBytes)
			if torrentBytes[pos] == 'd' {
				start := pos
				depth := 1
				pos++
				for pos < len(torrentBytes) && depth > 0 {
					switch torrentBytes[pos] {
					case 'i':
						pos++
						for pos < len(torrentBytes) && torrentBytes[pos] != 'e' {
							pos++
						}
						if pos < len(torrentBytes) {
							pos++
						}
					case 'l', 'd':
						depth++
						pos++
					case 'e':
						depth--
						pos++
					default:
						// string: <len>:<bytes>
						n, digits := 0, 0
						for pos+digits < len(torrentBytes) && torrentBytes[pos+digits] >= '0' && torrentBytes[pos+digits] <= '9' {
							n = n*10 + int(torrentBytes[pos+digits]-'0')
							digits++
						}
						if digits == 0 || pos+digits >= len(torrentBytes) || torrentBytes[pos+digits] != ':' {
							return nil, errors.New("invalid bencode string in info dict")
						}
						pos += digits + 1 + n
					}
				}
				return torrentBytes[start:pos], nil
			} else if torrentBytes[pos] >= '0' && torrentBytes[pos] <= '9' {
				// "info" value is a bencode string (how metainfo.InfoBytes is marshaled by anacrolix when using bencode.Marshal)
				n, digits := 0, 0
				for pos+digits < len(torrentBytes) && torrentBytes[pos+digits] >= '0' && torrentBytes[pos+digits] <= '9' {
					n = n*10 + int(torrentBytes[pos+digits]-'0')
					digits++
				}
				if digits == 0 || pos+digits >= len(torrentBytes) || torrentBytes[pos+digits] != ':' {
					return nil, errors.New("invalid bencode: info value string")
				}
				pos += digits + 1
				if pos+n > len(torrentBytes) {
					return nil, errors.New("invalid bencode: info string truncated")
				}
				return torrentBytes[pos : pos+n], nil
			} else {
				return nil, errors.New("invalid bencode: info value neither dict nor string")
			}
		} else {
			// Skip this key's value
			pos = skipBencodeValue(torrentBytes, pos)
			if pos < 0 {
				return nil, errors.New("invalid bencode: failed to skip value")
			}
		}
	}
	return nil, errors.New("info key not found")
}

func skipBencodeValue(data []byte, pos int) int {
	if pos >= len(data) {
		return -1
	}
	switch data[pos] {
	case 'i':
		pos++
		for pos < len(data) && data[pos] != 'e' {
			pos++
		}
		if pos < len(data) {
			return pos + 1
		}
		return -1
	case 'l', 'd':
		depth := 1
		pos++
		for pos < len(data) && depth > 0 {
			switch data[pos] {
			case 'i':
				pos++
				for pos < len(data) && data[pos] != 'e' {
					pos++
				}
				if pos < len(data) {
					pos++
				}
			case 'l', 'd':
				depth++
				pos++
			case 'e':
				depth--
				pos++
			default:
				n, digits := 0, 0
				for pos+digits < len(data) && data[pos+digits] >= '0' && data[pos+digits] <= '9' {
					n = n*10 + int(data[pos+digits]-'0')
					digits++
				}
				if digits == 0 || pos+digits >= len(data) || data[pos+digits] != ':' {
					return -1
				}
				pos += digits + 1 + n
			}
		}
		return pos
	default:
		// string
		n, digits := 0, 0
		for pos+digits < len(data) && data[pos+digits] >= '0' && data[pos+digits] <= '9' {
			n = n*10 + int(data[pos+digits]-'0')
			digits++
		}
		if digits == 0 || pos+digits >= len(data) || data[pos+digits] != ':' {
			return -1
		}
		return pos + digits + 1 + n
	}
}

// MarshalTorrentForDownload builds standard bencode bytes with "info" as a dict (not a string)
// so external torrent clients accept it. Internal storage uses marshalTorrentWithRawInfo (info as string)
// to preserve hash, but downloads must use the standard format.
func MarshalTorrentForDownload(mi *metainfo.MetaInfo) ([]byte, error) {
	if len(mi.InfoBytes) == 0 {
		return nil, fmt.Errorf("empty info bytes")
	}
	var b bytes.Buffer
	b.WriteByte('d')
	// 8:announce (alphabetically before "info" for standard ordering)
	if mi.Announce != "" {
		b.WriteString("8:announce")
		b.WriteString(strconv.Itoa(len(mi.Announce)))
		b.WriteByte(':')
		b.WriteString(mi.Announce)
	}
	// 10:created by
	if mi.CreatedBy != "" {
		b.WriteString("10:created by")
		b.WriteString(strconv.Itoa(len(mi.CreatedBy)))
		b.WriteByte(':')
		b.WriteString(mi.CreatedBy)
	}
	// 13:creation date
	if mi.CreationDate != 0 {
		b.WriteString("13:creation date")
		b.WriteByte('i')
		b.WriteString(strconv.FormatInt(mi.CreationDate, 10))
		b.WriteByte('e')
	}
	// 4:info <raw dict bytes> (NOT wrapped in a string; raw dict so external clients accept it)
	b.WriteString("4:info")
	b.Write(mi.InfoBytes)
	b.WriteByte('e')
	return b.Bytes(), nil
}

// ConvertToStandardFormat converts stored torrent bytes (info as string) to standard format (info as dict)
// for external client compatibility. Optionally rewrites announce URL; if empty, uses the stored announce.
func ConvertToStandardFormat(torrentBytes []byte, newAnnounce string) ([]byte, error) {
	if len(torrentBytes) == 0 {
		return nil, fmt.Errorf("empty torrent bytes")
	}
	var mi metainfo.MetaInfo
	if err := bencode.Unmarshal(torrentBytes, &mi); err != nil {
		return nil, fmt.Errorf("unmarshal torrent: %w", err)
	}
	rawInfo, err := extractRawInfoBytes(torrentBytes)
	if err != nil {
		return nil, fmt.Errorf("extract raw info: %w", err)
	}
	if len(rawInfo) == 0 {
		return nil, fmt.Errorf("empty info dict")
	}
	mi.InfoBytes = rawInfo
	if newAnnounce != "" {
		mi.Announce = newAnnounce
	}
	out, err := MarshalTorrentForDownload(&mi)
	if err != nil {
		return nil, err
	}
	// Sanity check: re-parse to ensure valid bencode and extract info
	if _, err := extractRawInfoBytes(out); err != nil {
		return nil, fmt.Errorf("converted torrent invalid: %w", err)
	}
	return out, nil
}
