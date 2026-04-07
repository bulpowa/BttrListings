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
	"golang.org/x/net/html"
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

	listings := ParseListings(string(body), searchURL)
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

// isListingHref reports whether an href is an OLX listing URL.
// Covers olx.bg (/d/ad/), olx.pl (/oferta/), and other OLX regions (/item/, /ad/).
func isListingHref(href string) bool {
	return strings.Contains(href, "/d/ad/") ||
		strings.Contains(href, "/oferta/") ||
		strings.Contains(href, "/item/") ||
		(strings.Contains(href, "/ad/") && !strings.Contains(href, "/d/ad/"))
}

// ParseListings parses listing cards from an OLX search results page using a
// proper HTML parser. It walks every <a> element and collects those whose href
// matches known OLX listing URL patterns. Exported so cmd/scrape-check can use
// it without a database.
func ParseListings(rawHTML, baseURL string) []*model.CreateListingInput {
	var listings []*model.CreateListingInput
	seen := map[string]bool{}
	baseHost := extractHost(baseURL)

	doc, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		log.Printf("scraper: html parse error: %v", err)
		return nil
	}

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			href := attrVal(n, "href")
			if href == "" || !isListingHref(href) {
				// still recurse into children
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
				return
			}

			// Resolve relative URLs
			listingURL := href
			if strings.HasPrefix(href, "/") {
				listingURL = baseHost + href
			}

			// Strip query string for dedup/normalisation
			hash := hashURL(listingURL)
			if seen[hash] {
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
				return
			}
			seen[hash] = true

			// Clean URL (no query params)
			cleanURL := listingURL
			if idx := strings.Index(cleanURL, "?"); idx != -1 {
				cleanURL = cleanURL[:idx]
			}

			title := textContent(n)
			if title == "" {
				title = "Untitled"
			}

			cardHTML := renderNode(n)
			listings = append(listings, &model.CreateListingInput{
				URL:     cleanURL,
				URLHash: hash,
				Title:   title,
				RawHTML: &cardHTML,
			})
			return // don't recurse into children of a listing anchor
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	return listings
}

// attrVal returns the value of the named attribute on an HTML element node.
func attrVal(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

// textContent returns the best title text from an anchor node.
// OLX cards use two anchors per listing: an image anchor (with <img alt="title">)
// and a text anchor (with <h4>title</h4>). We collect visible text nodes and
// fall back to <img alt> so whichever anchor we encounter first yields a title.
func textContent(n *html.Node) string {
	var b strings.Builder
	var imgAlt string

	var collect func(*html.Node)
	collect = func(node *html.Node) {
		if node.Type == html.ElementNode {
			switch node.Data {
			case "style", "script":
				return // skip CSS/JS subtrees
			case "img":
				if imgAlt == "" {
					imgAlt = attrVal(node, "alt")
				}
			}
		}
		if node.Type == html.TextNode {
			b.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			collect(c)
		}
	}
	collect(n)

	if t := strings.TrimSpace(b.String()); t != "" {
		return t
	}
	return strings.TrimSpace(imgAlt)
}

// renderNode renders an HTML node back to a string for storage as raw_html.
func renderNode(n *html.Node) string {
	var b strings.Builder
	_ = html.Render(&b, n)
	return b.String()
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

