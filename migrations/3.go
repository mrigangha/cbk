package main

import "database/sql"

func migrateV3(db *sql.DB) error {
	_, err := db.Exec(`
	PRAGMA foreign_keys = ON;

	CREATE TABLE IF NOT EXISTS agent_sessions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,

		title TEXT NOT NULL DEFAULT 'New Chat',

		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

		FOREIGN KEY (user_id)
			REFERENCES users(id)
			ON DELETE CASCADE
			ON UPDATE CASCADE
	);

	CREATE TABLE IF NOT EXISTS agent_messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id INTEGER NOT NULL,

		role TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
		content TEXT NOT NULL,
		steps TEXT,
		model_name TEXT,

		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

		FOREIGN KEY (session_id)
			REFERENCES agent_sessions(id)
			ON DELETE CASCADE
			ON UPDATE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_agent_sessions_user_id
	ON agent_sessions(user_id);

	CREATE INDEX IF NOT EXISTS idx_agent_messages_session_id
	ON agent_messages(session_id);
	`)
	return err
}
