package main

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
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
	testFileName = "testdata.bin"
	testFileSize = 2 * 1024 * 1024 // 2 MB
	pieceLength  = 256 * 1024      // 256 KB pieces
	trackerPort  = 19851
	seedPort     = 19852
)

// ---- Minimal embedded tracker ----

type miniTracker struct {
	peers map[string]peerEntry // key: peer_id
}

type peerEntry struct {
	ip   string
	port int
	left int64
}

func (mt *miniTracker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	rawHash := q.Get("info_hash")
	decoded, err := url.QueryUnescape(rawHash)
	if err != nil || len(decoded) != 20 {
		log.Printf("[TRACKER] ✗ Invalid info_hash len=%d err=%v raw=%q", len(decoded), err, rawHash)
		bencode.NewEncoder(w).Encode(map[string]interface{}{"failure reason": "Invalid info_hash"})
		return
	}
	infoHash := hex.EncodeToString([]byte(decoded))

	peerID := q.Get("peer_id")
	port, _ := strconv.Atoi(q.Get("port"))
	left, _ := strconv.ParseInt(q.Get("left"), 10, 64)
	event := q.Get("event")

	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip == "127.0.0.1" || ip == "::1" {
		ip = "127.0.0.1"
	}

	log.Printf("[TRACKER] Announce: hash=%s peer=%s ip=%s port=%d event=%s left=%d",
		infoHash[:16]+"…", peerID[:12]+"…", ip, port, event, left)

	if event == "stopped" {
		delete(mt.peers, peerID)
	} else {
		mt.peers[peerID] = peerEntry{ip: ip, port: port, left: left}
	}

	// Build compact peer list
	var peerBytes []byte
	seeders, leechers := 0, 0
	for pid, p := range mt.peers {
		if p.left == 0 {
			seeders++
		} else {
			leechers++
		}
		if pid == peerID {
			continue // don't send peer to itself
		}
		parsed := net.ParseIP(p.ip).To4()
		if parsed == nil {
			continue
		}
		peerBytes = append(peerBytes, parsed[0], parsed[1], parsed[2], parsed[3],
			byte(p.port>>8), byte(p.port&0xff))
	}

	resp := map[string]interface{}{
		"interval":   30,
		"complete":   seeders,
		"incomplete": leechers,
		"peers":      string(peerBytes),
	}

	log.Printf("[TRACKER] Response: seeders=%d leechers=%d peers_returned=%d",
		seeders, leechers, len(peerBytes)/6)

	w.Header().Set("Content-Type", "text/plain")
	bencode.NewEncoder(w).Encode(resp)
}

func startTracker() string {
	mt := &miniTracker{peers: make(map[string]peerEntry)}
	addr := fmt.Sprintf(":%d", trackerPort)
	go func() {
		log.Printf("[TRACKER] Starting on %s", addr)
		if err := http.ListenAndServe(addr, mt); err != nil {
			log.Fatalf("[TRACKER] Failed to start: %v", err)
		}
	}()
	time.Sleep(200 * time.Millisecond)
	return fmt.Sprintf("http://127.0.0.1:%d/announce", trackerPort)
}

