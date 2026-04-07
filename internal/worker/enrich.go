package worker

import (
	"context"
	"fmt"

	"OlxScraper/internal/llm"
	"OlxScraper/internal/repository"

	"github.com/riverqueue/river"
)

// EnrichListingArgs is the River job payload for listing enrichment.
type EnrichListingArgs struct {
	ListingID int64 `json:"listing_id"`
}

func (EnrichListingArgs) Kind() string { return "enrich_listing" }

// EnrichListingWorker loads a raw listing, calls Ollama, and writes enriched fields back.
type EnrichListingWorker struct {
	river.WorkerDefaults[EnrichListingArgs]
	repo   *repository.Repository
	ollama *llm.OllamaClient
}

func NewEnrichListingWorker(repo *repository.Repository, ollama *llm.OllamaClient) *EnrichListingWorker {
	return &EnrichListingWorker{repo: repo, ollama: ollama}
}

func (w *EnrichListingWorker) Work(ctx context.Context, job *river.Job[EnrichListingArgs]) error {
	listing, err := w.repo.Listing.GetByID(ctx, job.Args.ListingID)
	if err != nil {
		return fmt.Errorf("get listing %d: %w", job.Args.ListingID, err)
	}
	if listing == nil {
		// Listing not found — nothing to do. Return nil so River marks job done.
		return nil
	}
	if listing.EnrichedAt != nil {
		// Already enriched — idempotent, skip.
		return nil
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
		// River retries up to 25x with exponential backoff.
		return fmt.Errorf("ollama extract listing %d: %w", job.Args.ListingID, err)
	}

	if err := w.repo.Listing.UpdateEnrichment(ctx, listing.ID, result); err != nil {
		return fmt.Errorf("update enrichment listing %d: %w", job.Args.ListingID, err)
	}

	return nil
}
