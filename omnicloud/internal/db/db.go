package db

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/lib/pq"
)

// DB wraps the database connection
type DB struct {
	*sql.DB
}

// Connect establishes a connection to PostgreSQL
func Connect(connStr string) (*DB, error) {
	sqlDB, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test the connection
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Set connection pool settings
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(5)

	log.Println("Successfully connected to database")
	return &DB{sqlDB}, nil
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.DB.Close()
}
