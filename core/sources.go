package core

import (
	"database/sql"
	"os"
)

// ReadSeedSources loads source URL -> source ID from trusted, maintainer-owned
// SQL in an isolated database. Duplicate URLs use the last source ID in lexical
// order. Never pass untrusted SQL to this function.
func ReadSeedSources(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(string(data)); err != nil {
		return nil, err
	}
	rows, err := db.Query("SELECT source_id,url FROM sources ORDER BY source_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sources := map[string]string{}
	for rows.Next() {
		var id, url string
		if err = rows.Scan(&id, &url); err != nil {
			return nil, err
		}
		sources[url] = id
	}
	return sources, rows.Err()
}
