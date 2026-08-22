package main

import (
	"database/sql"
	"strings"
)

func migrateV9(db *sql.DB) error {

	// Each ALTER is idempotent on its own; modernc wraps the
	// "duplicate column name" error text.
	for _, stmt := range []string{
		`ALTER TABLE optimization_actions ADD COLUMN idempotency_key TEXT`,
		`ALTER TABLE optimization_actions ADD COLUMN before_metrics TEXT`,
		`ALTER TABLE optimization_actions ADD COLUMN outcome_status TEXT`,
		`ALTER TABLE optimization_actions ADD COLUMN goal_id INTEGER`,
		`ALTER TABLE optimization_actions ADD COLUMN signals TEXT`,
	} {
		if _, err := db.Exec(stmt); err != nil &&
			!strings.Contains(err.Error(), "duplicate column name") {
			return err
		}
	}

	_, err := db.Exec(`
	PRAGMA foreign_keys = ON;

	CREATE TABLE IF NOT EXISTS optimization_outcomes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		action_id INTEGER NOT NULL,
		user_id INTEGER NOT NULL,

		window_days INTEGER NOT NULL,
		metrics TEXT,          -- normalized metrics JSON at capture time
		evaluation TEXT,       -- evaluator verdict JSON

		captured_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

		FOREIGN KEY (action_id)
			REFERENCES optimization_actions(id)
			ON DELETE CASCADE
	);

	CREATE UNIQUE INDEX IF NOT EXISTS idx_outcomes_action_window
	ON optimization_outcomes(action_id, window_days);

	CREATE INDEX IF NOT EXISTS idx_optimization_actions_idem
	ON optimization_actions(user_id, idempotency_key);

	-- At most ONE executed row per replay key. This is the last-resort
	-- backstop behind the in-process execution lock: even if two executors
	-- race past the pre-checks, only the first write can commit.
	CREATE UNIQUE INDEX IF NOT EXISTS idx_actions_executed_idem
	ON optimization_actions(user_id, idempotency_key)
	WHERE status = 'EXECUTED' AND COALESCE(idempotency_key, '') <> '';
	`)

	return err
}
