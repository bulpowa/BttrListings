-- name: InsertListing :one
INSERT INTO listings (url, url_hash, title, description, raw_price, raw_html)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (url_hash) DO NOTHING
RETURNING id;

-- name: GetListingByID :one
SELECT id, url, url_hash, title, description, raw_price, raw_html, scraped_at,
       title_normalized, price_amount, price_currency, condition, category, location_city,
       specs, deal_score, deal_reasoning, is_suspicious, suspicious_reason,
       market_score, enrichment_status, enriched_at
FROM listings
WHERE id = $1;

-- name: GetListings :many
SELECT id, url, url_hash, title, description, raw_price, raw_html, scraped_at,
       title_normalized, price_amount, price_currency, condition, category, location_city,
       specs, deal_score, deal_reasoning, is_suspicious, suspicious_reason,
       market_score, enrichment_status, enriched_at
FROM listings
ORDER BY scraped_at DESC
LIMIT $1 OFFSET $2;

-- name: ResetListingEnrichment :exec
UPDATE listings
SET enriched_at = NULL, enrichment_status = 'pending'
WHERE id = $1;

-- name: UpdateListingEnrichment :exec
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
WHERE id = $1 AND enriched_at IS NULL;
