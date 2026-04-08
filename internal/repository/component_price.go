package repository

import (
	"context"
	"errors"
	"time"

	sqlcDb "OlxScraper/internal/db"
	"OlxScraper/internal/model"

	"github.com/jackc/pgx/v5"
)

// ComponentPriceRepository handles DB operations for the component_prices table.
type ComponentPriceRepository interface {
	Upsert(ctx context.Context, name, nameNormalized, category string, priceAmount float64, currency string, sampleCount int32) error
	GetByNormalizedName(ctx context.Context, nameNormalized string) (*model.ComponentPrice, error)
	ListStale(ctx context.Context, olderThan time.Duration) ([]*model.ComponentPrice, error)
}

type componentPriceRepository struct {
	q *sqlcDb.Queries
}

func NewComponentPriceRepository(q *sqlcDb.Queries) ComponentPriceRepository {
	return &componentPriceRepository{q: q}
}

func (r *componentPriceRepository) Upsert(ctx context.Context, name, nameNormalized, category string, priceAmount float64, currency string, sampleCount int32) error {
	return r.q.UpsertComponentPrice(ctx, sqlcDb.UpsertComponentPriceParams{
		Name:           name,
		NameNormalized: nameNormalized,
		Category:       category,
		PriceAmount:    priceAmount,
		PriceCurrency:  currency,
		SampleCount:    sampleCount,
	})
}

func (r *componentPriceRepository) GetByNormalizedName(ctx context.Context, nameNormalized string) (*model.ComponentPrice, error) {
	cp, err := r.q.GetComponentPriceByNormalizedName(ctx, nameNormalized)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &model.ComponentPrice{
		ID:             cp.ID,
		Name:           cp.Name,
		NameNormalized: cp.NameNormalized,
		Category:       cp.Category,
		PriceAmount:    cp.PriceAmount,
		PriceCurrency:  cp.PriceCurrency,
		SampleCount:    cp.SampleCount,
		ScrapedAt:      cp.ScrapedAt,
	}, nil
}

func (r *componentPriceRepository) ListStale(ctx context.Context, olderThan time.Duration) ([]*model.ComponentPrice, error) {
	rows, err := r.q.ListStaleComponentPrices(ctx, olderThan.Seconds())
	if err != nil {
		return nil, err
	}
	results := make([]*model.ComponentPrice, 0, len(rows))
	for _, cp := range rows {
		results = append(results, &model.ComponentPrice{
			ID:             cp.ID,
			Name:           cp.Name,
			NameNormalized: cp.NameNormalized,
			Category:       cp.Category,
			PriceAmount:    cp.PriceAmount,
			PriceCurrency:  cp.PriceCurrency,
			SampleCount:    cp.SampleCount,
			ScrapedAt:      cp.ScrapedAt,
		})
	}
	return results, nil
}
