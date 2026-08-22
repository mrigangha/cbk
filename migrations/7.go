package main

import "database/sql"

func migrateV7(db *sql.DB) error {
	_, err := db.Exec(`
	PRAGMA foreign_keys = ON;

	CREATE TABLE IF NOT EXISTS optimization_actions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,

		object_type TEXT NOT NULL
			CHECK (object_type IN ('CAMPAIGN', 'ADSET', 'AD')),
		object_id TEXT NOT NULL,
		object_name TEXT,

		action TEXT NOT NULL,
		reason TEXT NOT NULL,
		rule_id TEXT,
		confidence REAL,
		suggested_change TEXT, -- JSON

		status TEXT NOT NULL DEFAULT 'PENDING'
			CHECK (status IN ('PENDING','APPROVED','REJECTED','EXECUTED','FAILED')),
		date_preset TEXT,

		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		decided_at DATETIME,
		executed_at DATETIME,
		error TEXT,

		FOREIGN KEY (user_id)
			REFERENCES users(id)
			ON DELETE CASCADE
			ON UPDATE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_optimization_actions_user_status
	ON optimization_actions(user_id, status);
	`)
	if err != nil {
		return err
	}
	return nil
}
