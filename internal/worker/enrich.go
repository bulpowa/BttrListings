package worker

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"OlxScraper/internal/alert"
	"OlxScraper/internal/llm"
	"OlxScraper/internal/model"
	"OlxScraper/internal/repository"

	"github.com/riverqueue/river"
)

// QueueEnrich is the River queue used for LLM enrichment jobs.
// Kept separate from the default queue so component scraping workers
// don't compete with the (slow) LLM enrichment workers.
const QueueEnrich = "enrich"

// EnrichListingArgs is the River job payload for listing enrichment.
type EnrichListingArgs struct {
	ListingID int64 `json:"listing_id"`
}

func (EnrichListingArgs) Kind() string { return "enrich_listing" }

// EnrichListingWorker loads a raw listing, fetches its full OLX page,
// calls the LLM for fact extraction, computes market-based scores in Go,
// and writes enriched fields back.
type EnrichListingWorker struct {
	river.WorkerDefaults[EnrichListingArgs]
	repo               *repository.Repository
	ollama             *llm.OllamaClient
	notifier           *alert.Notifier
	client             *http.Client
	insertComponentJob InsertComponentJobFn // optional; nil means component scoring disabled
}

func NewEnrichListingWorker(repo *repository.Repository, ollama *llm.OllamaClient, notifier *alert.Notifier) *EnrichListingWorker {
	return &EnrichListingWorker{
		repo:     repo,
		ollama:   ollama,
		notifier: notifier,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

// WithInsertComponentJobFn wires in the function used to queue component price scrape
// jobs for components not yet in the DB. Call this after creating the River client.
func (w *EnrichListingWorker) WithInsertComponentJobFn(fn InsertComponentJobFn) *EnrichListingWorker {
	w.insertComponentJob = fn
	return w
}

func (w *EnrichListingWorker) Work(ctx context.Context, job *river.Job[EnrichListingArgs]) error {
	listing, err := w.repo.Listing.GetByID(ctx, job.Args.ListingID)
	if err != nil {
		return fmt.Errorf("get listing %d: %w", job.Args.ListingID, err)
	}
	if listing == nil {
		return nil // not found — nothing to do
	}
	if listing.EnrichedAt != nil {
		return nil // already enriched — idempotent
	}

	// Fetch the full OLX listing page for richer LLM context.
	// Card snippets from search results lack descriptions and specs,
	// so the LLM often can't extract components or even the price.
	rawText, fetchErr := w.fetchListingPage(ctx, listing.URL)
	if fetchErr != nil {
		log.Printf("enrich: listing %d: fetch full page %s: %v — falling back to card snippet",
			job.Args.ListingID, listing.URL, fetchErr)
		rawText = w.cardText(listing)
	}

	text := llm.PreprocessText(rawText)

	// LLM extracts only concrete facts — no scoring, no market guesses.
	result, err := w.ollama.Extract(ctx, text)
	if err != nil {
		return fmt.Errorf("ollama extract listing %d: %w", job.Args.ListingID, err)
	}

	// All scoring is computed in Go from real market data.
	marketScore := w.computeMarketScore(ctx, result)
	scores := scoreListing(result, marketScore)

	if err := w.repo.Listing.UpdateEnrichment(ctx, listing.ID, result, scores); err != nil {
		return fmt.Errorf("update enrichment listing %d: %w", job.Args.ListingID, err)
	}

	if scores.DealScore >= 8 {
		w.notifier.Send(listing.ID, listing.Title, scores.DealScore,
			result.PriceAmount, result.PriceCurrency, listing.URL)
	}

	return nil
}

// fetchListingPage fetches the full HTML of an OLX listing page.
func (w *EnrichListingWorker) fetchListingPage(ctx context.Context, listingURL string) (string, error) {
	if listingURL == "" {
		return "", fmt.Errorf("empty URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listingURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept-Language", "bg,en;q=0.9")

	resp, err := w.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// cardText builds the best available text from the stored card snippet.
// Used as a fallback when the full page fetch fails.
func (w *EnrichListingWorker) cardText(listing *model.ListingRow) string {
	if listing.RawHTML != nil && *listing.RawHTML != "" {
		return *listing.RawHTML
	}
	var sb strings.Builder
	sb.WriteString(listing.Title)
	if listing.Description != nil {
		sb.WriteString(" ")
		sb.WriteString(*listing.Description)
	}
	if listing.RawPrice != nil {
		sb.WriteString(" Price: ")
		sb.WriteString(*listing.RawPrice)
	}
	return sb.String()
}

// scoreListing computes deal_score, deal_reasoning, is_suspicious, and suspicious_reason
// from real market data. No LLM involvement — deterministic, auditable.
func scoreListing(result *llm.ExtractionResult, marketScore *float64) model.EnrichedScores {
	scores := model.EnrichedScores{MarketScore: marketScore}

	if marketScore != nil && *marketScore > 0 {
		ratio := *marketScore // ask / sum(component market prices)

		switch {
		case ratio <= 0.50:
			scores.DealScore = 10
		case ratio <= 0.60:
			scores.DealScore = 9
		case ratio <= 0.70:
			scores.DealScore = 8
		case ratio <= 0.80:
			scores.DealScore = 7
		case ratio <= 0.90:
			scores.DealScore = 6
		case ratio <= 1.05:
			scores.DealScore = 5
		case ratio <= 1.20:
			scores.DealScore = 4
		case ratio <= 1.50:
			scores.DealScore = 3
		case ratio <= 2.00:
			scores.DealScore = 2
		default:
			scores.DealScore = 1
		}

		switch result.Condition {
		case "new", "open_box":
			scores.DealScore = min(10, scores.DealScore+1)
		case "fair":
			scores.DealScore = max(1, scores.DealScore-1)
		case "poor":
			scores.DealScore = max(1, scores.DealScore-2)
		case "damaged":
			scores.DealScore = max(1, scores.DealScore-3)
		}

		pct := int((1 - ratio) * 100)
		if pct > 0 {
			scores.DealReasoning = fmt.Sprintf(
				"Asking price is %d%% below market value for these components (%s condition)", pct, result.Condition)
		} else if pct < 0 {
			scores.DealReasoning = fmt.Sprintf(
				"Asking price is %d%% above market value for these components (%s condition)", -pct, result.Condition)
		} else {
			scores.DealReasoning = fmt.Sprintf(
				"Asking price matches market value for these components (%s condition)", result.Condition)
		}

		if ratio < 0.20 {
			scores.IsSuspicious = true
			reason := fmt.Sprintf("Price is only %.0f%% of typical market value — possible scam, missing parts, or stolen goods", ratio*100)
			scores.SuspiciousReason = &reason
		} else if ratio > 3.0 {
			scores.IsSuspicious = true
			reason := fmt.Sprintf("Price is %.1fx typical market value — extremely overpriced", ratio)
			scores.SuspiciousReason = &reason
		}
	} else {
		switch result.Condition {
		case "new", "open_box":
			scores.DealScore = 6
		case "like_new":
			scores.DealScore = 5
		case "good":
			scores.DealScore = 5
		case "fair":
			scores.DealScore = 4
		case "poor":
			scores.DealScore = 3
		case "damaged":
			scores.DealScore = 2
		default:
			scores.DealScore = 5
		}
		scores.DealReasoning = "No market price data available for comparison"
	}

	return scores
}

// eurToBGN is the legally fixed EUR→BGN exchange rate (Currency Board, since 1999).
const eurToBGN = 1.95583

// computeMarketScore looks up component prices and returns ask/sum_parts (both in BGN).
// Returns nil when no component prices are available.
// As a side effect, queues ScrapeComponentPriceArgs jobs for unknown components.
func (w *EnrichListingWorker) computeMarketScore(ctx context.Context, result *llm.ExtractionResult) *float64 {
	if len(result.Components) == 0 || result.PriceAmount <= 0 {
		return nil
	}

	// Normalize ask price to BGN. Component prices are always in BGN.
	// EUR→BGN is a fixed legal rate. Non-BGN/EUR currencies return nil
	// (no reliable rate to apply).
	askBGN := result.PriceAmount
	switch strings.ToUpper(result.PriceCurrency) {
	case "BGN", "":
		// already BGN
	case "EUR":
		askBGN = result.PriceAmount * eurToBGN
	default:
		return nil
	}

	var sumParts float64
	var matched int

	for _, comp := range result.Components {
		normalized := strings.ToLower(strings.TrimSpace(comp))
		cp, err := w.repo.ComponentPrice.GetByNormalizedName(ctx, normalized)
		if err != nil {
			log.Printf("enrich: component lookup %q: %v", comp, err)
			continue
		}
		if cp == nil {
			if w.insertComponentJob != nil {
				if err := w.insertComponentJob(ctx, ScrapeComponentPriceArgs{Name: comp}); err != nil {
					log.Printf("enrich: queue component job %q: %v", comp, err)
				}
			}
			continue
		}
		sumParts += cp.PriceAmount
		matched++
	}

	// Require all components to be priced. A partial sum produces a
	// definitively wrong score (ask vs. one component instead of the full
	// system), which is worse than returning nil and falling back to
	// condition-based scoring.
	if matched == 0 || matched < len(result.Components) || sumParts <= 0 {
		return nil
	}

	score := askBGN / sumParts
	return &score
}
