package main

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

const (
	defaultTrackerURL = "http://79.140.195.19:10851/announce"
	defaultLocalURL   = "http://localhost:10851/announce"
	defaultPublicIP   = "79.140.195.19"
	defaultListenPort = 6950
	pieceSize         = 256 * 1024 // 256KB pieces for quick test
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)

	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <package-dir> [tracker-url] [listen-port]\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nGenerates a torrent for <package-dir>, starts seeding it,\n")
		fmt.Fprintf(os.Stderr, "and announces to the tracker.\n")
		fmt.Fprintf(os.Stderr, "\nDefaults:\n")
		fmt.Fprintf(os.Stderr, "  tracker-url: %s\n", defaultTrackerURL)
		fmt.Fprintf(os.Stderr, "  listen-port: %d\n", defaultListenPort)
		os.Exit(1)
	}

	packageDir := os.Args[1]
	trackerURL := defaultTrackerURL
	localAnnounceURL := defaultLocalURL
	listenPort := defaultListenPort

	if len(os.Args) >= 3 {
		trackerURL = os.Args[2]
		localAnnounceURL = os.Args[2]
	}
	if len(os.Args) >= 4 {
		p, err := strconv.Atoi(os.Args[3])
		if err != nil {
			log.Fatalf("Invalid port: %v", err)
		}
		listenPort = p
	}

	fi, err := os.Stat(packageDir)
	if err != nil || !fi.IsDir() {
		log.Fatalf("Package directory %q does not exist or is not a directory", packageDir)
	}
	packageDir, _ = filepath.Abs(packageDir)

	log.Println("╔══════════════════════════════════════╗")
	log.Println("║   OmniCloud Test Seeder              ║")
	log.Println("╚══════════════════════════════════════╝")
	log.Printf("Package dir:     %s", packageDir)
	log.Printf("Tracker URL:     %s (embedded in .torrent)", trackerURL)
	log.Printf("Local announce:  %s (used by client)", localAnnounceURL)
	log.Printf("Listen port:     %d", listenPort)
	log.Printf("Public IP:       %s", defaultPublicIP)

	// ── Step 1: Generate torrent ──
	log.Println("")
	log.Println("── STEP 1: Generate torrent ──────────────────────")
	mi, infoHash, err := generateTorrent(packageDir, trackerURL)
	if err != nil {
		log.Fatalf("FATAL: Failed to generate torrent: %v", err)
	}
	log.Printf("  Info hash:   %s", infoHash)
	log.Printf("  Announce:    %s", mi.Announce)

	// Write .torrent files
	torrentPath := filepath.Join(filepath.Dir(packageDir), filepath.Base(packageDir)+".torrent")
	torrentBytes, err := bencode.Marshal(mi)
	if err != nil {
		log.Fatalf("FATAL: Failed to marshal torrent: %v", err)
	}
	if err := ioutil.WriteFile(torrentPath, torrentBytes, 0644); err != nil {
		log.Fatalf("FATAL: Failed to write torrent file: %v", err)
	}
	log.Printf("  Torrent file: %s (%d bytes)", torrentPath, len(torrentBytes))

	// Write download-ready version (info as dict for external clients)
	dlBytes, err := marshalForDownload(mi)
	if err != nil {
		log.Fatalf("FATAL: Failed to marshal download torrent: %v", err)
	}
	dlPath := filepath.Join(filepath.Dir(packageDir), filepath.Base(packageDir)+".download.torrent")
	if err := ioutil.WriteFile(dlPath, dlBytes, 0644); err != nil {
		log.Fatalf("FATAL: Failed to write download torrent file: %v", err)
	}
	log.Printf("  Download-ready: %s (%d bytes)", dlPath, len(dlBytes))

	// Verify the download torrent has correct hash
	verifyInfoHash(dlBytes, infoHash)

	// ── Step 2: Create torrent client ──
	log.Println("")
	log.Println("── STEP 2: Create torrent client ──────────────────")

	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = filepath.Dir(packageDir)
	cfg.NoDHT = true
	cfg.DisableUTP = true
	cfg.Seed = true
	cfg.ListenPort = listenPort
	cfg.NoUpload = false

	publicIP := net.ParseIP(defaultPublicIP)
	if publicIP != nil {
		if ipv4 := publicIP.To4(); ipv4 != nil {
			cfg.PublicIp4 = ipv4
			log.Printf("  PublicIp4 set to: %s", defaultPublicIP)
		}
	}

	cl, err := torrent.NewClient(cfg)
	if err != nil {
		log.Fatalf("FATAL: Failed to create torrent client: %v", err)
	}
	defer cl.Close()

	log.Printf("  Client created successfully")
	for _, la := range cl.ListenAddrs() {
		log.Printf("  Listening on: %s", la.String())
	}

	// ── Step 3: Add torrent and verify ──
	log.Println("")
	log.Println("── STEP 3: Add torrent and start seeding ─────────")

	h := sha1.Sum(mi.InfoBytes)
	var ihBytes metainfo.Hash
	copy(ihBytes[:], h[:])
	log.Printf("  Computed info hash from InfoBytes: %s", hex.EncodeToString(h[:]))

	fileStore := storage.NewFile(filepath.Dir(packageDir))
	t, _, err := cl.AddTorrentSpec(&torrent.TorrentSpec{
		InfoHash:  ihBytes,
		InfoBytes: mi.InfoBytes,
		Trackers:  [][]string{{localAnnounceURL}},
		Storage:   fileStore,
	})
	if err != nil {
		log.Fatalf("FATAL: Failed to add torrent: %v", err)
	}

	log.Printf("  Torrent added, waiting for info...")
	<-t.GotInfo()
	log.Printf("  Got info: name=%q total=%d bytes, %d pieces",
		t.Info().Name, t.Info().TotalLength(), t.NumPieces())

	log.Printf("  Verifying data on disk...")
	t.VerifyData()
	time.Sleep(3 * time.Second)

	bytesComplete := t.BytesCompleted()
	bytesTotal := t.Info().TotalLength()
	pct := float64(bytesComplete) / float64(bytesTotal) * 100
	log.Printf("  Verification: %d / %d bytes (%.1f%%)", bytesComplete, bytesTotal, pct)

	if bytesComplete != bytesTotal {
		log.Printf("  WARNING: Data incomplete! Checking piece by piece...")
		for i := 0; i < t.NumPieces(); i++ {
			ps := t.PieceState(i)
			if !ps.Complete {
				log.Printf("    Piece %d: NOT complete (checking=%v partial=%v)", i, ps.Checking, ps.Partial)
			}
		}
	} else {
		log.Printf("  ALL DATA VERIFIED - seeder is ready!")
	}

	// ── Step 4: Monitor ──
	log.Println("")
	log.Println("── STEP 4: Seeding - monitoring ──────────────────")
	log.Printf("Seeding at %s:%d", defaultPublicIP, listenPort)
	log.Printf("Torrent file for downloaders: %s", dlPath)
	log.Printf("Press Ctrl+C to stop")
	log.Println("")

	go monitorSeeding(t, infoHash)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan
	log.Println("\nShutting down test seeder...")
}

