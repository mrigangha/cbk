package main

import (
	"database/sql"
	"strings"
)

func migrateV4(db *sql.DB) error {
	_, err := db.Exec(`
		ALTER TABLE agent_messages ADD COLUMN trace TEXT;
	`)
	if err != nil {
		// Column may already exist from a previous partial run.
		if strings.Contains(err.Error(), "duplicate column name") {
			return nil
		}
		return err
	}
	return nil
}
