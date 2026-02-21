package main

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

const (
	testFileName = "test-seed-file.bin"
	testFileSize = 5 * 1024 * 1024 // 5 MB
	pieceLength  = 256 * 1024      // 256 KB pieces for fast hashing
	seedPort     = 10860
	trackerURL   = "http://localhost:10851/announce"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Println("=== OmniCloud Standalone Seeder Test ===")

	// Step 1: Create test data directory and file
	dataDir := "/tmp/omnicloud-seed-test"
	torrentName := "SeedTestPackage"
	packageDir := filepath.Join(dataDir, torrentName)
	os.MkdirAll(packageDir, 0755)

	testFilePath := filepath.Join(packageDir, testFileName)
	log.Printf("[STEP 1] Creating test file: %s (%d bytes)", testFilePath, testFileSize)

	testData := make([]byte, testFileSize)
	if _, err := rand.Read(testData); err != nil {
		log.Fatalf("Failed to generate random data: %v", err)
	}
	if err := ioutil.WriteFile(testFilePath, testData, 0644); err != nil {
		log.Fatalf("Failed to write test file: %v", err)
	}
	log.Printf("[STEP 1] ✓ Test file created: %d bytes", testFileSize)

	// Step 2: Generate torrent metainfo
	log.Println("[STEP 2] Generating torrent metainfo...")

	info := metainfo.Info{
		PieceLength: pieceLength,
		Name:        torrentName,
		Files: []metainfo.FileInfo{
			{Path: []string{testFileName}, Length: int64(testFileSize)},
		},
	}

	// Generate piece hashes
	var pieces []byte
	for offset := 0; offset < testFileSize; offset += pieceLength {
		end := offset + pieceLength
		if end > testFileSize {
			end = testFileSize
		}
		h := sha1.Sum(testData[offset:end])
		pieces = append(pieces, h[:]...)
	}
	info.Pieces = pieces
	numPieces := len(pieces) / 20

	log.Printf("[STEP 2] ✓ Generated %d piece hashes (piece size: %d)", numPieces, pieceLength)

	// Create MetaInfo
	mi := &metainfo.MetaInfo{
		Announce:     trackerURL,
		CreatedBy:    "OmniCloud-SeedTest",
		CreationDate: time.Now().Unix(),
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		log.Fatalf("Failed to marshal info: %v", err)
	}
	mi.InfoBytes = infoBytes

	infoHash := mi.HashInfoBytes()
	log.Printf("[STEP 2] ✓ Torrent info hash: %s", infoHash.HexString())
	log.Printf("[STEP 2]   Announce URL: %s", trackerURL)

	// Save .torrent file for download test
	torrentFilePath := filepath.Join(dataDir, "test.torrent")
	torrentFileBytes, err := bencode.Marshal(mi)
	if err != nil {
		log.Fatalf("Failed to marshal torrent: %v", err)
	}
	if err := ioutil.WriteFile(torrentFilePath, torrentFileBytes, 0644); err != nil {
		log.Fatalf("Failed to write torrent file: %v", err)
	}
	log.Printf("[STEP 2] ✓ Saved .torrent file: %s", torrentFilePath)

	// Step 3: Create torrent client configured for seeding
	log.Println("[STEP 3] Creating torrent client...")

	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dataDir
	cfg.Seed = true // *** CRITICAL: Must be true to seed ***
	cfg.NoDHT = true
	cfg.DisableUTP = true
	cfg.NoUpload = false
	cfg.ListenPort = seedPort

	// Set public IP
	cfg.PublicIp4 = net.ParseIP("79.140.195.19").To4()

	// Use file storage with in-memory piece completion to avoid bolt DB lock issues
	cfg.DefaultStorage = storage.NewFileWithCompletion(dataDir, storage.NewMapPieceCompletion())

	client, err := torrent.NewClient(cfg)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	log.Printf("[STEP 3] ✓ Client created on port %d", client.LocalPort())
	log.Printf("[STEP 3]   Seed mode: %v", cfg.Seed)
	log.Printf("[STEP 3]   NoUpload: %v", cfg.NoUpload)
	log.Printf("[STEP 3]   DisableUTP: %v", cfg.DisableUTP)
	log.Printf("[STEP 3]   NoDHT: %v", cfg.NoDHT)

	// Step 4: Add torrent to client
	log.Println("[STEP 4] Adding torrent to client...")

	t, _, err := client.AddTorrentSpec(&torrent.TorrentSpec{
		InfoHash:  infoHash,
		InfoBytes: infoBytes,
		Trackers:  [][]string{{trackerURL}},
	})
	if err != nil {
		log.Fatalf("Failed to add torrent: %v", err)
	}

	log.Printf("[STEP 4] ✓ Torrent added: %s", t.Name())

	// Step 5: Wait for info and trigger verification
	log.Println("[STEP 5] Waiting for torrent info...")
	<-t.GotInfo()
	log.Printf("[STEP 5] ✓ Got torrent info: %d pieces, %d bytes", t.NumPieces(), t.Length())

	// Step 6: Download all (triggers piece verification against existing files)
	log.Println("[STEP 6] Triggering piece verification via DownloadAll()...")
	t.DownloadAll()

	// Wait for verification to complete
	log.Println("[STEP 6] Waiting for piece verification...")
	for i := 0; i < 60; i++ {
		completed := t.BytesCompleted()
		total := t.Length()
		pct := float64(completed) * 100 / float64(total)

		// Check each piece state
		verified := 0
		checking := 0
		incomplete := 0
		for p := 0; p < t.NumPieces(); p++ {
			ps := t.PieceState(p)
			if ps.Complete && ps.Ok {
				verified++
			} else if ps.Checking {
				checking++
			} else {
				incomplete++
			}
		}

		log.Printf("[STEP 6] Verification: %.1f%% (%d/%d bytes) | Verified: %d/%d | Checking: %d | Incomplete: %d",
			pct, completed, total, verified, numPieces, checking, incomplete)

		if completed >= total {
			log.Printf("[STEP 6] ✓ All pieces verified! %d/%d complete", verified, numPieces)
			break
		}

		if i == 59 {
			log.Printf("[STEP 6] ✗ Verification timed out after 60s")
		}

		time.Sleep(1 * time.Second)
	}

	// Step 7: Check seeding status
	log.Println("[STEP 7] Checking seeding status...")
	seeding := t.Seeding()
	stats := t.Stats()
	log.Printf("[STEP 7] Seeding: %v", seeding)
	log.Printf("[STEP 7] Stats: ActivePeers=%d, TotalPeers=%d, BytesCompleted=%d/%d",
		stats.ActivePeers, stats.TotalPeers, t.BytesCompleted(), t.Length())

	if !seeding {
		log.Println("[STEP 7] ✗ NOT SEEDING! Debugging...")
		log.Printf("  cfg.Seed = %v", cfg.Seed)
		log.Printf("  cfg.NoUpload = %v", cfg.NoUpload)
		log.Printf("  BytesCompleted = %d / %d", t.BytesCompleted(), t.Length())
		log.Printf("  NumPieces = %d", t.NumPieces())

		for p := 0; p < t.NumPieces() && p < 5; p++ {
			ps := t.PieceState(p)
			log.Printf("  Piece %d: Complete=%v, Ok=%v, Checking=%v, Partial=%v, Priority=%v",
				p, ps.Complete, ps.Ok, ps.Checking, ps.Partial, ps.Priority)
		}
	} else {
		log.Println("[STEP 7] ✓ SEEDING ACTIVE!")
	}

	// Step 8: Monitor tracker announces and peer connections
	log.Println("[STEP 8] Monitoring tracker announces and peer connections...")
	log.Printf("[STEP 8] Torrent file for download test: %s", torrentFilePath)
	log.Printf("[STEP 8] Info hash: %s", infoHash.HexString())
	log.Printf("")
	log.Printf("To test downloading, run in another terminal:")
	log.Printf("  ./download-test %s", torrentFilePath)
	log.Printf("")

	// Also save torrent bytes to a well-known location
	ioutil.WriteFile("/tmp/omnicloud-seed-test.torrent", torrentFileBytes, 0644)

	// Monitor loop
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			log.Println("Shutting down seeder...")
			return
		case <-ticker.C:
			stats := t.Stats()
			seeding := t.Seeding()
			log.Printf("[MONITOR] Seeding=%v | Peers: active=%d total=%d | Upload: %d bytes | BytesComplete=%d/%d",
				seeding, stats.ActivePeers, stats.TotalPeers,
				stats.BytesWrittenData.Int64(), t.BytesCompleted(), t.Length())

			_ = hex.EncodeToString(infoHash[:])
		}
	}
}
