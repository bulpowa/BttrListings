package alert

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"time"
)

// Notifier sends deal alerts to a configurable webhook URL.
// It is a no-op when URL is empty so the enrichment pipeline works
// without any alert configuration.
type Notifier struct {
	url    string
	client *http.Client
}

// New creates a Notifier. webhookURL may be empty — the notifier
// becomes a no-op and no HTTP calls are made.
func New(webhookURL string) *Notifier {
	return &Notifier{
		url:    webhookURL,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// Send fires a deal alert in a background goroutine and returns immediately.
// A failed send is logged but never propagates — it must not block or fail
// the enrichment job that calls it.
func (n *Notifier) Send(listingID int64, title string, dealScore int, priceAmount float64, priceCurrency, listingURL string) {
	if n.url == "" {
		return
	}
	go n.send(listingID, title, dealScore, priceAmount, priceCurrency, listingURL)
}

func (n *Notifier) send(listingID int64, title string, dealScore int, priceAmount float64, priceCurrency, listingURL string) {
	price := fmt.Sprintf("%.2f %s", priceAmount, priceCurrency)
	if priceCurrency == "" {
		price = fmt.Sprintf("%.2f", priceAmount)
	}

	body := fmt.Sprintf("Deal score: %d/10\nTitle: %s\nPrice: %s\nURL: %s",
		dealScore, title, price, listingURL)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, n.url, bytes.NewBufferString(body))
	if err != nil {
		log.Printf("alert: build request for listing %d: %v", listingID, err)
		return
	}
	req.Header.Set("Content-Type", "text/plain")
	// ntfy.sh-compatible headers (ignored by generic webhooks)
	req.Header.Set("Title", fmt.Sprintf("Deal alert (score %d): %s", dealScore, title))
	req.Header.Set("Priority", "high")
	req.Header.Set("Tags", fmt.Sprintf("deal,score-%d", dealScore))

	resp, err := n.client.Do(req)
	if err != nil {
		log.Printf("alert: send for listing %d: %v", listingID, err)
		return
	}
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		log.Printf("alert: webhook returned %d for listing %d", resp.StatusCode, listingID)
	}
}
