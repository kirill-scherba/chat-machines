// Copyright 2026 Kirill Scherba. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Optional SQLite persistence for message logging.
package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// SQLiteStore persists messages to SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore opens (and creates if needed) the SQLite database.
func NewSQLiteStore(path string) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

// migrate creates the messages table.
func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			room_id TEXT NOT NULL,
			round INTEGER NOT NULL,
			from_name TEXT NOT NULL,
			text TEXT NOT NULL,
			created_at DATETIME NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_messages_room_round ON messages(room_id, round);
	`)
	return err
}

// LogMessage writes a single message to SQLite.
func (s *SQLiteStore) LogMessage(roomID string, round int, from string, text string, createdAt time.Time) error {
	_, err := s.db.Exec(
		"INSERT INTO messages (room_id, round, from_name, text, created_at) VALUES (?, ?, ?, ?, ?)",
		roomID, round, from, text, createdAt,
	)
	return err
}

// Close closes the database.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// os.Dir is a helper returning the directory of a path (like filepath.Dir).