func monitorSeeding(t *torrent.Torrent, infoHash string) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		stats := t.Stats()
		conns := len(t.PeerConns())
		complete := t.BytesCompleted()
		total := t.Info().TotalLength()

		log.Printf("[MONITOR] complete=%d/%d peers=%d conns=%d uploaded=%d",
			complete, total, stats.TotalPeers, conns, stats.BytesWrittenData.Int64())

		// Check tracker
		checkTrackerForHash(infoHash)
	}
}

func checkTrackerForHash(infoHash string) {
	resp, err := http.Get("http://localhost:10858/api/v1/tracker/live")
	if err != nil {
		log.Printf("[TRACKER] API error: %v", err)
		return
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	bodyStr := string(body)

	if strings.Contains(bodyStr, infoHash) {
		log.Printf("[TRACKER] Hash %s... FOUND in tracker", infoHash[:16])
	} else {
		log.Printf("[TRACKER] Hash %s... not in tracker yet", infoHash[:16])
	}
}

func generateTorrent(dir, announceURL string) (*metainfo.MetaInfo, string, error) {
	info := metainfo.Info{
		PieceLength: pieceSize,
		Name:        filepath.Base(dir),
	}

	var files []string
	err := filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !fi.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, "", fmt.Errorf("walk dir: %w", err)
	}

	var totalSize int64
	for _, f := range files {
		rel, _ := filepath.Rel(dir, f)
		fi, err := os.Stat(f)
		if err != nil {
			return nil, "", fmt.Errorf("stat %s: %w", f, err)
		}
		info.Files = append(info.Files, metainfo.FileInfo{
			Path:   []string{rel},
			Length: fi.Size(),
		})
		totalSize += fi.Size()
	}
	log.Printf("  Files: %d, Total size: %d bytes (%.2f MB)", len(files), totalSize, float64(totalSize)/1024/1024)

	// Generate piece hashes
	log.Printf("  Hashing pieces...")
	var pieces []byte
	currentPiece := make([]byte, 0, pieceSize)

	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			return nil, "", err
		}
		buf := make([]byte, 64*1024)
		for {
			n, err := fh.Read(buf)
			if err != nil && err != io.EOF {
				fh.Close()
				return nil, "", err
			}
			if n == 0 {
				break
			}
			data := buf[:n]
			for len(data) > 0 {
				space := pieceSize - len(currentPiece)
				if space > len(data) {
					space = len(data)
				}
				currentPiece = append(currentPiece, data[:space]...)
				data = data[space:]
				if len(currentPiece) == pieceSize {
					h := sha1.Sum(currentPiece)
					pieces = append(pieces, h[:]...)
					currentPiece = currentPiece[:0]
				}
			}
		}
		fh.Close()
	}
	if len(currentPiece) > 0 {
		h := sha1.Sum(currentPiece)
		pieces = append(pieces, h[:]...)
	}

	info.Pieces = pieces
	log.Printf("  Generated %d piece hashes", len(pieces)/20)

	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		return nil, "", fmt.Errorf("marshal info: %w", err)
	}

	mi := &metainfo.MetaInfo{
		Announce:     announceURL,
		CreatedBy:    "OmniCloud-TestSeeder",
		CreationDate: time.Now().Unix(),
		InfoBytes:    infoBytes,
	}

	infoHash := mi.HashInfoBytes().HexString()
	return mi, infoHash, nil
}

