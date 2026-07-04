package main

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "test.db")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	// Create table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY,
			name TEXT
		)
	`)
	if err != nil {
		panic(err)
	}

	// Parameterized INSERT
	_, err = db.Exec(
		"INSERT INTO users (name) VALUES (?)",
		"John",
	)
	if err != nil {
		panic(err)
	}

	// Parameterized SELECT
	var (
		id   = 1
		name string
	)

	err = db.QueryRow(
		"SELECT name FROM users WHERE id = ?",
		id,
	).Scan(&name)
	if err != nil {
		if err == sql.ErrNoRows {
			fmt.Println("User not found")
			return
		}
		panic(err)
	}

	fmt.Println(name)
}
