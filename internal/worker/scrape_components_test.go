package worker

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
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
		name string
		want string
	}{
		{"RTX 4060", "https://www.olx.bg/ads/q-rtx-4060/"},
		{"iPhone 13 128GB", "https://www.olx.bg/ads/q-iphone-13-128gb/"},
		{"MacBook Air M1", "https://www.olx.bg/ads/q-macbook-air-m1/"},
		{"  RTX 4060  ", "https://www.olx.bg/ads/q-rtx-4060/"},
	}
	for _, tc := range cases {
		got := buildSearchURL(tc.name)
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
