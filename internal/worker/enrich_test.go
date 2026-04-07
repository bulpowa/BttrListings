package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"OlxScraper/internal/llm"
	"OlxScraper/internal/model"
	"OlxScraper/internal/repository"
	"OlxScraper/internal/worker"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockListingRepo is a test double for ListingRepository.
// It panics on any method not explicitly overridden (embed nil interface).
type mockListingRepo struct {
	repository.ListingRepository // satisfies interface; nil — panics if non-overridden methods are called

	fnGetByID          func(ctx context.Context, id int64) (*model.ListingRow, error)
	fnUpdateEnrichment func(ctx context.Context, id int64, result *llm.ExtractionResult) error
}

func (m *mockListingRepo) GetByID(ctx context.Context, id int64) (*model.ListingRow, error) {
	return m.fnGetByID(ctx, id)
}

func (m *mockListingRepo) UpdateEnrichment(ctx context.Context, id int64, result *llm.ExtractionResult) error {
	return m.fnUpdateEnrichment(ctx, id, result)
}

// ollamaResponse builds a minimal OpenAI-compatible chat completion response.
func ollamaResponse(content string) []byte {
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type choice struct {
		Message message `json:"message"`
	}
	type resp struct {
		Choices []choice `json:"choices"`
	}
	b, _ := json.Marshal(resp{
		Choices: []choice{{Message: message{Role: "assistant", Content: content}}},
	})
	return b
}

// newTestWorker returns a worker backed by a mock repo and a test LLM server.
// The handler fn controls what the fake LLM returns.
func newTestWorker(
	mock *mockListingRepo,
	llmHandler http.HandlerFunc,
) (*worker.EnrichListingWorker, *httptest.Server) {
	srv := httptest.NewServer(llmHandler)
	client := llm.NewOllamaClient(srv.URL, "test-model")
	repo := &repository.Repository{Listing: mock}
	return worker.NewEnrichListingWorker(repo, client), srv
}

func newJob(listingID int64) *river.Job[worker.EnrichListingArgs] {
	return &river.Job[worker.EnrichListingArgs]{
		Args: worker.EnrichListingArgs{ListingID: listingID},
	}
}

// validExtraction is the JSON the LLM returns for a normal listing.
const validExtraction = `{
  "title_normalized": "NVIDIA RTX 3080",
  "price_amount": 450.0,
  "price_currency": "EUR",
  "condition": "good",
  "category": "GPU",
  "location_city": "Sofia",
  "specs": {"vram": "10GB"},
  "deal_score": 8,
  "deal_reasoning": "Below market price, good condition",
  "is_suspicious": false,
  "suspicious_reason": null
}`

func TestEnrichWorker_HappyPath(t *testing.T) {
	raw := "RTX 3080 10GB"
	listing := &model.ListingRow{
		ID:      42,
		Title:   "RTX 3080",
		RawHTML: &raw,
	}

	var savedID int64
	var savedResult *llm.ExtractionResult

	mock := &mockListingRepo{
		fnGetByID: func(_ context.Context, id int64) (*model.ListingRow, error) {
			return listing, nil
		},
		fnUpdateEnrichment: func(_ context.Context, id int64, result *llm.ExtractionResult) error {
			savedID = id
			savedResult = result
			return nil
		},
	}

	w, srv := newTestWorker(mock, func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		rw.Write(ollamaResponse(validExtraction))
	})
	defer srv.Close()

	err := w.Work(context.Background(), newJob(42))
	require.NoError(t, err)

	assert.Equal(t, int64(42), savedID)
	require.NotNil(t, savedResult)
	assert.Equal(t, "NVIDIA RTX 3080", savedResult.TitleNormalized)
	assert.Equal(t, 450.0, savedResult.PriceAmount)
	assert.Equal(t, 8, savedResult.DealScore)
	assert.False(t, savedResult.IsSuspicious)
}

func TestEnrichWorker_ListingNotFound(t *testing.T) {
	mock := &mockListingRepo{
		fnGetByID: func(_ context.Context, id int64) (*model.ListingRow, error) {
			return nil, nil // listing not found
		},
	}

	// LLM handler should never be called — panics if it is.
	w, srv := newTestWorker(mock, func(rw http.ResponseWriter, r *http.Request) {
		t.Fatal("LLM was called for a non-existent listing")
	})
	defer srv.Close()

	err := w.Work(context.Background(), newJob(99))
	assert.NoError(t, err, "missing listing should be a no-op, not an error")
}

