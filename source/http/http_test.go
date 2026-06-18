package httpsource_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	httpsource "github.com/mooyang-code/snapshotcache/source/http"
)

type httpItem struct {
	ID string `json:"id"`
}

func TestHTTPSourceFetchesWrappedJSONData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"ok","data":[{"id":"apt"},{"id":"ar"}]}`))
	}))
	defer server.Close()

	source := httpsource.New[httpItem](httpsource.Options{URL: server.URL})
	items, err := source.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if len(items) != 2 || items[0].ID != "apt" || items[1].ID != "ar" {
		t.Fatalf("Fetch items = %#v, want apt/ar", items)
	}
}

func TestHTTPSourceRejectsDirectArrayResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"apt"}]`))
	}))
	defer server.Close()

	source := httpsource.New[httpItem](httpsource.Options{URL: server.URL})
	if _, err := source.Fetch(context.Background()); err == nil {
		t.Fatalf("Fetch returned nil error for direct array response")
	}
}

func TestHTTPSourceRejectsBusinessErrorCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":10001,"message":"bad request","data":[]}`))
	}))
	defer server.Close()

	source := httpsource.New[httpItem](httpsource.Options{URL: server.URL})
	if _, err := source.Fetch(context.Background()); err == nil {
		t.Fatalf("Fetch returned nil error for non-zero business code")
	}
}

func TestHTTPSourceReturnsErrorForNonSuccessStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "broken", http.StatusBadGateway)
	}))
	defer server.Close()

	source := httpsource.New[httpItem](httpsource.Options{URL: server.URL})
	if _, err := source.Fetch(context.Background()); err == nil {
		t.Fatalf("Fetch returned nil error for non-2xx response")
	}
}
