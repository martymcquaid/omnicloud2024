package torrent

import (
	"bytes"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// RewriteTorrentWithRawInfo rewrites torrent bytes so the "info" dict is stored as raw bytes (preserves hash).
func RewriteTorrentWithRawInfo(torrentFile []byte) ([]byte, error) {
	rawInfo, err := extractRawInfoBytes(torrentFile)
	if err != nil {
		return nil, err
	}
	var mi metainfo.MetaInfo
	if err := bencode.Unmarshal(torrentFile, &mi); err != nil {
		return nil, err
	}
	mi.InfoBytes = rawInfo
	return bencode.Marshal(&mi)
}

// RewriteTorrentAnnounceWithRawInfo rewrites torrent bytes with a new announce URL and raw info (preserves hash).
func RewriteTorrentAnnounceWithRawInfo(torrentFile []byte, announceURL string) ([]byte, error) {
	rawInfo, err := extractRawInfoBytes(torrentFile)
	if err != nil {
		return nil, err
	}
	var mi metainfo.MetaInfo
	if err := bencode.Unmarshal(torrentFile, &mi); err != nil {
		return nil, err
	}
	mi.InfoBytes = rawInfo
	mi.Announce = announceURL
	return bencode.Marshal(&mi)
}

// RunTorrentFileMigration rewrites each existing dcp_torrents.torrent_file to use
// "info" as raw string (same format as marshalTorrentWithRawInfo) so EnsureSeedingRestore
// and the client get correct info bytes. Idempotent: skips rows that fail to parse or
// are already in the new format. Skips rows where extracted info hash != stored info_hash.
func RunTorrentFileMigration(db *sql.DB) {
	rows, err := db.Query(`SELECT id, info_hash, torrent_file FROM dcp_torrents WHERE torrent_file IS NOT NULL AND length(torrent_file) > 0`)
	if err != nil {
		log.Printf("Torrent file migration: query failed: %v", err)
		return
	}
	defer rows.Close()

	var id, infoHash string
	var torrentFile []byte
	updated := 0
	skipped := 0
	failed := 0

	for rows.Next() {
		if err := rows.Scan(&id, &infoHash, &torrentFile); err != nil {
			log.Printf("Torrent file migration: scan row: %v", err)
			failed++
			continue
		}
		rawInfo, err := extractRawInfoBytes(torrentFile)
		if err != nil {
			log.Printf("Torrent file migration: extract raw info id=%s: %v", id, err)
			failed++
			continue
		}
		sum := sha1.Sum(rawInfo)
		computedHash := hex.EncodeToString(sum[:])
		if computedHash != infoHash {
			log.Printf("Torrent file migration: info hash mismatch id=%s (computed %s vs stored %s), updating anyway so format is correct", id, computedHash, infoHash)
		}
		newBytes, err := RewriteTorrentWithRawInfo(torrentFile)
		if err != nil {
			log.Printf("Torrent file migration: rewrite id=%s: %v", id, err)
			failed++
			continue
		}
		if bytes.Equal(newBytes, torrentFile) {
			skipped++
		}
		_, err = db.Exec(`UPDATE dcp_torrents SET torrent_file = $1 WHERE id = $2`, newBytes, id)
		if err != nil {
			log.Printf("Torrent file migration: update id=%s: %v", id, err)
			failed++
			continue
		}
		updated++
	}

	if err := rows.Err(); err != nil {
		log.Printf("Torrent file migration: iterating rows: %v", err)
	}
	if updated > 0 || skipped > 0 || failed > 0 {
		log.Printf("Torrent file migration: updated=%d skipped=%d failed=%d", updated, skipped, failed)
	}
}

// RunAnnounceURLMigration rewrites stored dcp_torrents.torrent_file announce URLs to the configured value.
// This keeps existing torrents aligned when tracker port/host changes.
func RunAnnounceURLMigration(db *sql.DB, announceURL string) {
	if announceURL == "" {
		return
	}

	rows, err := db.Query(`SELECT id, torrent_file FROM dcp_torrents WHERE torrent_file IS NOT NULL AND length(torrent_file) > 0`)
	if err != nil {
		log.Printf("Announce URL migration: query failed: %v", err)
		return
	}
	defer rows.Close()

	updated := 0
	skipped := 0
	failed := 0

	for rows.Next() {
		var id string
		var torrentFile []byte
		if err := rows.Scan(&id, &torrentFile); err != nil {
			log.Printf("Announce URL migration: scan row: %v", err)
			failed++
			continue
		}

		var mi metainfo.MetaInfo
		if err := bencode.Unmarshal(torrentFile, &mi); err != nil {
			log.Printf("Announce URL migration: unmarshal id=%s: %v", id, err)
			failed++
			continue
		}
		if mi.Announce == announceURL {
			skipped++
			continue
		}

		newBytes, err := RewriteTorrentAnnounceWithRawInfo(torrentFile, announceURL)
		if err != nil {
			log.Printf("Announce URL migration: rewrite id=%s: %v", id, err)
			failed++
			continue
		}
		if bytes.Equal(newBytes, torrentFile) {
			skipped++
			continue
		}
		if _, err := db.Exec(`UPDATE dcp_torrents SET torrent_file = $1 WHERE id = $2`, newBytes, id); err != nil {
			log.Printf("Announce URL migration: update id=%s: %v", id, err)
			failed++
			continue
		}
		updated++
	}

	if err := rows.Err(); err != nil {
		log.Printf("Announce URL migration: iterating rows: %v", err)
	}
	if updated > 0 || skipped > 0 || failed > 0 {
		log.Printf("Announce URL migration: updated=%d skipped=%d failed=%d target=%s", updated, skipped, failed, announceURL)
	}
}

// extractRawInfoBytes extracts the raw bencode bytes for the "info" dictionary from a torrent file
func extractRawInfoBytes(torrentFile []byte) ([]byte, error) {
	// Simply return the InfoBytes if already parsed
	var mi metainfo.MetaInfo
	if err := bencode.Unmarshal(torrentFile, &mi); err != nil {
		return nil, err
	}
	if len(mi.InfoBytes) > 0 {
		return mi.InfoBytes, nil
	}
	// No InfoBytes set, return error
	return nil, fmt.Errorf("InfoBytes not set in metainfo")
}
