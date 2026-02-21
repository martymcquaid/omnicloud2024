package torrent

import (
	"database/sql"
	"fmt"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// PostgresPieceCompletion implements storage.PieceCompletion using PostgreSQL
// This replaces BoltDB to avoid file locking issues and provides centralized storage
type PostgresPieceCompletion struct {
	db       *sql.DB
	infoHash string
}

// NewPostgresPieceCompletion creates a PieceCompletion backed by PostgreSQL
func NewPostgresPieceCompletion(db *sql.DB, infoHash metainfo.Hash) storage.PieceCompletion {
	return &PostgresPieceCompletion{
		db:       db,
		infoHash: infoHash.HexString(),
	}
}

// Get returns whether a piece is complete
func (pc *PostgresPieceCompletion) Get(pk metainfo.PieceKey) (storage.Completion, error) {
	var completed bool
	query := `SELECT completed FROM torrent_piece_completion WHERE info_hash = $1 AND piece_index = $2`
	err := pc.db.QueryRow(query, pc.infoHash, pk.Index).Scan(&completed)

	if err == sql.ErrNoRows {
		// Not in database = unknown state. Return Ok: false so the torrent
		// library will queue a hash verification from disk, then call Set()
		// with the result. Returning Ok: true would make the library trust
		// "not complete" and never verify the piece.
		return storage.Completion{Complete: false, Ok: false}, nil
	}
	if err != nil {
		return storage.Completion{Ok: false}, fmt.Errorf("failed to query piece completion: %w", err)
	}

	return storage.Completion{Complete: completed, Ok: true}, nil
}

// Set marks a piece as complete or incomplete
func (pc *PostgresPieceCompletion) Set(pk metainfo.PieceKey, completed bool) error {
	query := `
		INSERT INTO torrent_piece_completion (info_hash, piece_index, completed, verified_at)
		VALUES ($1, $2, $3, CURRENT_TIMESTAMP)
		ON CONFLICT (info_hash, piece_index)
		DO UPDATE SET completed = $3, verified_at = CURRENT_TIMESTAMP
	`
	_, err := pc.db.Exec(query, pc.infoHash, pk.Index, completed)
	if err != nil {
		return fmt.Errorf("failed to set piece completion: %w", err)
	}
	return nil
}

// Close closes the completion tracker (no-op for PostgreSQL)
func (pc *PostgresPieceCompletion) Close() error {
	// No cleanup needed - database connection is managed externally
	return nil
}
