package main

import (
	"database/sql"
	"log"

	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "app.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`
	PRAGMA foreign_keys = ON;

	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL UNIQUE,
		email TEXT NOT NULL UNIQUE,
		hashed_password TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS providers (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		provider_name TEXT NOT NULL,
		api_key TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

		FOREIGN KEY (user_id)
			REFERENCES users(id)
			ON DELETE CASCADE
			ON UPDATE CASCADE
	);

	CREATE TABLE IF NOT EXISTS meta_ads_accounts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,

		ad_account_id TEXT NOT NULL,
		business_id TEXT,
		account_name TEXT,

		access_token TEXT NOT NULL,
		token_expires_at DATETIME,

		currency TEXT,
		timezone_name TEXT,

		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

		FOREIGN KEY (user_id)
			REFERENCES users(id)
			ON DELETE CASCADE
			ON UPDATE CASCADE,

		UNIQUE(user_id, ad_account_id)
	);

	CREATE INDEX IF NOT EXISTS idx_providers_user_id
	ON providers(user_id);

	CREATE INDEX IF NOT EXISTS idx_meta_ads_accounts_user_id
	ON meta_ads_accounts(user_id);

	CREATE INDEX IF NOT EXISTS idx_meta_ads_accounts_ad_account_id
	ON meta_ads_accounts(ad_account_id);
	`)
	if err != nil {
		log.Fatal(err)
	}

	log.Println("Migration completed successfully.")
}
