package alert_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"OlxScraper/internal/alert"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotifier_Send_HappyPath(t *testing.T) {
	var (
		mu      sync.Mutex
		gotBody string
		gotReq  *http.Request
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotBody = string(body)
		// Clone headers we care about before the handler returns
		gotReq = r.Clone(r.Context())
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := alert.New(srv.URL)
	n.Send(42, "RTX 3080", 9, 450.0, "EUR", "https://olx.bg/ad/rtx-3080")

	// Send is fire-and-forget — wait briefly for the goroutine.
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	require.NotEmpty(t, gotBody, "webhook should have received a request")
	assert.Contains(t, gotBody, "9/10")
	assert.Contains(t, gotBody, "RTX 3080")
	assert.Contains(t, gotBody, "450.00 EUR")
	assert.Contains(t, gotBody, "https://olx.bg/ad/rtx-3080")

	// ntfy.sh headers
	assert.Contains(t, gotReq.Header.Get("Title"), "score 9")
	assert.Equal(t, "high", gotReq.Header.Get("Priority"))
	assert.Contains(t, gotReq.Header.Get("Tags"), "score-9")
}

func TestNotifier_Send_NoURL_NoRequest(t *testing.T) {
	// No-op notifier — should make zero HTTP calls.
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	n := alert.New("") // empty URL
	n.Send(1, "Some item", 9, 100.0, "EUR", "https://example.com")

	time.Sleep(100 * time.Millisecond)
	assert.False(t, called, "no-op notifier must not make HTTP calls")
}

func TestNotifier_Send_WebhookError_DoesNotPanic(t *testing.T) {
	// Webhook returns 500 — Send should log but not panic or return error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := alert.New(srv.URL)
	// Should complete without panic.
	assert.NotPanics(t, func() {
		n.Send(5, "item", 9, 50.0, "EUR", "https://example.com")
		time.Sleep(200 * time.Millisecond)
	})
}

func TestNotifier_Send_MissingCurrency(t *testing.T) {
	var mu sync.Mutex
	var gotBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotBody = string(body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := alert.New(srv.URL)
	n.Send(7, "GPU", 8, 300.0, "", "https://example.com")
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	// No currency — should not include a trailing space or empty currency.
	assert.True(t, strings.Contains(gotBody, "300.00"), "price should still appear")
	assert.False(t, strings.Contains(gotBody, "300.00 "), "no trailing space when currency empty")
}