// ---- Main ----

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Println("╔══════════════════════════════════════════╗")
	log.Println("║   TORRENT SEED TEST — FULL PIPELINE      ║")
	log.Println("╚══════════════════════════════════════════╝")

	// Step 0: Start embedded tracker
	trackerURL := startTracker()
	log.Printf("[SETUP] Tracker URL: %s", trackerURL)

	// Step 1: Create test data
	dataDir := "/tmp/torrent-seed-test"
	os.RemoveAll(dataDir) // clean start
	torrentName := "TestDCP"
	packageDir := filepath.Join(dataDir, torrentName)
	os.MkdirAll(packageDir, 0755)

	testFilePath := filepath.Join(packageDir, testFileName)
	log.Printf("\n[1/8] Creating test file: %s (%d bytes)", testFilePath, testFileSize)

	testData := make([]byte, testFileSize)
	rand.Read(testData)
	ioutil.WriteFile(testFilePath, testData, 0644)

	// Also create a small second file to mimic multi-file DCP
	smallData := []byte("This is a test ASSETMAP file for torrent testing.\n")
	ioutil.WriteFile(filepath.Join(packageDir, "ASSETMAP"), smallData, 0644)

	log.Printf("[1/8] DONE — Created %d byte test file + ASSETMAP", testFileSize)

	// Step 2: Build torrent metainfo (same as our generator does)
	log.Printf("\n[2/8] Generating torrent metainfo...")

	files := []struct {
		name string
		size int64
		data []byte
	}{
		{testFileName, int64(testFileSize), testData},
		{"ASSETMAP", int64(len(smallData)), smallData},
	}

	info := metainfo.Info{
		PieceLength: pieceLength,
		Name:        torrentName,
	}
	for _, f := range files {
		info.Files = append(info.Files, metainfo.FileInfo{
			Path:   []string{f.name},
			Length: f.size,
		})
	}

	// Concatenate all file data and hash pieces (exactly as anacrolix does)
	var allData []byte
	for _, f := range files {
		allData = append(allData, f.data...)
	}

	var pieces []byte
	numPieces := 0
	for off := 0; off < len(allData); off += pieceLength {
		end := off + pieceLength
		if end > len(allData) {
			end = len(allData)
		}
		h := sha1.Sum(allData[off:end])
		pieces = append(pieces, h[:]...)
		numPieces++
	}
	info.Pieces = pieces

	log.Printf("[2/8] Pieces: %d (piece size: %d, total data: %d bytes)", numPieces, pieceLength, len(allData))

	// Marshal info dict
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		log.Fatalf("Marshal info: %v", err)
	}

	mi := &metainfo.MetaInfo{
		Announce:     trackerURL,
		CreatedBy:    "SeedTest",
		CreationDate: time.Now().Unix(),
	}
	mi.InfoBytes = infoBytes

	infoHash := mi.HashInfoBytes()
	log.Printf("[2/8] Info hash: %s", infoHash.HexString())
	log.Printf("[2/8] Announce: %s", trackerURL)

	// Save .torrent file
	torrentFileBytes, _ := bencode.Marshal(mi)
	torrentPath := filepath.Join(dataDir, "test.torrent")
	ioutil.WriteFile(torrentPath, torrentFileBytes, 0644)
	log.Printf("[2/8] DONE — Saved %s (%d bytes)", torrentPath, len(torrentFileBytes))

	// Also verify: re-parse the torrent bytes and confirm info_hash matches
	log.Printf("\n[3/8] Verifying torrent file round-trip...")
	var mi2 metainfo.MetaInfo
	if err := bencode.Unmarshal(torrentFileBytes, &mi2); err != nil {
		log.Fatalf("Failed to re-parse torrent: %v", err)
	}
	hash2 := mi2.HashInfoBytes()
	if hash2 != infoHash {
		log.Fatalf("[3/8] ✗ HASH MISMATCH after round-trip! Original=%s Reparsed=%s",
			infoHash.HexString(), hash2.HexString())
	}
	log.Printf("[3/8] DONE — Round-trip hash matches: %s", hash2.HexString())

	// Step 4: Create torrent client for seeding
	log.Printf("\n[4/8] Creating torrent client for seeding...")

	completion := storage.NewMapPieceCompletion()
	clientStorage := storage.NewFileWithCompletion(dataDir, completion)

	cfg := torrent.NewDefaultClientConfig()
	cfg.Seed = true
	cfg.NoDHT = true
	cfg.DisableUTP = true
	cfg.NoUpload = false
	cfg.ListenPort = seedPort
	cfg.DefaultStorage = clientStorage

	client, err := torrent.NewClient(cfg)
	if err != nil {
		log.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	log.Printf("[4/8] DONE — Client listening on port %d", client.LocalPort())

	// Step 5: Add torrent from the saved bytes (simulate OmniCloud flow)
	log.Printf("\n[5/8] Adding torrent from .torrent file bytes...")

	// Read back from file (exactly as OmniCloud does after saving to DB)
	savedBytes, _ := ioutil.ReadFile(torrentPath)
	var mi3 metainfo.MetaInfo
	bencode.Unmarshal(savedBytes, &mi3)

	log.Printf("[5/8] Parsed announce: %s", mi3.Announce)
	log.Printf("[5/8] Parsed info_hash: %s", mi3.HashInfoBytes().HexString())

	// Use per-torrent storage (like OmniCloud's StartSeeding does)
	perTorrentStorage := storage.NewFileWithCompletion(dataDir, storage.NewMapPieceCompletion())

	t, _, err := client.AddTorrentSpec(&torrent.TorrentSpec{
		InfoHash:  mi3.HashInfoBytes(),
		InfoBytes: mi3.InfoBytes,
		Trackers:  [][]string{{mi3.Announce}},
		Storage:   perTorrentStorage,
	})
	if err != nil {
		log.Fatalf("AddTorrentSpec: %v", err)
	}

	<-t.GotInfo()
	log.Printf("[5/8] DONE — Torrent added: name=%s pieces=%d length=%d",
		t.Name(), t.NumPieces(), t.Length())

	// Step 6: Wait for piece verification
	log.Printf("\n[6/8] Waiting for piece verification...")

	deadline := time.After(30 * time.Second)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()

	verified := false
	for !verified {
		select {
		case <-deadline:
			log.Printf("[6/8] ✗ TIMEOUT waiting for verification")
			dumpPieceStates(t)
			os.Exit(1)
		case <-tick.C:
			bc := t.BytesCompleted()
			total := t.Length()
			pct := float64(bc) / float64(total) * 100

			complete, checking, incomplete := countPieceStates(t)
			log.Printf("[6/8] Progress: %.1f%% (%d/%d) | complete=%d checking=%d incomplete=%d",
				pct, bc, total, complete, checking, incomplete)

			if bc >= total && complete == t.NumPieces() {
				log.Printf("[6/8] DONE — All %d pieces verified!", complete)
				verified = true
			}
		}
	}

	// Step 7: Check seeding status
	log.Printf("\n[7/8] Checking seeding status...")
	if t.Seeding() {
		log.Printf("[7/8] DONE — SEEDING IS ACTIVE!")
	} else {
		log.Printf("[7/8] ✗ NOT SEEDING — dumping debug info:")
		log.Printf("  cfg.Seed=%v cfg.NoUpload=%v", cfg.Seed, cfg.NoUpload)
		log.Printf("  BytesCompleted=%d/%d", t.BytesCompleted(), t.Length())
		dumpPieceStates(t)
		os.Exit(1)
	}

	// Step 8: Ready for download test
	log.Printf("\n[8/8] Seeder is running. Test with:")
	log.Printf("  cd /home/appbox/DCPCLOUDAPP/omnicloud && go run tools/download-test/main.go %s", torrentPath)
	log.Printf("  Info hash: %s", infoHash.HexString())
	log.Printf("  Tracker: %s", trackerURL)
	log.Printf("  Seed port: %d", client.LocalPort())
	log.Printf("")
	log.Printf("  Press Ctrl+C to stop.")
	log.Printf("")

	// Monitor
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	monTick := time.NewTicker(5 * time.Second)
	defer monTick.Stop()

	for {
		select {
		case <-sigCh:
			log.Println("Shutting down.")
			return
		case <-monTick.C:
			st := t.Stats()
			conns := t.PeerConns()
			log.Printf("[MONITOR] seeding=%v | peers=%d conns=%d | uploaded=%d bytes",
				t.Seeding(), st.TotalPeers, len(conns), st.BytesWrittenData)
		}
	}
}

func countPieceStates(t *torrent.Torrent) (complete, checking, incomplete int) {
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
	return
}

func dumpPieceStates(t *torrent.Torrent) {
	n := t.NumPieces()
	if n > 20 {
		n = 20
	}
	for i := 0; i < n; i++ {
		ps := t.PieceState(i)
		log.Printf("  piece[%d] Complete=%v Ok=%v Checking=%v Partial=%v Priority=%v",
			i, ps.Complete, ps.Ok, ps.Checking, ps.Partial, ps.Priority)
	}
}

// Silence compiler complaints about unused imports that the build might reference transitively
var _ = strings.TrimSpace
