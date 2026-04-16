package worker

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"OlxScraper/internal/model"
	"OlxScraper/internal/repository"

	"github.com/riverqueue/river"
)

// --- parsePricesFromPage ---

func TestParsePricesFromPage_Normal(t *testing.T) {
	html := `<html><body>
		<p data-testid="ad-price" class="css-blr5zl">1777.85 лв. / 909 €</p>
		<p data-testid="ad-price" class="css-blr5zl">1200 лв. / 613 €</p>
		<p data-testid="ad-price" class="css-blr5zl">900.50 лв. / 460 €</p>
	</body></html>`

	prices := parsePricesFromPage(html)
	if len(prices) != 3 {
		t.Fatalf("expected 3 prices, got %d", len(prices))
	}
	if prices[0] != 1777.85 {
		t.Errorf("expected 1777.85, got %f", prices[0])
	}
	if prices[1] != 1200.0 {
		t.Errorf("expected 1200.0, got %f", prices[1])
	}
	if prices[2] != 900.50 {
		t.Errorf("expected 900.50, got %f", prices[2])
	}
}

func TestParsePricesFromPage_Empty(t *testing.T) {
	html := `<html><body><p>no prices here</p></body></html>`
	prices := parsePricesFromPage(html)
	if prices == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(prices) != 0 {
		t.Fatalf("expected 0 prices, got %d", len(prices))
	}
}

func TestParsePricesFromPage_InvalidHTML(t *testing.T) {
	prices := parsePricesFromPage("not html at all <<<")
	if prices == nil {
		t.Fatal("expected non-nil slice on invalid html")
	}
}

func TestParsePricesFromPage_CommaDecimalSeparator(t *testing.T) {
	// OLX.bg uses "." as decimal separator (e.g. "1777.85 лв.").
	// European "1.234,50" format is not used on OLX.bg — this test documents
	// that such a value is silently skipped (no panic, no wrong result).
	html := `<html><body>
		<p data-testid="ad-price">1.234,50 лв. / 630 €</p>
		<p data-testid="ad-price">1200 лв. / 613 €</p>
	</body></html>`
	prices := parsePricesFromPage(html)
	// Only the valid "1200" should be parsed.
	if len(prices) != 1 {
		t.Fatalf("expected 1 price, got %d: %v", len(prices), prices)
	}
	if prices[0] != 1200 {
		t.Errorf("expected 1200, got %f", prices[0])
	}
}

// --- medianPrice ---

func TestMedianPrice_Odd(t *testing.T) {
	prices := []float64{300, 100, 200}
	got := medianPrice(prices)
	if got != 200 {
		t.Errorf("expected 200, got %f", got)
	}
}

func TestMedianPrice_Even(t *testing.T) {
	prices := []float64{100, 200, 300, 400}
	got := medianPrice(prices)
	if got != 250 {
		t.Errorf("expected 250, got %f", got)
	}
}

func TestMedianPrice_Single(t *testing.T) {
	got := medianPrice([]float64{42.5})
	if got != 42.5 {
		t.Errorf("expected 42.5, got %f", got)
	}
}

func TestMedianPrice_Empty(t *testing.T) {
	got := medianPrice([]float64{})
	if got != 0 {
		t.Errorf("expected 0, got %f", got)
	}
}

func TestMedianPrice_DoesNotMutateInput(t *testing.T) {
	prices := []float64{300, 100, 200}
	medianPrice(prices)
	if prices[0] != 300 {
		t.Error("medianPrice mutated the input slice")
	}
}

// --- buildSearchURL ---

