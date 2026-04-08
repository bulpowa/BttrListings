package model

import "time"

// ListingRow is the raw DB record returned by pgx scans.
type ListingRow struct {
	ID               int64
	URL              string
	URLHash          string
	Title            string
	Description      *string
	RawPrice         *string
	RawHTML          *string
	ScrapedAt        time.Time
	TitleNormalized  *string
	PriceAmount      *float64
	PriceCurrency    *string
	Condition        *string
	Category         *string
	LocationCity     *string
	Specs            []byte // JSONB raw bytes
	DealScore        *int32
	DealReasoning    *string
	IsSuspicious     *bool
	SuspiciousReason *string
	MarketScore      *float64
	EnrichmentStatus string
	EnrichedAt       *time.Time
}

// Listing is the API response shape for a listing.
type Listing struct {
	ID               int64             `json:"id"`
	URL              string            `json:"url"`
	Title            string            `json:"title"`
	Description      *string           `json:"description,omitempty"`
	RawPrice         *string           `json:"raw_price,omitempty"`
	ScrapedAt        time.Time         `json:"scraped_at"`
	TitleNormalized  *string           `json:"title_normalized,omitempty"`
	PriceAmount      *float64          `json:"price_amount,omitempty"`
	PriceCurrency    *string           `json:"price_currency,omitempty"`
	Condition        *string           `json:"condition,omitempty"`
	Category         *string           `json:"category,omitempty"`
	LocationCity     *string           `json:"location_city,omitempty"`
	Specs            map[string]string `json:"specs,omitempty"`
	DealScore        *int32            `json:"deal_score,omitempty"`
	DealReasoning    *string           `json:"deal_reasoning,omitempty"`
	IsSuspicious     *bool             `json:"is_suspicious,omitempty"`
	SuspiciousReason *string           `json:"suspicious_reason,omitempty"`
	MarketScore      *float64          `json:"market_score,omitempty"`
	EnrichmentStatus string            `json:"enrichment_status"`
	EnrichedAt       *time.Time        `json:"enriched_at,omitempty"`
}

// CreateListingInput is the input for inserting a raw scraped listing.
type CreateListingInput struct {
	URL         string
	URLHash     string
	Title       string
	Description *string
	RawPrice    *string
	RawHTML     *string
}
