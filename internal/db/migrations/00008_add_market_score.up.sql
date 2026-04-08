ALTER TABLE listings ADD COLUMN IF NOT EXISTS market_score NUMERIC;

CREATE INDEX IF NOT EXISTS listings_market_score ON listings(market_score);
