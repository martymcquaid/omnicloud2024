package main

import (
	"crypto/sha256"
	"fmt"
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

const downloadPort = 19853

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Println("╔══════════════════════════════════════════╗")
	log.Println("║   TORRENT DOWNLOAD TEST                  ║")
	log.Println("╚══════════════════════════════════════════╝")

	if len(os.Args) < 2 {
		log.Fatal("Usage: download-test <path-to-torrent-file OR http://url>")
	}
	source := os.Args[1]

	// Step 1: Load torrent
	log.Printf("\n[1/6] Loading torrent from: %s", source)
	var torrentBytes []byte
	var err error

	if len(source) > 4 && source[:4] == "http" {
		resp, err := http.Get(source)
		if err != nil {
			log.Fatalf("HTTP GET failed: %v", err)
		}
		defer resp.Body.Close()
		torrentBytes, err = ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Fatalf("Failed to read HTTP body: %v", err)
		}
		log.Printf("[1/6] Downloaded %d bytes from URL", len(torrentBytes))
	} else {
		torrentBytes, err = ioutil.ReadFile(source)
		if err != nil {
			log.Fatalf("Failed to read file: %v", err)
		}
		log.Printf("[1/6] Read %d bytes from file", len(torrentBytes))
	}

	var mi metainfo.MetaInfo
	if err := bencode.Unmarshal(torrentBytes, &mi); err != nil {
		log.Fatalf("Failed to parse torrent: %v", err)
	}

	info, err := mi.UnmarshalInfo()
	if err != nil {
		log.Fatalf("Failed to unmarshal info: %v", err)
	}

	infoHash := mi.HashInfoBytes()
	log.Printf("[1/6] Torrent parsed successfully:")
	log.Printf("  Name:      %s", info.Name)
	log.Printf("  Announce:  %s", mi.Announce)
	log.Printf("  InfoHash:  %s", infoHash.HexString())
	log.Printf("  Size:      %d bytes (%.2f MB)", info.TotalLength(), float64(info.TotalLength())/1024/1024)
	log.Printf("  Pieces:    %d (piece size: %d)", info.NumPieces(), info.PieceLength)
	log.Printf("  Files:")
	for _, f := range info.UpvertedFiles() {
		log.Printf("    - %s (%d bytes)", filepath.Join(f.Path...), f.Length)
	}

	// Step 2: Prepare download directory
	downloadDir := "/tmp/torrent-download-test"
	os.RemoveAll(filepath.Join(downloadDir, info.Name)) // clean old data
	os.MkdirAll(downloadDir, 0755)
	log.Printf("\n[2/6] Download directory: %s", downloadDir)

	// Step 3: Create client
	log.Printf("\n[3/6] Creating download client...")

	completion := storage.NewMapPieceCompletion()
	clientStorage := storage.NewFileWithCompletion(downloadDir, completion)

	cfg := torrent.NewDefaultClientConfig()
	cfg.Seed = false
	cfg.NoDHT = true
	cfg.DisableUTP = true
	cfg.ListenPort = downloadPort
	cfg.DefaultStorage = clientStorage

	client, err := torrent.NewClient(cfg)
	if err != nil {
		log.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	log.Printf("[3/6] DONE — Client on port %d", client.LocalPort())

	// Step 4: Add torrent
	log.Printf("\n[4/6] Adding torrent...")

	t, _, err := client.AddTorrentSpec(&torrent.TorrentSpec{
		InfoHash:  infoHash,
		InfoBytes: mi.InfoBytes,
		Trackers:  [][]string{{mi.Announce}},
	})
	if err != nil {
		log.Fatalf("AddTorrentSpec: %v", err)
	}

	<-t.GotInfo()
	log.Printf("[4/6] DONE — name=%s pieces=%d", t.Name(), t.NumPieces())

	// Step 5: Download
	log.Printf("\n[5/6] Starting download...")
	t.DownloadAll()

	startTime := time.Now()
	lastBytes := int64(0)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()

	for range tick.C {
		bc := t.BytesCompleted()
		total := t.Length()
		pct := float64(bc) / float64(total) * 100

		speed := float64(bc-lastBytes) / 2.0
		lastBytes = bc

		complete, checking, incomplete := 0, 0, 0
		for i := 0; i < t.NumPieces(); i++ {
			ps := t.PieceState(i)
			if ps.Complete {
				complete++
			} else if ps.Checking {
				checking++
			} else {
				incomplete++
			}
		}

		st := t.Stats()
		conns := t.PeerConns()

		log.Printf("[5/6] %.1f%% (%d/%d) | %.1f KB/s | pieces: %d/%d ok, %d checking, %d inc | peers=%d conns=%d",
			pct, bc, total, speed/1024,
			complete, t.NumPieces(), checking, incomplete,
			st.TotalPeers, len(conns))

		if len(conns) > 0 {
			log.Printf("  %d active peer connection(s)", len(conns))
		}

		if bc >= total {
			elapsed := time.Since(startTime)
			avgSpeed := float64(total) / elapsed.Seconds()
			log.Printf("[5/6] DOWNLOAD COMPLETE — %d bytes in %s (%.1f KB/s avg)",
				total, elapsed.Round(time.Millisecond), avgSpeed/1024)
			break
		}

		if time.Since(startTime) > 120*time.Second {
			log.Printf("[5/6] ✗ TIMEOUT after 120s")
			for i := 0; i < t.NumPieces() && i < 10; i++ {
				ps := t.PieceState(i)
				log.Printf("  piece[%d] Complete=%v Ok=%v Checking=%v Partial=%v",
					i, ps.Complete, ps.Ok, ps.Checking, ps.Partial)
			}
			os.Exit(1)
		}
	}

	// Step 6: Verify files
	log.Printf("\n[6/6] Verifying downloaded files...")
	allGood := true
	for _, f := range info.UpvertedFiles() {
		fp := filepath.Join(downloadDir, info.Name, filepath.Join(f.Path...))
		fi, err := os.Stat(fp)
		if err != nil {
			log.Printf("  ✗ MISSING: %s — %v", fp, err)
			allGood = false
			continue
		}
		if fi.Size() != f.Length {
			log.Printf("  ✗ SIZE MISMATCH: %s — expected %d got %d", fp, f.Length, fi.Size())
			allGood = false
			continue
		}
		data, _ := ioutil.ReadFile(fp)
		hash := sha256.Sum256(data)
		log.Printf("  ✓ %s (%d bytes, sha256=%x…)", filepath.Base(fp), fi.Size(), hash[:8])
	}

	if allGood {
		log.Printf("\n[RESULT] ALL FILES VERIFIED — Download successful!")
		fmt.Println("\n  SUCCESS")
	} else {
		log.Printf("\n[RESULT] ✗ SOME FILES FAILED VERIFICATION")
		fmt.Println("\n  FAILED")
		os.Exit(1)
	}
}
