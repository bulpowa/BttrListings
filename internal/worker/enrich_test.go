package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"OlxScraper/internal/alert"
	"OlxScraper/internal/llm"
	"OlxScraper/internal/model"
	"OlxScraper/internal/repository"
	"OlxScraper/internal/worker"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockListingRepo is a test double for ListingRepository.
type mockListingRepo struct {
	repository.ListingRepository // nil embed — panics on non-overridden methods

	fnGetByID          func(ctx context.Context, id int64) (*model.ListingRow, error)
	fnUpdateEnrichment func(ctx context.Context, id int64, result *llm.ExtractionResult, marketScore *float64) error
}

func (m *mockListingRepo) GetByID(ctx context.Context, id int64) (*model.ListingRow, error) {
	return m.fnGetByID(ctx, id)
}

func (m *mockListingRepo) UpdateEnrichment(ctx context.Context, id int64, result *llm.ExtractionResult, marketScore *float64) error {
	return m.fnUpdateEnrichment(ctx, id, result, marketScore)
}

// mockComponentPriceRepo is a test double for ComponentPriceRepository.
type mockComponentPriceRepo struct {
	repository.ComponentPriceRepository // nil embed

	fnGetByNormalizedName func(ctx context.Context, name string) (*model.ComponentPrice, error)
}

