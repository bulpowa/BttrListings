-- name: UpsertComponentPrice :exec
INSERT INTO component_prices (name, name_normalized, category, price_amount, price_currency, sample_count, scraped_at)
VALUES ($1, $2, $3, $4, $5, $6, NOW())
ON CONFLICT (name_normalized) DO UPDATE SET
    price_amount   = EXCLUDED.price_amount,
    price_currency = EXCLUDED.price_currency,
    sample_count   = EXCLUDED.sample_count,
    scraped_at     = NOW();

-- name: GetComponentPriceByNormalizedName :one
SELECT id, name, name_normalized, category, price_amount, price_currency, sample_count, scraped_at
FROM component_prices
WHERE name_normalized = $1;

-- name: ListStaleComponentPrices :many
SELECT id, name, name_normalized, category, price_amount, price_currency, sample_count, scraped_at
FROM component_prices
WHERE scraped_at < NOW() - make_interval(secs => $1);