func TestBuildSearchURL(t *testing.T) {
	cases := []struct {
		name    string
		want    string
		wantErr bool
	}{
		{"RTX 4060", "https://www.olx.bg/ads/q-rtx-4060/", false},
		{"iPhone 13 128GB", "https://www.olx.bg/ads/q-iphone-13-128gb/", false},
		{"MacBook Air M1", "https://www.olx.bg/ads/q-macbook-air-m1/", false},
		{"  RTX 4060  ", "https://www.olx.bg/ads/q-rtx-4060/", false},
		{"Фотоапарат", "", true},  // Cyrillic-only → empty slug → error
		{"   ", "", true},         // whitespace-only → empty slug → error
	}
	for _, tc := range cases {
		got, err := buildSearchURL(tc.name)
		if tc.wantErr {
			if err == nil {
				t.Errorf("buildSearchURL(%q): expected error, got %q", tc.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("buildSearchURL(%q): unexpected error: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("buildSearchURL(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// --- ScrapeComponentPriceWorker integration (HTTP mock) ---

func TestScrapeComponentPriceWorker_HappyPath(t *testing.T) {
	// Serve a fake OLX search results page with price elements.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body>
			<p data-testid="ad-price">1000 лв. / 511 €</p>
			<p data-testid="ad-price">1200 лв. / 613 €</p>
			<p data-testid="ad-price">800 лв. / 409 €</p>
		</body></html>`)
	}))
	defer srv.Close()

	prices := parsePricesFromPage(fetchTestPage(t, srv.URL))
	median := medianPrice(prices)

	if len(prices) != 3 {
		t.Fatalf("expected 3 prices, got %d", len(prices))
	}
	if median != 1000 {
		t.Errorf("expected median 1000, got %f", median)
	}
}

func TestScrapeComponentPriceWorker_NoListings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body><p>нямаме резултати</p></body></html>`)
	}))
	defer srv.Close()

	prices := parsePricesFromPage(fetchTestPage(t, srv.URL))
	if len(prices) != 0 {
		t.Errorf("expected 0 prices for empty page, got %d", len(prices))
	}
	// medianPrice on empty must not panic and must return 0
	m := medianPrice(prices)
	if math.IsNaN(m) || m != 0 {
		t.Errorf("expected 0 median for empty prices, got %f", m)
	}
}

// fetchTestPage is a helper that GETs a URL and returns the body string.
func fetchTestPage(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url) //nolint:gosec // test helper, URL is from httptest
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	var b []byte
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		b = append(b, buf[:n]...)
		if err != nil {
			break
		}
	}
	return string(b)
}

// --- filteredPrices ---

func TestFilteredPrices_DropsCompleteSystemsByKeyword(t *testing.T) {
	candidates := []pricedListing{
		{title: "RTX 4060 8GB ASUS TUF", price: 600},
		{title: "RTX 4060 Gigabyte Gaming OC", price: 650},
		{title: "Геймърски компютър RTX 4060 i7", price: 2800}, // should be dropped
		{title: "Gaming laptop RTX 4060 16GB", price: 3200},    // should be dropped
		{title: "RTX 4060 MSI Ventus", price: 580},
	}
	got := filteredPrices(candidates)
	if len(got) != 3 {
		t.Fatalf("expected 3 prices after keyword filter, got %d: %v", len(got), got)
	}
	for _, p := range got {
		if p > 1000 {
			t.Errorf("complete-system price %.0f leaked through filter", p)
		}
	}
}

func TestFilteredPrices_IQRRemovesOutliers(t *testing.T) {
	// All standalone GPU titles, but one extreme outlier price.
	candidates := []pricedListing{
		{title: "RTX 4060", price: 550},
		{title: "RTX 4060", price: 600},
		{title: "RTX 4060", price: 620},
		{title: "RTX 4060", price: 580},
		{title: "RTX 4060", price: 610},
		{title: "RTX 4060", price: 590},
		{title: "RTX 4060", price: 5000}, // extreme outlier (data error / wrong listing)
	}
	got := filteredPrices(candidates)
	for _, p := range got {
		if p > 1000 {
			t.Errorf("outlier price %.0f leaked through IQR filter", p)
		}
	}
}

func TestFilteredPrices_EmptyInput(t *testing.T) {
	got := filteredPrices(nil)
	if len(got) != 0 {
		t.Errorf("expected empty result for nil input, got %v", got)
	}
}

func TestFilteredPrices_TooFewForIQR(t *testing.T) {
	// With <4 prices, IQR is skipped — all kept prices are returned as-is.
	candidates := []pricedListing{
		{title: "RTX 4060", price: 600},
		{title: "RTX 4060", price: 700},
	}
	got := filteredPrices(candidates)
	if len(got) != 2 {
		t.Errorf("expected 2 prices when IQR skipped, got %d", len(got))
	}
}

// --- mockComponentPriceRepoForScraper ---

type mockComponentPriceRepoForScraper struct {
	repository.ComponentPriceRepository

	upsertCalls []upsertCall
}

type upsertCall struct {
	name       string
	normalized string
	category   string
	price      float64
	count      int32
}

func (m *mockComponentPriceRepoForScraper) Upsert(_ context.Context, name, normalized, category string, price float64, _ string, count int32) error {
	m.upsertCalls = append(m.upsertCalls, upsertCall{name: name, normalized: normalized, category: category, price: price, count: count})
	return nil
}

// --- ScrapeComponentPriceWorker.Work() end-to-end ---

func TestScrapeComponentPriceWorker_Work_UpsertMedian(t *testing.T) {
	// Serve a single-page OLX result with 3 prices. Median = 1000.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body>
			<h4>RTX 4060</h4><p data-testid="ad-price">800 лв. / 409 €</p>
			<h4>RTX 4060</h4><p data-testid="ad-price">1000 лв. / 511 €</p>
			<h4>RTX 4060</h4><p data-testid="ad-price">1200 лв. / 613 €</p>
		</body></html>`)
	}))
	defer srv.Close()

	mock := &mockComponentPriceRepoForScraper{}
	repo := &repository.Repository{ComponentPrice: mock}
	w := newScrapeWorkerWithURL(repo, srv.URL)

	job := &river.Job[ScrapeComponentPriceArgs]{
		Args: ScrapeComponentPriceArgs{Name: "RTX 4060", Category: "gpu"},
	}
	err := w.Work(context.Background(), job)
	if err != nil {
		t.Fatalf("Work returned unexpected error: %v", err)
	}
	if len(mock.upsertCalls) != 1 {
		t.Fatalf("expected 1 Upsert call, got %d", len(mock.upsertCalls))
	}
	call := mock.upsertCalls[0]
	if call.name != "RTX 4060" {
		t.Errorf("expected name 'RTX 4060', got %q", call.name)
	}
	if call.normalized != "rtx 4060" {
		t.Errorf("expected normalized 'rtx 4060', got %q", call.normalized)
	}
	if call.price != 1000 {
		t.Errorf("expected median 1000, got %f", call.price)
	}
}

func TestScrapeComponentPriceWorker_Work_NoPrices_NoUpsert(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body><p>нямаме резултати</p></body></html>`)
	}))
	defer srv.Close()

	mock := &mockComponentPriceRepoForScraper{}
	repo := &repository.Repository{ComponentPrice: mock}
	w := newScrapeWorkerWithURL(repo, srv.URL)

	job := &river.Job[ScrapeComponentPriceArgs]{
		Args: ScrapeComponentPriceArgs{Name: "RTX 4060"},
	}
	err := w.Work(context.Background(), job)
	if err != nil {
		t.Fatalf("Work returned error on empty results: %v", err)
	}
	if len(mock.upsertCalls) != 0 {
		t.Errorf("expected no Upsert when no prices found, got %d", len(mock.upsertCalls))
	}
}

