package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"github.com/mooyang-code/snapshotcache"
	sqlitesource "github.com/mooyang-code/snapshotcache/source/sqlite"
	_ "modernc.org/sqlite"
)

type verifyItem struct {
	ID     string `json:"id"`
	Market string `json:"market"`
	Score  int    `json:"score"`
}

func main() {
	ctx := context.Background()
	if err := run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "snapshotcache verify failed: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	root, err := os.MkdirTemp("", "snapshotcache-verify-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)

	if err := verifyHTTP(ctx, root); err != nil {
		return err
	}
	if err := verifySQLite(ctx, root); err != nil {
		return err
	}
	fmt.Println("snapshotcache verify: all checks passed")
	return nil
}

func verifyHTTP(ctx context.Context, root string) error {
	items := []verifyItem{
		{ID: "APT-USDT", Market: "binance_spot", Score: 10},
		{ID: "AR-USDT", Market: "binance_spot", Score: 20},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    0,
			"message": "ok",
			"data":    items,
		})
	}))
	filePath := filepath.Join(root, "http.snapshot.json")
	cache, err := snapshotcache.NewHTTPMap[verifyItem](
		server.URL,
		snapshotcache.UniqueKey(func(item verifyItem) []string { return []string{item.ID} }),
		snapshotcache.WithName("http_items"),
		snapshotcache.WithTimeout(3*time.Second),
		snapshotcache.WithFileCache(filePath, time.Hour),
		snapshotcache.WithRefreshDisabled(),
	)
	if err != nil {
		return err
	}
	if err := cache.Start(ctx); err != nil {
		return err
	}
	defer cache.Stop(ctx)

	byMarket, err := snapshotcache.NewHTTPMultiMap[verifyItem](
		server.URL,
		snapshotcache.Key(func(item verifyItem) []string { return []string{item.Market} }),
		snapshotcache.WithName("http_items_by_market"),
		snapshotcache.WithTimeout(3*time.Second),
		snapshotcache.WithRefreshDisabled(),
	)
	if err != nil {
		return err
	}
	if err := byMarket.Start(ctx); err != nil {
		return err
	}
	defer byMarket.Stop(ctx)
	server.Close()

	if item, ok := cache.Get("APT-USDT"); !ok || item.Score != 10 {
		return fmt.Errorf("http cache Get(APT-USDT) = %#v/%v", item, ok)
	}
	rows := byMarket.List("binance_spot")
	if len(rows) != 2 {
		return fmt.Errorf("http cache List(by_market) returned %d rows", len(rows))
	}
	fmt.Printf("HTTP map: loaded %d rows and wrote file fallback\n", len(cache.All()))

	fallback, err := snapshotcache.NewHTTPMap[verifyItem](
		server.URL,
		snapshotcache.UniqueKey(func(item verifyItem) []string { return []string{item.ID} }),
		snapshotcache.WithName("http_items"),
		snapshotcache.WithTimeout(time.Second),
		snapshotcache.WithFileCache(filePath, time.Hour),
		snapshotcache.WithRefreshDisabled(),
	)
	if err != nil {
		return err
	}
	if err := fallback.Start(ctx); err != nil {
		return err
	}
	defer fallback.Stop(ctx)
	if status := fallback.Status(); status.LoadedFrom != snapshotcache.LoadedFromFile {
		return fmt.Errorf("fallback LoadedFrom = %q, want %q", status.LoadedFrom, snapshotcache.LoadedFromFile)
	}
	fmt.Printf("HTTP fallback: restored %d rows from file\n", len(fallback.All()))
	return nil
}

func verifySQLite(ctx context.Context, root string) error {
	dbPath := filepath.Join(root, "items.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE items (payload TEXT NOT NULL)`); err != nil {
		return err
	}
	if _, err := db.Exec(`INSERT INTO items(payload) VALUES ('{"id":"APT-USDT","market":"binance_spot","score":10}')`); err != nil {
		return err
	}

	cache, err := snapshotcache.New[verifyItem](snapshotcache.Options[verifyItem]{
		Name: "sqlite_items",
		Source: sqlitesource.New[verifyItem](sqlitesource.Options{
			Path:  dbPath,
			Query: `SELECT payload FROM items ORDER BY payload`,
		}),
		Indexes: []snapshotcache.Index[verifyItem]{
			{Name: "by_id", Unique: true, Key: func(item verifyItem) []string { return []string{item.ID} }},
		},
	})
	if err != nil {
		return err
	}
	if err := cache.Start(ctx); err != nil {
		return err
	}
	defer cache.Stop(ctx)
	if item, ok := cache.Get("by_id", "APT-USDT"); !ok || item.Score != 10 {
		return fmt.Errorf("sqlite cache Get(APT-USDT) = %#v/%v", item, ok)
	}
	if _, err := db.Exec(`INSERT INTO items(payload) VALUES ('{"id":"AR-USDT","market":"binance_spot","score":20}')`); err != nil {
		return err
	}
	if err := cache.Refresh(ctx); err != nil {
		return err
	}
	if item, ok := cache.Get("by_id", "AR-USDT"); !ok || item.Score != 20 {
		return fmt.Errorf("sqlite cache Get(AR-USDT) after refresh = %#v/%v", item, ok)
	}
	fmt.Printf("SQLite source: loaded %d rows after manual refresh\n", len(cache.Snapshot().Items))
	return nil
}
