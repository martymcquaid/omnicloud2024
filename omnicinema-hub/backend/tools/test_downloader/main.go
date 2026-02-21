package main

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

const (
	defaultListenPort = 6951
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)

	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <torrent-file> <download-dir> [listen-port]\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nDownloads content from the given .torrent file using the embedded\n")
		fmt.Fprintf(os.Stderr, "tracker URL. Logs detailed progress including peer connections,\n")
		fmt.Fprintf(os.Stderr, "tracker responses, and download speed.\n")
		fmt.Fprintf(os.Stderr, "\nDefaults:\n")
		fmt.Fprintf(os.Stderr, "  listen-port: %d\n", defaultListenPort)
		os.Exit(1)
	}

	torrentFile := os.Args[1]
	downloadDir := os.Args[2]
	listenPort := defaultListenPort

	if len(os.Args) >= 4 {
		p, err := strconv.Atoi(os.Args[3])
		if err != nil {
			log.Fatalf("Invalid port: %v", err)
		}
		listenPort = p
	}

	// Read torrent file
	torrentBytes, err := ioutil.ReadFile(torrentFile)
	if err != nil {
		log.Fatalf("FATAL: Cannot read torrent file: %v", err)
	}

	log.Println("╔══════════════════════════════════════╗")
	log.Println("║   OmniCloud Test Downloader          ║")
	log.Println("╚══════════════════════════════════════╝")
	log.Printf("Torrent file:  %s (%d bytes)", torrentFile, len(torrentBytes))
	log.Printf("Download dir:  %s", downloadDir)
	log.Printf("Listen port:   %d", listenPort)

	// ── Step 1: Parse torrent ──
	log.Println("")
	log.Println("── STEP 1: Parse torrent file ────────────────────")

	var mi metainfo.MetaInfo
	if err := bencode.Unmarshal(torrentBytes, &mi); err != nil {
		log.Fatalf("FATAL: Failed to parse torrent: %v", err)
	}

	log.Printf("  Announce URL: %s", mi.Announce)
	log.Printf("  Created by:   %s", mi.CreatedBy)

	// Extract raw info bytes for correct hash
	infoBytes, err := extractRawInfoBytes(torrentBytes)
	if err != nil {
		log.Fatalf("FATAL: Failed to extract info bytes: %v", err)
	}

	h := sha1.Sum(infoBytes)
	infoHash := hex.EncodeToString(h[:])
	var ihBytes metainfo.Hash
	copy(ihBytes[:], h[:])

	log.Printf("  Info hash:    %s", infoHash)

	// Parse info to show details
	var info metainfo.Info
	if err := bencode.Unmarshal(infoBytes, &info); err != nil {
		log.Printf("  WARNING: Could not parse info dict: %v", err)
	} else {
		log.Printf("  Name:         %s", info.Name)
		log.Printf("  Total size:   %d bytes (%.2f MB)", info.TotalLength(), float64(info.TotalLength())/1024/1024)
		log.Printf("  Piece length: %d bytes", info.PieceLength)
		log.Printf("  Pieces:       %d", len(info.Pieces)/20)
		log.Printf("  Files:        %d", len(info.Files))
		for _, f := range info.Files {
			log.Printf("    - %s (%d bytes)", filepath.Join(f.Path...), f.Length)
		}
	}

	// ── Step 2: Create download directory and client ──
	log.Println("")
	log.Println("── STEP 2: Create torrent client ──────────────────")

	if err := os.MkdirAll(downloadDir, 0755); err != nil {
		log.Fatalf("FATAL: Cannot create download directory: %v", err)
	}

	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = downloadDir
	cfg.NoDHT = true
	cfg.DisableUTP = true
	cfg.Seed = false
	cfg.ListenPort = listenPort
	cfg.NoUpload = true // We're just downloading

	cl, err := torrent.NewClient(cfg)
	if err != nil {
		log.Fatalf("FATAL: Failed to create torrent client: %v", err)
	}
	defer cl.Close()

	log.Printf("  Client created")
	for _, la := range cl.ListenAddrs() {
		log.Printf("  Listening on: %s", la.String())
	}

	// ── Step 3: Add torrent and start download ──
	log.Println("")
	log.Println("── STEP 3: Add torrent and start download ────────")

	announceURL := mi.Announce
	log.Printf("  Using announce URL: %s", announceURL)

	fileStore := storage.NewFile(downloadDir)
	t, _, err := cl.AddTorrentSpec(&torrent.TorrentSpec{
		InfoHash:  ihBytes,
		InfoBytes: infoBytes,
		Trackers:  [][]string{{announceURL}},
		Storage:   fileStore,
	})
	if err != nil {
		log.Fatalf("FATAL: Failed to add torrent: %v", err)
	}

	log.Printf("  Torrent added, waiting for info...")
	<-t.GotInfo()
	log.Printf("  Got torrent info: %s (%d bytes)", t.Info().Name, t.Info().TotalLength())

	// Start downloading
	t.DownloadAll()
	log.Printf("  Download started!")

	// ── Step 4: Monitor download progress ──
	log.Println("")
	log.Println("── STEP 4: Downloading - monitoring ──────────────")

	done := make(chan struct{})
	go monitorDownload(t, infoHash, done)

	// Also listen for Ctrl+C
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	select {
	case <-done:
		log.Println("")
		log.Println("╔══════════════════════════════════════╗")
		log.Println("║   DOWNLOAD COMPLETE!                 ║")
		log.Println("╚══════════════════════════════════════╝")
		verifyDownloadedData(downloadDir, info)
	case <-sigChan:
		log.Println("\nInterrupted!")
		bytesComplete := t.BytesCompleted()
		bytesTotal := t.Info().TotalLength()
		log.Printf("Progress: %d / %d bytes (%.1f%%)",
			bytesComplete, bytesTotal, float64(bytesComplete)/float64(bytesTotal)*100)
	}
}