// newScrapeWorkerWithURL creates a ScrapeComponentPriceWorker that fetches from the
// given base URL (test server) instead of OLX.
func newScrapeWorkerWithURL(repo *repository.Repository, baseURL string) *testScrapeWorker {
	return &testScrapeWorker{
		ScrapeComponentPriceWorker: NewScrapeComponentPriceWorker(repo),
		baseURL:                    baseURL,
	}
}

// testScrapeWorker wraps the real worker but overrides the search URL for testing.
type testScrapeWorker struct {
	*ScrapeComponentPriceWorker
	baseURL string
}

func (w *testScrapeWorker) Work(ctx context.Context, job *river.Job[ScrapeComponentPriceArgs]) error {
	// Re-implement Work using the test base URL instead of OLX.
	name := job.Args.Name
	category := job.Args.Category

	var allPrices []float64
	for page := 1; page <= 3; page++ {
		url := w.baseURL
		if page > 1 {
			url += fmt.Sprintf("?page=%d", page)
		}
		prices, err := w.scrapePricePage(ctx, url)
		if err != nil || len(prices) == 0 {
			break
		}
		allPrices = append(allPrices, prices...)
	}

	if len(allPrices) == 0 {
		return nil
	}
	median := medianPrice(allPrices)
	if median <= 0 {
		return nil
	}
	normalized := normalizeComponentName(name)
	return w.repo.ComponentPrice.Upsert(ctx, name, normalized, category, median, "BGN", int32(len(allPrices)))
}

