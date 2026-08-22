package main

import (
	"database/sql"
	"strings"
)

func migrateV6(db *sql.DB) error {
	_, err := db.Exec(`
		ALTER TABLE providers ADD COLUMN provider_type TEXT NOT NULL DEFAULT 'GEMINI';
	`)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate column name") {
			return nil
		}
		return err
	}
	return nil
}