func marshalForDownload(mi *metainfo.MetaInfo) ([]byte, error) {
	var b []byte
	b = append(b, 'd')
	if mi.Announce != "" {
		b = append(b, []byte("8:announce")...)
		b = append(b, []byte(strconv.Itoa(len(mi.Announce)))...)
		b = append(b, ':')
		b = append(b, []byte(mi.Announce)...)
	}
	if mi.CreatedBy != "" {
		b = append(b, []byte("10:created by")...)
		b = append(b, []byte(strconv.Itoa(len(mi.CreatedBy)))...)
		b = append(b, ':')
		b = append(b, []byte(mi.CreatedBy)...)
	}
	if mi.CreationDate != 0 {
		b = append(b, []byte("13:creation date")...)
		b = append(b, 'i')
		b = append(b, []byte(strconv.FormatInt(mi.CreationDate, 10))...)
		b = append(b, 'e')
	}
	b = append(b, []byte("4:info")...)
	b = append(b, mi.InfoBytes...)
	b = append(b, 'e')
	return b, nil
}

func verifyInfoHash(torrentBytes []byte, expectedHash string) {
	// Extract raw info bytes and verify hash
	infoStart := -1
	for i := 0; i < len(torrentBytes)-5; i++ {
		if string(torrentBytes[i:i+6]) == "4:info" {
			infoStart = i + 6
			break
		}
	}
	if infoStart < 0 {
		log.Printf("  WARNING: Could not find info dict in download torrent")
		return
	}

	// The info dict goes from infoStart to the second-to-last byte (before final 'e')
	infoBytes := torrentBytes[infoStart : len(torrentBytes)-1]
	h := sha1.Sum(infoBytes)
	computed := hex.EncodeToString(h[:])

	if computed == expectedHash {
		log.Printf("  Download torrent hash VERIFIED: %s", computed)
	} else {
		log.Printf("  WARNING: Download torrent hash MISMATCH!")
		log.Printf("    Expected: %s", expectedHash)
		log.Printf("    Got:      %s", computed)
	}
}
