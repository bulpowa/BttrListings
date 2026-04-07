// scrape-check fetches OLX search pages and prints the parsed listings.
// No database, no Ollama, no River — just the HTTP scraper and HTML parser.
//
// Usage:
//
//	go run ./cmd/scrape-check https://www.olx.bg/elektronika/
//	go run ./cmd/scrape-check https://www.olx.bg/elektronika/ https://www.olx.bg/dom-i-gradina/
//
// Or via SCRAPER_URLS env var (same format as the main binary):
//
//	SCRAPER_URLS=https://www.olx.bg/elektronika/ go run ./cmd/scrape-check
package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"OlxScraper/internal/scraper"
)

func main() {
	urls := os.Args[1:]
	if len(urls) == 0 {
		if env := os.Getenv("SCRAPER_URLS"); env != "" {
			for _, u := range strings.Split(env, ",") {
				if u = strings.TrimSpace(u); u != "" {
					urls = append(urls, u)
				}
			}
		}
	}
	if len(urls) == 0 {
		fmt.Fprintln(os.Stderr, "usage: scrape-check <url> [url...]")
		fmt.Fprintln(os.Stderr, "       SCRAPER_URLS=<url> scrape-check")
		os.Exit(1)
	}

	client := &http.Client{Timeout: 30 * time.Second}

	for _, url := range urls {
		fmt.Printf("\n=== %s ===\n", url)
		listings, err := fetchAndParse(client, url)
		if err != nil {
			log.Printf("error: %v", err)
			continue
		}
		fmt.Printf("Found %d listings\n\n", len(listings))
		for i, l := range listings {
			fmt.Printf("  [%3d] %s\n        %s\n", i+1, l.Title, l.URL)
		}
	}
}

type result struct {
	Title string
	URL   string
}

func fetchAndParse(client *http.Client, url string) ([]result, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	listings := scraper.ParseListings(string(body), url)
	out := make([]result, len(listings))
	for i, l := range listings {
		title := "Untitled"
		if l.Title != "" {
			title = l.Title
		}
		out[i] = result{Title: title, URL: l.URL}
	}
	return out, nil
}
