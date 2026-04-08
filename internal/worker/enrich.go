package worker

import (
	"context"
	"fmt"
	"log"
	"strings"

	"OlxScraper/internal/alert"
	"OlxScraper/internal/llm"
	"OlxScraper/internal/model"
	"OlxScraper/internal/repository"

	"github.com/riverqueue/river"
)

// EnrichListingArgs is the River job payload for listing enrichment.
type EnrichListingArgs struct {
	ListingID int64 `json:"listing_id"`
}

func (EnrichListingArgs) Kind() string { return "enrich_listing" }

// EnrichListingWorker loads a raw listing, calls the LLM for fact extraction,
// computes market-based scores in Go, and writes enriched fields back.
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

// scoreListing computes deal_score, deal_reasoning, is_suspicious, and suspicious_reason
// from real market data. No LLM involvement — deterministic, auditable.
func scoreListing(result *llm.ExtractionResult, marketScore *float64) model.EnrichedScores {
	scores := model.EnrichedScores{MarketScore: marketScore}

	if marketScore != nil && *marketScore > 0 {
		ratio := *marketScore // ask / sum(component market prices)

		// Base score from price ratio.
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

		// Condition modifier.
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

		// Reasoning.
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

		// Suspicious: under 20% of market or over 3x market.
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
		// No market data — score by condition alone.
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
