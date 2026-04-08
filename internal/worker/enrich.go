package worker

import (
	"context"
	"fmt"
	"log"
	"strings"

	"OlxScraper/internal/alert"
	"OlxScraper/internal/llm"
	"OlxScraper/internal/repository"

	"github.com/riverqueue/river"
)

// EnrichListingArgs is the River job payload for listing enrichment.
type EnrichListingArgs struct {
	ListingID int64 `json:"listing_id"`
}

func (EnrichListingArgs) Kind() string { return "enrich_listing" }

// EnrichListingWorker loads a raw listing, calls Ollama, computes market_score,
// and writes enriched fields back.
type EnrichListingWorker struct {
	river.WorkerDefaults[EnrichListingArgs]
	repo               *repository.Repository
	ollama             *llm.OllamaClient
	notifier           *alert.Notifier
	insertComponentJob InsertComponentJobFn // optional; nil means component scoring disabled
}

func NewEnrichListingWorker(repo *repository.Repository, ollama *llm.OllamaClient, notifier *alert.Notifier) *EnrichListingWorker {
	return &EnrichListingWorker{repo: repo, ollama: ollama, notifier: notifier}
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

	// Build raw text for the LLM from whatever we have.
	rawText := ""
	if listing.RawHTML != nil && *listing.RawHTML != "" {
		rawText = *listing.RawHTML
	} else {
		rawText = listing.Title
		if listing.Description != nil {
			rawText += " " + *listing.Description
		}
		if listing.RawPrice != nil {
			rawText += " Price: " + *listing.RawPrice
		}
	}

	text := llm.PreprocessText(rawText)

	result, err := w.ollama.Extract(ctx, text)
	if err != nil {
		return fmt.Errorf("ollama extract listing %d: %w", job.Args.ListingID, err)
	}

	marketScore := w.computeMarketScore(ctx, result)

	if err := w.repo.Listing.UpdateEnrichment(ctx, listing.ID, result, marketScore); err != nil {
		return fmt.Errorf("update enrichment listing %d: %w", job.Args.ListingID, err)
	}

	if isDeal(result.DealScore, marketScore) {
		w.notifier.Send(listing.ID, listing.Title, result.DealScore,
			result.PriceAmount, result.PriceCurrency, listing.URL)
	}

	return nil
}

// computeMarketScore looks up component prices and returns ask/sum_parts.
// Returns nil when no component prices are available.
// As a side effect, queues ScrapeComponentPriceArgs jobs for unknown components.
func (w *EnrichListingWorker) computeMarketScore(ctx context.Context, result *llm.ExtractionResult) *float64 {
	if len(result.Components) == 0 || result.PriceAmount <= 0 {
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
			// Not in DB — queue a scrape job if we have the function wired in.
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

	if matched == 0 || sumParts <= 0 {
		return nil
	}

	score := result.PriceAmount / sumParts
	return &score
}

// isDeal returns true when a listing qualifies as a deal worth alerting on.
// A listing is a deal if:
//   - deal_score >= 8 (LLM subjective quality score), OR
//   - market_score <= 0.65 (asking ≤65% of the sum of component market prices)
func isDeal(dealScore int, marketScore *float64) bool {
	if dealScore >= 8 {
		return true
	}
	if marketScore != nil && *marketScore <= 0.65 {
		return true
	}
	return false
}
