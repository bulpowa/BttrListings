package worker

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"OlxScraper/internal/repository"

	"golang.org/x/net/html"

	"github.com/riverqueue/river"
)

// ComponentSeed is a component name + category pair used to bootstrap price scraping.
type ComponentSeed struct {
	Name     string
	Category string
}

// --- ScrapeComponentPriceWorker ---

// ScrapeComponentPriceArgs is the River job payload for scraping a single component's price.
type ScrapeComponentPriceArgs struct {
	Name     string `json:"name"`
	Category string `json:"category"`
}

func (ScrapeComponentPriceArgs) Kind() string { return "scrape_component_price" }

// ScrapeComponentPriceWorker fetches OLX search results for a component and upserts
// the median price into component_prices.
type ScrapeComponentPriceWorker struct {
	river.WorkerDefaults[ScrapeComponentPriceArgs]
	repo   *repository.Repository
	client *http.Client
}

func NewScrapeComponentPriceWorker(repo *repository.Repository) *ScrapeComponentPriceWorker {
	return &ScrapeComponentPriceWorker{
		repo:   repo,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (w *ScrapeComponentPriceWorker) Work(ctx context.Context, job *river.Job[ScrapeComponentPriceArgs]) error {
	name := job.Args.Name
	category := job.Args.Category

	// Scrape up to 3 pages for a better median sample.
	var allPrices []float64
	for page := 1; page <= 3; page++ {
		prices, err := w.scrapePricePage(ctx, name, page)
		if err != nil {
			log.Printf("component scraper: %q page %d: %v", name, page, err)
			break
		}
		allPrices = append(allPrices, prices...)
		if len(prices) == 0 {
			break // no more results on this page
		}
		// Brief pause between pages — be polite to OLX.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}

	if len(allPrices) == 0 {
		log.Printf("component scraper: %q — no prices found across all pages", name)
		return nil
	}

	median := medianPrice(allPrices)
	if median <= 0 {
		return nil
	}

	normalized := normalizeComponentName(name)
	if err := w.repo.ComponentPrice.Upsert(ctx, name, normalized, category, median, "BGN", int32(len(allPrices))); err != nil {
		return fmt.Errorf("upsert component price %q: %w", name, err)
	}

	log.Printf("component scraper: %q → median %.2f BGN (from %d listings across pages)", name, median, len(allPrices))
	return nil
}

func (w *ScrapeComponentPriceWorker) scrapePricePage(ctx context.Context, name string, page int) ([]float64, error) {
	searchURL := buildSearchURL(name)
	if page > 1 {
		searchURL += fmt.Sprintf("?page=%d", page)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", searchURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	candidates := parseCandidatesFromPage(string(body))
	return filteredPrices(candidates), nil
}

// --- RefreshStaleComponentPricesWorker ---

// RefreshStaleComponentPricesArgs is the River job payload for the periodic refresh trigger.
type RefreshStaleComponentPricesArgs struct{}

func (RefreshStaleComponentPricesArgs) Kind() string { return "refresh_stale_component_prices" }

// InsertComponentJobFn is a function that queues a ScrapeComponentPriceArgs job.
type InsertComponentJobFn func(ctx context.Context, args ScrapeComponentPriceArgs) error

// RefreshStaleComponentPricesWorker runs periodically and re-queues scrape jobs for:
//   - seed components not yet in the DB
//   - existing component prices older than staleAfter
type RefreshStaleComponentPricesWorker struct {
	river.WorkerDefaults[RefreshStaleComponentPricesArgs]
	repo       *repository.Repository
	insertJob  InsertComponentJobFn
	seeds      []ComponentSeed
	staleAfter time.Duration
}

func NewRefreshStaleComponentPricesWorker(
	repo *repository.Repository,
	insertJob InsertComponentJobFn,
	seeds []ComponentSeed,
	staleAfter time.Duration,
) *RefreshStaleComponentPricesWorker {
	return &RefreshStaleComponentPricesWorker{
		repo:       repo,
		insertJob:  insertJob,
		seeds:      seeds,
		staleAfter: staleAfter,
	}
}

func (w *RefreshStaleComponentPricesWorker) Work(ctx context.Context, _ *river.Job[RefreshStaleComponentPricesArgs]) error {
	queued := 0

	// Re-scrape existing stale entries.
	stale, err := w.repo.ComponentPrice.ListStale(ctx, w.staleAfter)
	if err != nil {
		return fmt.Errorf("list stale: %w", err)
	}
	for _, cp := range stale {
		if err := w.insertJob(ctx, ScrapeComponentPriceArgs{Name: cp.Name, Category: cp.Category}); err != nil {
			log.Printf("refresh: queue stale %q: %v", cp.Name, err)
			continue
		}
		queued++
	}

	// Queue seeds that are not in the DB yet.
	for _, seed := range w.seeds {
		normalized := normalizeComponentName(seed.Name)
		existing, err := w.repo.ComponentPrice.GetByNormalizedName(ctx, normalized)
		if err != nil {
			log.Printf("refresh: lookup %q: %v", seed.Name, err)
			continue
		}
		if existing != nil {
			continue // already present (stale ones handled above)
		}
		if err := w.insertJob(ctx, ScrapeComponentPriceArgs{Name: seed.Name, Category: seed.Category}); err != nil {
			log.Printf("refresh: queue seed %q: %v", seed.Name, err)
			continue
		}
		queued++
	}

	log.Printf("component refresh: queued %d jobs (%d stale + seeds)", queued, len(stale))
	return nil
}

// --- price parsing helpers ---

// pricedListing pairs a listing title with its parsed BGN price.
type pricedListing struct {
	title string
	price float64
}

// systemKeywords are substrings that indicate a complete system rather than a
// standalone component. Listings matching any keyword are excluded from price samples
// to prevent full PC / laptop prices from skewing the component median.
var systemKeywords = []string{
	// Bulgarian
	"компютър", "лаптоп", "конфигурац", "комплект", "кула", "система",
	// English
	"laptop", "notebook", "desktop", "gaming pc", "pc build",
	"complete", "bundle", "workstation",
}

// parseCandidatesFromPage walks an OLX search results page and extracts
// (title, price) pairs from <p data-testid="ad-price"> elements.
// The title is found by walking up the DOM to the nearest <h4> in the card.
func parseCandidatesFromPage(rawHTML string) []pricedListing {
	doc, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		log.Printf("component scraper: html parse error: %v", err)
		return nil
	}

	var candidates []pricedListing
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "p" {
			for _, a := range n.Attr {
				if a.Key == "data-testid" && a.Val == "ad-price" {
					if p := parseBGNPrice(n); p > 0 {
						candidates = append(candidates, pricedListing{
							title: cardTitleNear(n),
							price: p,
						})
					}
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return candidates
}

// parsePricesFromPage is the existing API used by tests and callers that don't
// need filtering. It returns raw prices from the page without any filtering.
func parsePricesFromPage(rawHTML string) []float64 {
	candidates := parseCandidatesFromPage(rawHTML)
	if len(candidates) == 0 {
		log.Printf("component scraper: no ad-price elements found — OLX page structure may have changed")
		return []float64{}
	}
	prices := make([]float64, 0, len(candidates))
	for _, c := range candidates {
		prices = append(prices, c.price)
	}
	return prices
}

// filteredPrices applies two-stage filtering to a candidate list:
//  1. Keyword filter: drops listings whose title suggests a complete system.
//  2. IQR outlier removal: drops prices outside Q1-1.5×IQR … Q3+1.5×IQR.
//
// This prevents full gaming PC / laptop listings from skewing the median upward
// when searching for a standalone component like "RTX 4060".
func filteredPrices(candidates []pricedListing) []float64 {
	// Stage 1: keyword filter.
	var kept []float64
	var dropped int
	for _, c := range candidates {
		lower := strings.ToLower(c.title)
		skip := false
		for _, kw := range systemKeywords {
			if strings.Contains(lower, kw) {
				skip = true
				break
			}
		}
		if skip {
			dropped++
			continue
		}
		kept = append(kept, c.price)
	}
	if dropped > 0 {
		log.Printf("component scraper: keyword filter dropped %d complete-system listings", dropped)
	}

	// Stage 2: IQR outlier removal (needs at least 4 prices to be meaningful).
	if len(kept) < 4 {
		return kept
	}
	sorted := make([]float64, len(kept))
	copy(sorted, kept)
	sort.Float64s(sorted)
	n := len(sorted)
	q1 := sorted[n/4]
	q3 := sorted[3*n/4]
	iqr := q3 - q1
	if iqr == 0 {
		return kept
	}
	lower := q1 - 1.5*iqr
	upper := q3 + 1.5*iqr
	result := kept[:0]
	for _, p := range kept {
		if p >= lower && p <= upper {
			result = append(result, p)
		}
	}
	return result
}

// cardTitleNear walks up from a DOM node up to 10 levels looking for the
// nearest ancestor that contains an <h4> element (OLX listing card title).
func cardTitleNear(n *html.Node) string {
	node := n.Parent
	for i := 0; i < 10 && node != nil; i++ {
		if t := firstH4Text(node); t != "" {
			return t
		}
		node = node.Parent
	}
	return ""
}

func firstH4Text(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "h4" {
		return nodeText(n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if t := firstH4Text(c); t != "" {
			return t
		}
	}
	return ""
}

func nodeText(n *html.Node) string {
	var b strings.Builder
	var collect func(*html.Node)
	collect = func(node *html.Node) {
		if node.Type == html.TextNode {
			b.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			collect(c)
		}
	}
	collect(n)
	return strings.TrimSpace(b.String())
}

// parseBGNPrice extracts the BGN float from the first text node of a price element.
// OLX format: "1777.85 лв. / 909 €" — takes the number before the first space.
func parseBGNPrice(n *html.Node) float64 {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.TextNode {
			continue
		}
		text := strings.TrimSpace(c.Data)
		if text == "" {
			continue
		}
		if idx := strings.IndexByte(text, ' '); idx > 0 {
			text = text[:idx]
		}
		text = strings.ReplaceAll(text, ",", ".")
		f, err := strconv.ParseFloat(text, 64)
		if err == nil && f > 0 {
			return f
		}
	}
	return 0
}

// medianPrice returns the median of a price slice. Returns 0 for empty input.
func medianPrice(prices []float64) float64 {
	if len(prices) == 0 {
		return 0
	}
	sorted := make([]float64, len(prices))
	copy(sorted, prices)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 0 {
		return (sorted[mid-1] + sorted[mid]) / 2
	}
	return sorted[mid]
}

// normalizeComponentName lowercases and trims a component name for DB storage/lookup.
func normalizeComponentName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// buildSearchURL constructs the OLX search URL for a component name.
// e.g. "RTX 4060" → "https://www.olx.bg/ads/q-rtx-4060/"
func buildSearchURL(name string) string {
	slug := strings.ToLower(strings.TrimSpace(name))
	slug = strings.ReplaceAll(slug, " ", "-")
	var b strings.Builder
	for _, r := range slug {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	return "https://www.olx.bg/ads/q-" + b.String() + "/"
}
