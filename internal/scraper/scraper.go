package scraper

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"OlxScraper/internal/model"
	"OlxScraper/internal/repository"
	"OlxScraper/internal/worker"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// Scraper fetches OLX search pages periodically and inserts new listings into Postgres,
// atomically enqueueing a River enrichment job for each new listing.
type Scraper struct {
	urls   []string
	repo   *repository.Repository
	river  *river.Client[pgx.Tx]
	pool   *pgxpool.Pool
	client *http.Client
}

func New(urls []string, repo *repository.Repository, riverClient *river.Client[pgx.Tx], pool *pgxpool.Pool) *Scraper {
	return &Scraper{
		urls:  urls,
		repo:  repo,
		river: riverClient,
		pool:  pool,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// Run starts the periodic scrape loop. It runs immediately on start, then every 15 minutes.
// It exits cleanly when ctx is cancelled.
func (s *Scraper) Run(ctx context.Context) {
	log.Println("scraper: starting")
	s.scrapeAll(ctx)

	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("scraper: stopped")
			return
		case <-ticker.C:
			s.scrapeAll(ctx)
		}
	}
}

func (s *Scraper) scrapeAll(ctx context.Context) {
	for _, url := range s.urls {
		if err := s.scrapePage(ctx, url); err != nil {
			log.Printf("scraper: error scraping %s: %v", url, err)
		}
	}
}

func (s *Scraper) scrapePage(ctx context.Context, searchURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; BttrListings/1.0)")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", searchURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	listings := parseListings(string(body), searchURL)
	log.Printf("scraper: found %d listings on %s", len(listings), searchURL)

	for _, l := range listings {
		if err := s.insertListing(ctx, l); err != nil {
			log.Printf("scraper: insert listing %s: %v", l.URL, err)
		}
	}
	return nil
}

// insertListing atomically inserts a listing and enqueues a River enrichment job.
// Uses a pgx transaction so both operations succeed or fail together.
func (s *Scraper) insertListing(ctx context.Context, input *model.CreateListingInput) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		id, err := repository.InsertListingTx(ctx, tx, input)
		if err != nil {
			return err
		}
		if id == 0 {
			return nil // already exists — skip
		}

		_, err = s.river.InsertTx(ctx, tx, worker.EnrichListingArgs{ListingID: id}, &river.InsertOpts{
			UniqueOpts: river.UniqueOpts{ByArgs: true},
		})
		return err
	})
}

// parseListings parses listing cards from an OLX search results page.
// Adapt the selectors for your OLX region — the structure varies by country.
func parseListings(html, baseURL string) []*model.CreateListingInput {
	var listings []*model.CreateListingInput

	// TODO: adapt these selectors for your OLX region.
	// The example below looks for anchor tags whose href contains "/oferta/" or "/item/",
	// which is the common OLX listing URL pattern.
	//
	// For production use, replace this with a proper HTML parser
	// (e.g. golang.org/x/net/html) and target the exact card structure.

	lines := strings.Split(html, "\n")
	seen := map[string]bool{}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, `href="`) {
			continue
		}
		// Extract href value
		start := strings.Index(line, `href="`)
		if start == -1 {
			continue
		}
		start += 6
		end := strings.Index(line[start:], `"`)
		if end == -1 {
			continue
		}
		href := line[start : start+end]

		// Filter for listing URLs — olx.bg uses /ad/, other regions use /oferta/ or /item/
		if !strings.Contains(href, "/oferta/") && !strings.Contains(href, "/item/") && !strings.Contains(href, "/ad/") {
			continue
		}

		// Normalise and deduplicate
		listingURL := href
		if strings.HasPrefix(href, "/") {
			// Relative URL — prepend base
			baseHost := extractHost(baseURL)
			listingURL = baseHost + href
		}

		hash := hashURL(listingURL)
		if seen[hash] {
			continue
		}
		seen[hash] = true

		rawHTML := line // Store the raw line as minimal raw_html
		title := extractTitle(line)

		listings = append(listings, &model.CreateListingInput{
			URL:     listingURL,
			URLHash: hash,
			Title:   title,
			RawHTML: &rawHTML,
		})
	}

	return listings
}

func hashURL(rawURL string) string {
	normalized := normalizeURL(rawURL)
	h := sha256.Sum256([]byte(normalized))
	return fmt.Sprintf("%x", h)
}

func normalizeURL(rawURL string) string {
	u := strings.ToLower(rawURL)
	if idx := strings.Index(u, "?"); idx != -1 {
		u = u[:idx]
	}
	u = strings.TrimRight(u, "/")
	return u
}

func extractHost(rawURL string) string {
	// e.g. "https://www.olx.pl/oferta/..." → "https://www.olx.pl"
	if idx := strings.Index(rawURL, "://"); idx != -1 {
		rest := rawURL[idx+3:]
		if slashIdx := strings.Index(rest, "/"); slashIdx != -1 {
			return rawURL[:idx+3+slashIdx]
		}
	}
	return ""
}

func extractTitle(line string) string {
	// Very naive: grab text between > and <
	start := strings.LastIndex(line, ">")
	end := strings.Index(line[start:], "<")
	if start == -1 || end == -1 {
		return "Untitled"
	}
	title := strings.TrimSpace(line[start+1 : start+end])
	if title == "" {
		return "Untitled"
	}
	return title
}
