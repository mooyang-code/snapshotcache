package httpsource

import (
	"net/http"
	"testing"
)

type internalHTTPItem struct {
	ID string `json:"id"`
}

func TestNewUsesBoundedDefaultHTTPClient(t *testing.T) {
	source := New[internalHTTPItem](Options{URL: "https://example.com/items"})

	if source.opts.Client == nil {
		t.Fatalf("Client is nil, want bounded default client")
	}
	if source.opts.Client == http.DefaultClient {
		t.Fatalf("Client uses http.DefaultClient, want isolated bounded default client")
	}
	if source.opts.Client.Timeout <= 0 {
		t.Fatalf("Client timeout = %s, want positive default timeout", source.opts.Client.Timeout)
	}
}
