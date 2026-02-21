package main

import (
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

const downloadPort = 10861

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Println("=== OmniCloud Download Test ===")

	if len(os.Args) < 2 {
		log.Fatal("Usage: download-test <torrent-file-or-url>")
	}

	source := os.Args[1]

	// Step 1: Load torrent
	log.Printf("[STEP 1] Loading torrent from: %s", source)

	var torrentBytes []byte
	var err error

	if len(source) > 4 && source[:4] == "http" {
		resp, err := http.Get(source)
		if err != nil {
			log.Fatalf("Failed to download torrent: %v", err)
		}
		defer resp.Body.Close()
		torrentBytes, err = ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Fatalf("Failed to read response: %v", err)
		}
	} else {
		f, err := os.Open(source)
		if err != nil {
			log.Fatalf("Failed to open file: %v", err)
		}
		defer f.Close()
		torrentBytes, err = ioutil.ReadAll(f)
		if err != nil {
			log.Fatalf("Failed to read file: %v", err)
		}
	}

	var mi metainfo.MetaInfo
	if err = bencode.Unmarshal(torrentBytes, &mi); err != nil {
		log.Fatalf("Failed to parse torrent: %v", err)
	}

	info, err := mi.UnmarshalInfo()
	if err != nil {
		log.Fatalf("Failed to unmarshal info: %v", err)
	}

	log.Printf("[STEP 1] ✓ Torrent loaded:")
	log.Printf("  Name: %s", info.Name)
	log.Printf("  Announce: %s", mi.Announce)
	log.Printf("  InfoHash: %s", mi.HashInfoBytes().HexString())
	log.Printf("  Size: %d bytes (%.2f MB)", info.TotalLength(), float64(info.TotalLength())/1024/1024)
	log.Printf("  Pieces: %d (piece size: %d)", info.NumPieces(), info.PieceLength)
	log.Printf("  Files:")
	for _, f := range info.UpvertedFiles() {
		log.Printf("    - %s (%d bytes)", filepath.Join(f.Path...), f.Length)
	}

	// Step 2: Create download client
	log.Println("[STEP 2] Creating download client...")

	downloadDir := "/tmp/omnicloud-download-test"
	os.MkdirAll(downloadDir, 0755)
	// Clean any previous download
	os.RemoveAll(filepath.Join(downloadDir, info.Name))

	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = downloadDir
	cfg.Seed = false // We're downloading, not seeding
	cfg.NoDHT = true
	cfg.DisableUTP = true
	cfg.ListenPort = downloadPort
	cfg.DefaultStorage = storage.NewFileWithCompletion(downloadDir, storage.NewMapPieceCompletion())

	client, err := torrent.NewClient(cfg)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	log.Printf("[STEP 2] ✓ Client created on port %d", client.LocalPort())

	// Step 3: Add torrent
	log.Println("[STEP 3] Adding torrent...")

	t, _, err := client.AddTorrentSpec(&torrent.TorrentSpec{
		InfoHash:  mi.HashInfoBytes(),
		InfoBytes: mi.InfoBytes,
		Trackers:  [][]string{{mi.Announce}},
	})
	if err != nil {
		log.Fatalf("Failed to add torrent: %v", err)
	}

	log.Printf("[STEP 3] ✓ Torrent added")

	// Step 4: Wait for info
	log.Println("[STEP 4] Waiting for torrent info...")
	<-t.GotInfo()
	log.Printf("[STEP 4] ✓ Got info: %s (%d pieces)", t.Name(), t.NumPieces())

	// Step 5: Start download
	log.Println("[STEP 5] Starting download...")
	t.DownloadAll()

	// Step 6: Monitor download progress
	log.Println("[STEP 6] Monitoring download progress...")

	startTime := time.Now()
	lastBytes := int64(0)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		completed := t.BytesCompleted()
		total := t.Length()
		pct := float64(completed) * 100 / float64(total)

		speed := float64(completed-lastBytes) / 2.0
		lastBytes = completed

		stats := t.Stats()

		log.Printf("[DOWNLOAD] %.1f%% (%d/%d) | Speed: %.1f KB/s | Peers: %d active, %d total",
			pct, completed, total, speed/1024, stats.ActivePeers, stats.TotalPeers)

		// Check verified pieces
		verified := 0
		for p := 0; p < t.NumPieces(); p++ {
			ps := t.PieceState(p)
			if ps.Complete && ps.Ok {
				verified++
			}
		}
		log.Printf("[DOWNLOAD]   Verified pieces: %d/%d", verified, t.NumPieces())

		if completed >= total {
			elapsed := time.Since(startTime)
			avgSpeed := float64(total) / elapsed.Seconds()
			log.Printf("[DOWNLOAD] ✓ COMPLETE! %d bytes in %s (%.1f KB/s avg)",
				total, elapsed.Round(time.Millisecond), avgSpeed/1024)

			// Verify downloaded files
			log.Println("[VERIFY] Checking downloaded files...")
			for _, f := range info.UpvertedFiles() {
				fp := filepath.Join(downloadDir, info.Name, filepath.Join(f.Path...))
				fi, err := os.Stat(fp)
				if err != nil {
					log.Printf("[VERIFY] ✗ File missing: %s - %v", fp, err)
				} else {
					log.Printf("[VERIFY] ✓ %s (%d bytes)", fp, fi.Size())
				}
			}
			return
		}

		// Timeout
		if time.Since(startTime) > 120*time.Second {
			log.Println("[DOWNLOAD] ✗ TIMEOUT after 120 seconds")

			// Dump piece states for debugging
			for p := 0; p < t.NumPieces() && p < 10; p++ {
				ps := t.PieceState(p)
				log.Printf("[DEBUG] Piece %d: Complete=%v Ok=%v Checking=%v Partial=%v Priority=%v",
					p, ps.Complete, ps.Ok, ps.Checking, ps.Partial, ps.Priority)
			}
			return
		}
	}
}