func monitorDownload(t *torrent.Torrent, infoHash string, done chan struct{}) {
	startTime := time.Now()
	var lastBytes int64

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		stats := t.Stats()
		conns := len(t.PeerConns())
		complete := t.BytesCompleted()
		total := t.Info().TotalLength()
		pct := float64(complete) / float64(total) * 100
		elapsed := time.Since(startTime)

		// Calculate speed
		speed := float64(complete-lastBytes) / 5.0
		lastBytes = complete

		var speedStr string
		if speed > 1024*1024 {
			speedStr = fmt.Sprintf("%.2f MB/s", speed/1024/1024)
		} else if speed > 1024 {
			speedStr = fmt.Sprintf("%.2f KB/s", speed/1024)
		} else {
			speedStr = fmt.Sprintf("%.0f B/s", speed)
		}

		// ETA
		var eta string
		if speed > 0 {
			remaining := float64(total-complete) / speed
			eta = (time.Duration(remaining) * time.Second).String()
		} else {
			eta = "unknown"
		}

		log.Printf("[DOWNLOAD] %.1f%% (%d/%d) speed=%s peers=%d conns=%d elapsed=%s eta=%s",
			pct, complete, total, speedStr, stats.TotalPeers, conns,
			elapsed.Round(time.Second), eta)

		// Show connected peer count
		peerConns := t.PeerConns()
		if len(peerConns) > 0 {
			log.Printf("  [PEERS] %d active peer connections", len(peerConns))
		} else {
			log.Printf("  [PEERS] No peer connections yet - waiting for tracker response...")
		}

		// Check if complete
		if complete >= total {
			close(done)
			return
		}
	}
}

func verifyDownloadedData(downloadDir string, info metainfo.Info) {
	log.Printf("Verifying downloaded files in: %s", downloadDir)
	packageDir := filepath.Join(downloadDir, info.Name)

	allGood := true
	for _, f := range info.Files {
		fpath := filepath.Join(packageDir, filepath.Join(f.Path...))
		fi, err := os.Stat(fpath)
		if err != nil {
			log.Printf("  MISSING: %s", fpath)
			allGood = false
			continue
		}
		if fi.Size() != f.Length {
			log.Printf("  SIZE MISMATCH: %s (expected %d, got %d)", fpath, f.Length, fi.Size())
			allGood = false
		} else {
			log.Printf("  OK: %s (%d bytes)", filepath.Join(f.Path...), f.Length)
		}
	}

	if allGood {
		log.Println("All files verified successfully!")
	} else {
		log.Println("WARNING: Some files have issues")
	}
}