func (m *mockComponentPriceRepo) GetByNormalizedName(ctx context.Context, name string) (*model.ComponentPrice, error) {
	return m.fnGetByNormalizedName(ctx, name)
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

// newTestWorker returns a worker backed by mock repos and a test LLM server.
func newTestWorker(
	listingMock *mockListingRepo,
	llmHandler http.HandlerFunc,
) (*worker.EnrichListingWorker, *httptest.Server) {
	return newTestWorkerWithComponentRepo(listingMock, nil, llmHandler)
}

func newTestWorkerWithComponentRepo(
	listingMock *mockListingRepo,
	componentMock *mockComponentPriceRepo,
	llmHandler http.HandlerFunc,
) (*worker.EnrichListingWorker, *httptest.Server) {
	srv := httptest.NewServer(llmHandler)
	client := llm.NewOllamaClient(srv.URL, "test-model")
	repo := &repository.Repository{Listing: listingMock, ComponentPrice: componentMock}
	return worker.NewEnrichListingWorker(repo, client, alert.New("")), srv
}

func newJob(listingID int64) *river.Job[worker.EnrichListingArgs] {
	return &river.Job[worker.EnrichListingArgs]{
		Args: worker.EnrichListingArgs{ListingID: listingID},
	}
}

// validExtraction is the JSON the LLM returns for a normal listing (no components).
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
  "suspicious_reason": null,
  "components": []
}`

// extractionWithComponents returns LLM JSON with a components list.
func extractionWithComponents(components []string, price float64) string {
	compsJSON, _ := json.Marshal(components)
	return `{
  "title_normalized": "Gaming PC",
  "price_amount": ` + jsonFloat(price) + `,
  "price_currency": "BGN",
  "condition": "good",
  "category": "PC",
  "location_city": "Sofia",
  "specs": {},
  "deal_score": 6,
  "deal_reasoning": "Fair price",
  "is_suspicious": false,
  "suspicious_reason": null,
  "components": ` + string(compsJSON) + `
}`
}

func jsonFloat(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

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
		fnUpdateEnrichment: func(_ context.Context, id int64, result *llm.ExtractionResult, _ *float64) error {
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
			return nil, nil
		},
	}

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
		fnUpdateEnrichment: func(_ context.Context, id int64, result *llm.ExtractionResult, _ *float64) error {
			t.Fatal("UpdateEnrichment should not be called when LLM fails")
			return nil
		},
	}

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
		fnUpdateEnrichment: func(_ context.Context, id int64, result *llm.ExtractionResult, _ *float64) error {
			t.Fatal("UpdateEnrichment should not be called when LLM returns invalid JSON")
			return nil
		},
	}

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
		RawHTML:     nil,
	}

	var capturedBody []byte
	mock := &mockListingRepo{
		fnGetByID: func(_ context.Context, id int64) (*model.ListingRow, error) {
			return listing, nil
		},
		fnUpdateEnrichment: func(_ context.Context, id int64, result *llm.ExtractionResult, _ *float64) error {
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

	body := string(capturedBody)
	assert.Contains(t, body, "RTX 3080")
	assert.Contains(t, body, "10GB VRAM")
	assert.Contains(t, body, "450 EUR")
}

func TestEnrichWorker_MarketScoreComputed(t *testing.T) {
	// LLM extracts ["RTX 4060"] at 800 BGN ask price.
	// component_prices has RTX 4060 = 1200 BGN.
	// market_score = 800 / 1200 = 0.667.
	raw := "RTX 4060 gaming PC"
	listing := &model.ListingRow{ID: 20, Title: "PC with RTX 4060", RawHTML: &raw}

	var savedMarketScore *float64

	listingMock := &mockListingRepo{
		fnGetByID: func(_ context.Context, id int64) (*model.ListingRow, error) {
			return listing, nil
		},
		fnUpdateEnrichment: func(_ context.Context, id int64, result *llm.ExtractionResult, ms *float64) error {
			savedMarketScore = ms
			return nil
		},
	}

	componentMock := &mockComponentPriceRepo{
		fnGetByNormalizedName: func(_ context.Context, name string) (*model.ComponentPrice, error) {
			if name == "rtx 4060" {
				return &model.ComponentPrice{Name: "RTX 4060", PriceAmount: 1200}, nil
			}
			return nil, nil
		},
	}

	w, srv := newTestWorkerWithComponentRepo(listingMock, componentMock, func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		rw.Write(ollamaResponse(extractionWithComponents([]string{"RTX 4060"}, 800)))
	})
	defer srv.Close()

	err := w.Work(context.Background(), newJob(20))
	require.NoError(t, err)
	require.NotNil(t, savedMarketScore, "market_score should be set when component price is found")
	// 800 / 1200 ≈ 0.667
	assert.InDelta(t, 0.667, *savedMarketScore, 0.001)
}

func TestEnrichWorker_UnknownComponentQueuesJob(t *testing.T) {
	// LLM extracts ["RTX 4060"] but it's not in component_prices.
	// A ScrapeComponentPriceArgs job should be queued.
	raw := "RTX 4060"
	listing := &model.ListingRow{ID: 21, Title: "GPU", RawHTML: &raw}

	var queuedComponent string

	listingMock := &mockListingRepo{
		fnGetByID: func(_ context.Context, id int64) (*model.ListingRow, error) {
			return listing, nil
		},
		fnUpdateEnrichment: func(_ context.Context, id int64, result *llm.ExtractionResult, ms *float64) error {
			assert.Nil(t, ms, "market_score should be nil when component not in DB")
			return nil
		},
	}

	componentMock := &mockComponentPriceRepo{
		fnGetByNormalizedName: func(_ context.Context, name string) (*model.ComponentPrice, error) {
			return nil, nil // not found
		},
	}

	w, srv := newTestWorkerWithComponentRepo(listingMock, componentMock, func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		rw.Write(ollamaResponse(extractionWithComponents([]string{"RTX 4060"}, 900)))
	})
	defer srv.Close()

	w.WithInsertComponentJobFn(func(_ context.Context, args worker.ScrapeComponentPriceArgs) error {
		queuedComponent = args.Name
		return nil
	})

	err := w.Work(context.Background(), newJob(21))
	require.NoError(t, err)
	assert.Equal(t, "RTX 4060", queuedComponent, "unknown component should be queued for scraping")
}

func TestEnrichWorker_NoComponentsNoMarketScore(t *testing.T) {
	// LLM returns empty components — market_score should stay nil.
	raw := "Nice sofa for sale"
	listing := &model.ListingRow{ID: 22, Title: "Sofa", RawHTML: &raw}

	listingMock := &mockListingRepo{
		fnGetByID: func(_ context.Context, id int64) (*model.ListingRow, error) {
			return listing, nil
		},
		fnUpdateEnrichment: func(_ context.Context, id int64, result *llm.ExtractionResult, ms *float64) error {
			assert.Nil(t, ms, "market_score should be nil for listings with no components")
			return nil
		},
	}

	w, srv := newTestWorker(listingMock, func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		rw.Write(ollamaResponse(validExtraction)) // validExtraction has "components": []
	})
	defer srv.Close()

	err := w.Work(context.Background(), newJob(22))
	require.NoError(t, err)
}