// scrapePricePage fetches a URL directly (used by testScrapeWorker).
func (w *testScrapeWorker) scrapePricePage(ctx context.Context, url string) ([]float64, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var buf []byte
	tmp := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return filteredPrices(parseCandidatesFromPage(string(buf))), nil
}

// --- RefreshStaleComponentPricesWorker ---

type mockComponentPriceRepoForRefresh struct {
	repository.ComponentPriceRepository

	staleEntries []*model.ComponentPrice
	lookupResult map[string]*model.ComponentPrice
}

func (m *mockComponentPriceRepoForRefresh) ListStale(_ context.Context, _ time.Duration) ([]*model.ComponentPrice, error) {
	return m.staleEntries, nil
}

func (m *mockComponentPriceRepoForRefresh) GetByNormalizedName(_ context.Context, name string) (*model.ComponentPrice, error) {
	if m.lookupResult != nil {
		if cp, ok := m.lookupResult[name]; ok {
			return cp, nil
		}
	}
	return nil, nil
}

func TestRefreshWorker_StaleEntry_Requeued(t *testing.T) {
	stale := []*model.ComponentPrice{{Name: "RTX 4060", Category: "gpu"}}
	mock := &mockComponentPriceRepoForRefresh{staleEntries: stale}
	repo := &repository.Repository{ComponentPrice: mock}

	var queued []ScrapeComponentPriceArgs
	insertFn := func(_ context.Context, args ScrapeComponentPriceArgs) error {
		queued = append(queued, args)
		return nil
	}

	w := NewRefreshStaleComponentPricesWorker(repo, insertFn, nil, 6*time.Hour)
	err := w.Work(context.Background(), &river.Job[RefreshStaleComponentPricesArgs]{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(queued) != 1 || queued[0].Name != "RTX 4060" {
		t.Errorf("expected stale entry to be requeued, got %v", queued)
	}
}

func TestRefreshWorker_MissingSeed_Queued(t *testing.T) {
	// No stale entries, seed is not in DB.
	mock := &mockComponentPriceRepoForRefresh{}
	repo := &repository.Repository{ComponentPrice: mock}

	seeds := []ComponentSeed{{Name: "RTX 4060", Category: "gpu"}}
	var queued []ScrapeComponentPriceArgs
	insertFn := func(_ context.Context, args ScrapeComponentPriceArgs) error {
		queued = append(queued, args)
		return nil
	}

	w := NewRefreshStaleComponentPricesWorker(repo, insertFn, seeds, 6*time.Hour)
	err := w.Work(context.Background(), &river.Job[RefreshStaleComponentPricesArgs]{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(queued) != 1 || queued[0].Name != "RTX 4060" {
		t.Errorf("expected missing seed to be queued, got %v", queued)
	}
}

func TestRefreshWorker_FreshSeed_NotRequeued(t *testing.T) {
	// Seed already exists in DB (non-stale).
	existing := &model.ComponentPrice{Name: "RTX 4060"}
	mock := &mockComponentPriceRepoForRefresh{
		lookupResult: map[string]*model.ComponentPrice{"rtx 4060": existing},
	}
	repo := &repository.Repository{ComponentPrice: mock}

	seeds := []ComponentSeed{{Name: "RTX 4060", Category: "gpu"}}
	var queued []ScrapeComponentPriceArgs
	insertFn := func(_ context.Context, args ScrapeComponentPriceArgs) error {
		queued = append(queued, args)
		return nil
	}

	w := NewRefreshStaleComponentPricesWorker(repo, insertFn, seeds, 6*time.Hour)
	err := w.Work(context.Background(), &river.Job[RefreshStaleComponentPricesArgs]{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(queued) != 0 {
		t.Errorf("expected no jobs queued for fresh seed, got %v", queued)
	}
}
