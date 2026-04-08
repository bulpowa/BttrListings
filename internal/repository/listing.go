package repository

import (
	"context"
	"encoding/json"

	"OlxScraper/internal/llm"
	"OlxScraper/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ListingRepository handles all DB operations for the listings table.
type ListingRepository interface {
	Insert(ctx context.Context, input *model.CreateListingInput) (int64, error)
	GetByID(ctx context.Context, id int64) (*model.ListingRow, error)
	GetAll(ctx context.Context, limit, offset int) ([]*model.ListingRow, error)
	UpdateEnrichment(ctx context.Context, id int64, result *llm.ExtractionResult, marketScore *float64) error
}

type listingRepository struct {
	pool *pgxpool.Pool
}

func NewListingRepository(pool *pgxpool.Pool) ListingRepository {
	return &listingRepository{pool: pool}
}

// Insert inserts a new listing atomically. Returns 0 if the url_hash already exists.
func (r *listingRepository) Insert(ctx context.Context, input *model.CreateListingInput) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO listings (url, url_hash, title, description, raw_price, raw_html)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (url_hash) DO NOTHING
		RETURNING id
	`, input.URL, input.URLHash, input.Title, input.Description,
		input.RawPrice, input.RawHTML).Scan(&id)
	if err == pgx.ErrNoRows {
		return 0, nil // already exists — idempotent
	}
	return id, err
}

// InsertTx inserts a listing inside an existing pgx transaction (for atomic scraper inserts).
func InsertListingTx(ctx context.Context, tx pgx.Tx, input *model.CreateListingInput) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `
		INSERT INTO listings (url, url_hash, title, description, raw_price, raw_html)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (url_hash) DO NOTHING
		RETURNING id
	`, input.URL, input.URLHash, input.Title, input.Description,
		input.RawPrice, input.RawHTML).Scan(&id)
	if err == pgx.ErrNoRows {
		return 0, nil
	}
	return id, err
}

func (r *listingRepository) GetByID(ctx context.Context, id int64) (*model.ListingRow, error) {
	row := r.pool.QueryRow(ctx, listingSelectCols+` WHERE id = $1`, id)
	return scanListing(row)
}

func (r *listingRepository) GetAll(ctx context.Context, limit, offset int) ([]*model.ListingRow, error) {
	rows, err := r.pool.Query(ctx, listingSelectCols+` ORDER BY scraped_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var listings []*model.ListingRow
	for rows.Next() {
		l, err := scanListing(rows)
		if err != nil {
			return nil, err
		}
		listings = append(listings, l)
	}
	return listings, rows.Err()
}

func (r *listingRepository) UpdateEnrichment(ctx context.Context, id int64, result *llm.ExtractionResult, marketScore *float64) error {
	specsJSON, err := json.Marshal(result.Specs)
	if err != nil {
		specsJSON = []byte("{}")
	}

	_, err = r.pool.Exec(ctx, `
		UPDATE listings SET
			title_normalized  = $2,
			price_amount      = $3,
			price_currency    = $4,
			condition         = $5,
			category          = $6,
			location_city     = $7,
			specs             = $8,
			deal_score        = $9,
			deal_reasoning    = $10,
			is_suspicious     = $11,
			suspicious_reason = $12,
			market_score      = $13,
			enrichment_status = 'done',
			enriched_at       = NOW()
		WHERE id = $1 AND enriched_at IS NULL
	`, id,
		result.TitleNormalized, result.PriceAmount, result.PriceCurrency,
		result.Condition, result.Category, result.LocationCity,
		specsJSON, result.DealScore, result.DealReasoning,
		result.IsSuspicious, result.SuspiciousReason, marketScore)
	return err
}

// listingSelectCols is the full SELECT column list for listing rows.
const listingSelectCols = `
SELECT id, url, url_hash, title, description, raw_price, raw_html, scraped_at,
       title_normalized, price_amount, price_currency, condition, category, location_city,
       specs, deal_score, deal_reasoning, is_suspicious, suspicious_reason,
       market_score, enrichment_status, enriched_at
FROM listings`

type scannable interface {
	Scan(dest ...any) error
}

func scanListing(row scannable) (*model.ListingRow, error) {
	var l model.ListingRow
	err := row.Scan(
		&l.ID, &l.URL, &l.URLHash, &l.Title, &l.Description, &l.RawPrice, &l.RawHTML,
		&l.ScrapedAt, &l.TitleNormalized, &l.PriceAmount, &l.PriceCurrency,
		&l.Condition, &l.Category, &l.LocationCity, &l.Specs, &l.DealScore,
		&l.DealReasoning, &l.IsSuspicious, &l.SuspiciousReason,
		&l.MarketScore, &l.EnrichmentStatus, &l.EnrichedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &l, nil
}
