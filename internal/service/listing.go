package service

import (
	"context"
	"encoding/json"

	"OlxScraper/internal/model"
	"OlxScraper/internal/repository"
)

// EnqueueFn enqueues a River enrichment job for the given listing ID.
// Defined as a function type to keep the service layer free of River imports.
type EnqueueFn func(ctx context.Context, listingID int64) error

// ListingService exposes listing read and re-enrich operations for the HTTP layer.
type ListingService interface {
	GetListings(ctx context.Context, limit, offset int) ([]*model.Listing, error)
	GetListingByID(ctx context.Context, id int64) (*model.Listing, error)
	ReEnrich(ctx context.Context, id int64) error
}

type listingService struct {
	repo    *repository.Repository
	enqueue EnqueueFn
}

func NewListingService(repo *repository.Repository, enqueue EnqueueFn) ListingService {
	return &listingService{repo: repo, enqueue: enqueue}
}

func (s *listingService) GetListings(ctx context.Context, limit, offset int) ([]*model.Listing, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.repo.Listing.GetAll(ctx, limit, offset)
	if err != nil {
		return nil, err
	}
	listings := make([]*model.Listing, 0, len(rows))
	for _, r := range rows {
		listings = append(listings, toListingResponse(r))
	}
	return listings, nil
}

func (s *listingService) GetListingByID(ctx context.Context, id int64) (*model.Listing, error) {
	row, err := s.repo.Listing.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	return toListingResponse(row), nil
}

func (s *listingService) ReEnrich(ctx context.Context, id int64) error {
	// Verify listing exists before enqueuing.
	row, err := s.repo.Listing.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if row == nil {
		return ErrListingNotFound
	}
	// Clear enriched_at so the worker doesn't skip it as already done.
	if err := s.repo.Listing.ResetEnrichment(ctx, id); err != nil {
		return err
	}
	return s.enqueue(ctx, id)
}

// toListingResponse maps a raw DB row to the API response model.
func toListingResponse(r *model.ListingRow) *model.Listing {
	l := &model.Listing{
		ID:               r.ID,
		URL:              r.URL,
		Title:            r.Title,
		Description:      r.Description,
		RawPrice:         r.RawPrice,
		ScrapedAt:        r.ScrapedAt,
		TitleNormalized:  r.TitleNormalized,
		PriceAmount:      r.PriceAmount,
		PriceCurrency:    r.PriceCurrency,
		Condition:        r.Condition,
		Category:         r.Category,
		LocationCity:     r.LocationCity,
		DealScore:        r.DealScore,
		DealReasoning:    r.DealReasoning,
		IsSuspicious:     r.IsSuspicious,
		SuspiciousReason: r.SuspiciousReason,
		MarketScore:      r.MarketScore,
		EnrichmentStatus: r.EnrichmentStatus,
		EnrichedAt:       r.EnrichedAt,
	}
	if len(r.Specs) > 0 && string(r.Specs) != "null" {
		_ = json.Unmarshal(r.Specs, &l.Specs)
	}
	return l
}
