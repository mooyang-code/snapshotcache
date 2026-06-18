package sqlsource_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	sqlsource "github.com/mooyang-code/snapshotcache/source/sql"
	_ "modernc.org/sqlite"
)

type sqlItem struct {
	ID string `json:"id"`
}

type sqlInstrument struct {
	ID     string `json:"id"`
	Market string `json:"market"`
	Score  int    `json:"score"`
}

func TestSQLSourceFetchesSingleJSONColumn(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "items.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE items (payload TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO items(payload) VALUES ('{"id":"apt"}'), ('{"id":"ar"}')`); err != nil {
		t.Fatalf("insert rows: %v", err)
	}

	source := sqlsource.New[sqlItem](sqlsource.Options{
		DB:    db,
		Query: `SELECT payload FROM items ORDER BY payload`,
	})
	items, err := source.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if len(items) != 2 || items[0].ID != "apt" || items[1].ID != "ar" {
		t.Fatalf("Fetch items = %#v, want apt/ar", items)
	}
}

func TestSQLSourceFetchesMultipleColumns(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "items.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE items (id TEXT NOT NULL, market TEXT NOT NULL, score INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO items(id, market, score) VALUES ('apt', 'binance_spot', 10)`); err != nil {
		t.Fatalf("insert rows: %v", err)
	}

	source := sqlsource.New[sqlInstrument](sqlsource.Options{
		DB:    db,
		Query: `SELECT id, market, score FROM items`,
	})
	items, err := source.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if len(items) != 1 || items[0].ID != "apt" || items[0].Market != "binance_spot" || items[0].Score != 10 {
		t.Fatalf("Fetch items = %#v, want mapped multi-column item", items)
	}
}