func TestEnrichWorker_AlreadyEnriched(t *testing.T) {
	now := time.Now()
	listing := &model.ListingRow{
		ID:         1,
		Title:      "Already enriched item",
		EnrichedAt: &now,
	}

	mock := &mockListingRepo{
		fnGetByID: func(_ context.Context, id int64) (*model.ListingRow, error) {
			return listing, nil
		},
	}

	w, srv := newTestWorker(mock, func(rw http.ResponseWriter, r *http.Request) {
		t.Fatal("LLM was called for an already-enriched listing")
	})
	defer srv.Close()

	err := w.Work(context.Background(), newJob(1))
	assert.NoError(t, err, "already-enriched listing should be a no-op")
}

func TestEnrichWorker_LLMError_Retried(t *testing.T) {
	raw := "Some listing text"
	listing := &model.ListingRow{ID: 5, Title: "Test item", RawHTML: &raw}

	mock := &mockListingRepo{
		fnGetByID: func(_ context.Context, id int64) (*model.ListingRow, error) {
			return listing, nil
		},
		fnUpdateEnrichment: func(_ context.Context, id int64, result *llm.ExtractionResult) error {
			t.Fatal("UpdateEnrichment should not be called when LLM fails")
			return nil
		},
	}

	// LLM always returns HTTP 500 → Extract returns an error → River will retry.
	w, srv := newTestWorker(mock, func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusInternalServerError)
	})
	defer srv.Close()

	err := w.Work(context.Background(), newJob(5))
	assert.Error(t, err, "LLM failure should propagate so River retries the job")
}

func TestEnrichWorker_LLMInvalidJSON_Retried(t *testing.T) {
	raw := "listing"
	listing := &model.ListingRow{ID: 7, Title: "Bad JSON item", RawHTML: &raw}

	callCount := 0
	mock := &mockListingRepo{
		fnGetByID: func(_ context.Context, id int64) (*model.ListingRow, error) {
			return listing, nil
		},
		fnUpdateEnrichment: func(_ context.Context, id int64, result *llm.ExtractionResult) error {
			t.Fatal("UpdateEnrichment should not be called when LLM returns invalid JSON")
			return nil
		},
	}

	// Always return invalid JSON — the client retries internally twice, then returns error.
	w, srv := newTestWorker(mock, func(rw http.ResponseWriter, r *http.Request) {
		callCount++
		rw.Header().Set("Content-Type", "application/json")
		rw.Write(ollamaResponse("not valid json at all {{{"))
	})
	defer srv.Close()

	err := w.Work(context.Background(), newJob(7))
	assert.Error(t, err, "invalid JSON should propagate so River retries the job")
	assert.Equal(t, 2, callCount, "OllamaClient should retry once with temperature=0.1 before giving up")
}

func TestEnrichWorker_GetByIDError(t *testing.T) {
	mock := &mockListingRepo{
		fnGetByID: func(_ context.Context, id int64) (*model.ListingRow, error) {
			return nil, errors.New("db connection lost")
		},
	}

	w, srv := newTestWorker(mock, func(rw http.ResponseWriter, r *http.Request) {
		t.Fatal("LLM should not be called when GetByID errors")
	})
	defer srv.Close()

	err := w.Work(context.Background(), newJob(3))
	assert.Error(t, err)
}

func TestEnrichWorker_FallbackToTitleWhenNoRawHTML(t *testing.T) {
	desc := "10GB VRAM, used 6 months"
	price := "450 EUR"
	listing := &model.ListingRow{
		ID:          10,
		Title:       "RTX 3080",
		Description: &desc,
		RawPrice:    &price,
		RawHTML:     nil, // no raw HTML — should fall back to title+description+price
	}

	var capturedBody []byte
	mock := &mockListingRepo{
		fnGetByID: func(_ context.Context, id int64) (*model.ListingRow, error) {
			return listing, nil
		},
		fnUpdateEnrichment: func(_ context.Context, id int64, result *llm.ExtractionResult) error {
			return nil
		},
	}

	w, srv := newTestWorker(mock, func(rw http.ResponseWriter, r *http.Request) {
		capturedBody = make([]byte, r.ContentLength)
		r.Body.Read(capturedBody)
		rw.Header().Set("Content-Type", "application/json")
		rw.Write(ollamaResponse(validExtraction))
	})
	defer srv.Close()

	err := w.Work(context.Background(), newJob(10))
	require.NoError(t, err)

	// The request body sent to the LLM should contain the title, description, and price.
	body := string(capturedBody)
	assert.Contains(t, body, "RTX 3080")
	assert.Contains(t, body, "10GB VRAM")
	assert.Contains(t, body, "450 EUR")
}
