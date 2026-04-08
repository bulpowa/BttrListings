package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	sqlcDb "OlxScraper/internal/db"
	"OlxScraper/internal/llm"
	"OlxScraper/internal/model"

	"github.com/jackc/pgx/v5"
)

// ListingRepository handles all DB operations for the listings table.
type ListingRepository interface {
	Insert(ctx context.Context, input *model.CreateListingInput) (int64, error)
	GetByID(ctx context.Context, id int64) (*model.ListingRow, error)
	GetAll(ctx context.Context, limit, offset int) ([]*model.ListingRow, error)
	ResetEnrichment(ctx context.Context, id int64) error
	UpdateEnrichment(ctx context.Context, id int64, result *llm.ExtractionResult, scores model.EnrichedScores) error
}

type listingRepository struct {
	q *sqlcDb.Queries
}

func NewListingRepository(q *sqlcDb.Queries) ListingRepository {
	return &listingRepository{q: q}
}

// Insert inserts a new listing. Returns 0 if the url_hash already exists (idempotent).
func (r *listingRepository) Insert(ctx context.Context, input *model.CreateListingInput) (int64, error) {
	id, err := r.q.InsertListing(ctx, sqlcDb.InsertListingParams{
		Url:         input.URL,
		UrlHash:     input.URLHash,
		Title:       input.Title,
		Description: input.Description,
		RawPrice:    input.RawPrice,
		RawHtml:     input.RawHTML,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil // ON CONFLICT DO NOTHING — already exists
	}
	return id, err
}

// InsertListingTx inserts a listing inside an existing pgx transaction.
func InsertListingTx(ctx context.Context, tx pgx.Tx, input *model.CreateListingInput) (int64, error) {
	q := sqlcDb.New(tx)
	id, err := q.InsertListing(ctx, sqlcDb.InsertListingParams{
		Url:         input.URL,
		UrlHash:     input.URLHash,
		Title:       input.Title,
		Description: input.Description,
		RawPrice:    input.RawPrice,
		RawHtml:     input.RawHTML,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

func (r *listingRepository) GetByID(ctx context.Context, id int64) (*model.ListingRow, error) {
	row, err := r.q.GetListingByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return getListingByIDRowToModel(row), nil
}

func (r *listingRepository) GetAll(ctx context.Context, limit, offset int) ([]*model.ListingRow, error) {
	rows, err := r.q.GetListings(ctx, sqlcDb.GetListingsParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		return nil, err
	}
	listings := make([]*model.ListingRow, 0, len(rows))
	for _, row := range rows {
		listings = append(listings, getListingsRowToModel(row))
	}
	return listings, nil
}

func (r *listingRepository) ResetEnrichment(ctx context.Context, id int64) error {
	return r.q.ResetListingEnrichment(ctx, id)
}

func (r *listingRepository) UpdateEnrichment(ctx context.Context, id int64, result *llm.ExtractionResult, scores model.EnrichedScores) error {
	specsJSON, err := json.Marshal(result.Specs)
	if err != nil {
		specsJSON = []byte("{}")
	}
	dealScore := int32(scores.DealScore)
	priceAmount := result.PriceAmount
	return r.q.UpdateListingEnrichment(ctx, sqlcDb.UpdateListingEnrichmentParams{
		ID:               id,
		TitleNormalized:  &result.TitleNormalized,
		PriceAmount:      &priceAmount,
		PriceCurrency:    &result.PriceCurrency,
		Condition:        &result.Condition,
		Category:         &result.Category,
		LocationCity:     &result.LocationCity,
		Specs:            specsJSON,
		DealScore:        &dealScore,
		DealReasoning:    &scores.DealReasoning,
		IsSuspicious:     &scores.IsSuspicious,
		SuspiciousReason: scores.SuspiciousReason,
		MarketScore:      scores.MarketScore,
	})
}

func getListingByIDRowToModel(r sqlcDb.GetListingByIDRow) *model.ListingRow {
	return listingFieldsToModel(
		r.ID, r.Url, r.UrlHash, r.Title,
		r.Description, r.RawPrice, r.RawHtml, r.ScrapedAt,
		r.TitleNormalized, r.PriceAmount, r.PriceCurrency,
		r.Condition, r.Category, r.LocationCity, r.Specs,
		r.DealScore, r.DealReasoning, r.IsSuspicious, r.SuspiciousReason,
		r.MarketScore, r.EnrichmentStatus, r.EnrichedAt,
	)
}

func getListingsRowToModel(r sqlcDb.GetListingsRow) *model.ListingRow {
	return listingFieldsToModel(
		r.ID, r.Url, r.UrlHash, r.Title,
		r.Description, r.RawPrice, r.RawHtml, r.ScrapedAt,
		r.TitleNormalized, r.PriceAmount, r.PriceCurrency,
		r.Condition, r.Category, r.LocationCity, r.Specs,
		r.DealScore, r.DealReasoning, r.IsSuspicious, r.SuspiciousReason,
		r.MarketScore, r.EnrichmentStatus, r.EnrichedAt,
	)
}

// listingFieldsToModel maps individual sqlc row fields to model.ListingRow.
// scraped_at has DEFAULT NOW() but no NOT NULL constraint; falls back to time.Now().
func listingFieldsToModel(
	id int64, url, urlHash, title string,
	description, rawPrice, rawHTML *string,
	scrapedAt *time.Time,
	titleNormalized *string,
	priceAmount *float64,
	priceCurrency, condition, category, locationCity *string,
	specs []byte,
	dealScore *int32,
	dealReasoning *string,
	isSuspicious *bool,
	suspiciousReason *string,
	marketScore *float64,
	enrichmentStatus string,
	enrichedAt *time.Time,
) *model.ListingRow {
	sa := time.Now()
	if scrapedAt != nil {
		sa = *scrapedAt
	}
	return &model.ListingRow{
		ID:               id,
		URL:              url,
		URLHash:          urlHash,
		Title:            title,
		Description:      description,
		RawPrice:         rawPrice,
		RawHTML:          rawHTML,
		ScrapedAt:        sa,
		TitleNormalized:  titleNormalized,
		PriceAmount:      priceAmount,
		PriceCurrency:    priceCurrency,
		Condition:        condition,
		Category:         category,
		LocationCity:     locationCity,
		Specs:            specs,
		DealScore:        dealScore,
		DealReasoning:    dealReasoning,
		IsSuspicious:     isSuspicious,
		SuspiciousReason: suspiciousReason,
		MarketScore:      marketScore,
		EnrichmentStatus: enrichmentStatus,
		EnrichedAt:       enrichedAt,
	}
}
