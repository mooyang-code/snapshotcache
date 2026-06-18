package sqlitesource_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	sqlitesource "github.com/mooyang-code/snapshotcache/source/sqlite"
	_ "modernc.org/sqlite"
)

type sqliteItem struct {
	ID string `json:"id"`
}

func TestSQLiteSourceFetchesSingleJSONColumn(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "items.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE items (payload TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO items(payload) VALUES ('{"id":"apt"}'), ('{"id":"ar"}')`); err != nil {
		t.Fatalf("insert rows: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}

	source := sqlitesource.New[sqliteItem](sqlitesource.Options{
		Path:  dbPath,
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

func TestSQLiteSourceReturnsErrorForMissingDatabase(t *testing.T) {
	source := sqlitesource.New[sqliteItem](sqlitesource.Options{
		Path:  filepath.Join(t.TempDir(), "missing", "items.db"),
		Query: `SELECT payload FROM items`,
	})
	if _, err := source.Fetch(context.Background()); err == nil {
		t.Fatalf("Fetch returned nil error for missing sqlite database")
	}
}

func TestSQLiteSourceDoesNotCreateMissingDatabaseFile(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "items.db")
	source := sqlitesource.New[sqliteItem](sqlitesource.Options{
		Path:  dbPath,
		Query: `SELECT payload FROM items`,
	})
	if _, err := source.Fetch(context.Background()); err == nil {
		t.Fatalf("Fetch returned nil error for missing sqlite database file")
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("missing sqlite database file was created, stat error = %v", err)
	}
}
