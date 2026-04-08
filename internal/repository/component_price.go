package repository

import (
	"context"
	"time"

	"OlxScraper/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ComponentPriceRepository handles DB operations for the component_prices table.
type ComponentPriceRepository interface {
	Upsert(ctx context.Context, name, nameNormalized, category string, priceAmount float64, currency string, sampleCount int) error
	GetByNormalizedName(ctx context.Context, nameNormalized string) (*model.ComponentPrice, error)
	ListStale(ctx context.Context, olderThan time.Duration) ([]*model.ComponentPrice, error)
}

type componentPriceRepository struct {
	pool *pgxpool.Pool
}

func NewComponentPriceRepository(pool *pgxpool.Pool) ComponentPriceRepository {
	return &componentPriceRepository{pool: pool}
}

// Upsert inserts or updates a component price by normalized name.
func (r *componentPriceRepository) Upsert(ctx context.Context, name, nameNormalized, category string, priceAmount float64, currency string, sampleCount int) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO component_prices (name, name_normalized, category, price_amount, price_currency, sample_count, scraped_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
		ON CONFLICT (name_normalized) DO UPDATE SET
			price_amount  = EXCLUDED.price_amount,
			price_currency = EXCLUDED.price_currency,
			sample_count  = EXCLUDED.sample_count,
			scraped_at    = NOW()
	`, name, nameNormalized, category, priceAmount, currency, sampleCount)
	return err
}

// GetByNormalizedName returns a component price row or nil if not found.
func (r *componentPriceRepository) GetByNormalizedName(ctx context.Context, nameNormalized string) (*model.ComponentPrice, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, name, name_normalized, category, price_amount, price_currency, sample_count, scraped_at
		FROM component_prices
		WHERE name_normalized = $1
	`, nameNormalized)

	var cp model.ComponentPrice
	err := row.Scan(&cp.ID, &cp.Name, &cp.NameNormalized, &cp.Category,
		&cp.PriceAmount, &cp.PriceCurrency, &cp.SampleCount, &cp.ScrapedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cp, nil
}

// ListStale returns all component prices last scraped longer ago than olderThan.
func (r *componentPriceRepository) ListStale(ctx context.Context, olderThan time.Duration) ([]*model.ComponentPrice, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, name, name_normalized, category, price_amount, price_currency, sample_count, scraped_at
		FROM component_prices
		WHERE scraped_at < NOW() - $1::interval
	`, olderThan.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*model.ComponentPrice
	for rows.Next() {
		var cp model.ComponentPrice
		if err := rows.Scan(&cp.ID, &cp.Name, &cp.NameNormalized, &cp.Category,
			&cp.PriceAmount, &cp.PriceCurrency, &cp.SampleCount, &cp.ScrapedAt); err != nil {
			return nil, err
		}
		results = append(results, &cp)
	}
	return results, rows.Err()
}