// extractRawInfoBytes returns the exact raw "info" dict bytes from a .torrent file
func extractRawInfoBytes(torrentBytes []byte) ([]byte, error) {
	if len(torrentBytes) == 0 || torrentBytes[0] != 'd' {
		return nil, fmt.Errorf("invalid bencode: expected dict")
	}
	pos := 1
	for pos < len(torrentBytes) && torrentBytes[pos] != 'e' {
		n, digits := 0, 0
		for pos+digits < len(torrentBytes) && torrentBytes[pos+digits] >= '0' && torrentBytes[pos+digits] <= '9' {
			n = n*10 + int(torrentBytes[pos+digits]-'0')
			digits++
		}
		if digits == 0 || pos+digits >= len(torrentBytes) || torrentBytes[pos+digits] != ':' {
			return nil, fmt.Errorf("invalid bencode key at %d", pos)
		}
		pos += digits + 1
		key := string(torrentBytes[pos : pos+n])
		pos += n
		if pos >= len(torrentBytes) {
			return nil, fmt.Errorf("truncated bencode")
		}
		if key == "info" {
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
						sn, sd := 0, 0
						for pos+sd < len(torrentBytes) && torrentBytes[pos+sd] >= '0' && torrentBytes[pos+sd] <= '9' {
							sn = sn*10 + int(torrentBytes[pos+sd]-'0')
							sd++
						}
						if sd == 0 || pos+sd >= len(torrentBytes) || torrentBytes[pos+sd] != ':' {
							return nil, fmt.Errorf("invalid bencode string in info")
						}
						pos += sd + 1 + sn
					}
				}
				return torrentBytes[start:pos], nil
			} else if torrentBytes[pos] >= '0' && torrentBytes[pos] <= '9' {
				sn, sd := 0, 0
				for pos+sd < len(torrentBytes) && torrentBytes[pos+sd] >= '0' && torrentBytes[pos+sd] <= '9' {
					sn = sn*10 + int(torrentBytes[pos+sd]-'0')
					sd++
				}
				if sd == 0 || pos+sd >= len(torrentBytes) || torrentBytes[pos+sd] != ':' {
					return nil, fmt.Errorf("invalid bencode info value string")
				}
				pos += sd + 1
				return torrentBytes[pos : pos+sn], nil
			}
			return nil, fmt.Errorf("info value neither dict nor string")
		}
		// Skip value
		pos = skipValue(torrentBytes, pos)
		if pos < 0 {
			return nil, fmt.Errorf("failed to skip value")
		}
	}
	return nil, fmt.Errorf("info key not found")
}

func skipValue(data []byte, pos int) int {
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
				n, d := 0, 0
				for pos+d < len(data) && data[pos+d] >= '0' && data[pos+d] <= '9' {
					n = n*10 + int(data[pos+d]-'0')
					d++
				}
				if d == 0 || pos+d >= len(data) || data[pos+d] != ':' {
					return -1
				}
				pos += d + 1 + n
			}
		}
		return pos
	default:
		n, d := 0, 0
		for pos+d < len(data) && data[pos+d] >= '0' && data[pos+d] <= '9' {
			n = n*10 + int(data[pos+d]-'0')
			d++
		}
		if d == 0 || pos+d >= len(data) || data[pos+d] != ':' {
			return -1
		}
		return pos + d + 1 + n
	}
}

func resolveIP(host string) string {
	ips, err := net.LookupIP(host)
	if err != nil {
		return host
	}
	for _, ip := range ips {
		if ipv4 := ip.To4(); ipv4 != nil {
			return ipv4.String()
		}
	}
	if len(ips) > 0 {
		return ips[0].String()
	}
	return host
}
