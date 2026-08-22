package main

import (
	"database/sql"
	"strings"
)

func migrateV8(db *sql.DB) error {
	_, err := db.Exec(`
		ALTER TABLE optimization_actions ADD COLUMN result TEXT;
	`)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate column name") {
			return nil
		}
		return err
	}
	return nil
}
