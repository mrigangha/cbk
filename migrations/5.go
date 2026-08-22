package main

import (
	"database/sql"
	"strings"
)

func migrateV5(db *sql.DB) error {
	_, err := db.Exec(`
	PRAGMA foreign_keys = ON;

	CREATE TABLE IF NOT EXISTS marketing_goals (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,

		name TEXT NOT NULL,
		description TEXT,

		objective TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'ACTIVE'
			CHECK (status IN ('ACTIVE', 'PAUSED', 'COMPLETED', 'ARCHIVED')),

		target_cpl REAL,
		target_cpa REAL,
		target_roas REAL,
		target_conversions INTEGER,

		budget REAL,
		daily_budget REAL,

		start_date DATETIME,
		end_date DATETIME,

		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

		FOREIGN KEY (user_id)
			REFERENCES users(id)
			ON DELETE CASCADE
			ON UPDATE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_marketing_goals_user_id
	ON marketing_goals(user_id);
	`)
	if err != nil {
		return err
	}

	// A session can optionally work toward a goal. A goal can have
	// many sessions; deleting a goal detaches its sessions.
	_, err = db.Exec(`
		ALTER TABLE agent_sessions ADD COLUMN goal_id INTEGER
			REFERENCES marketing_goals(id) ON DELETE SET NULL;
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
