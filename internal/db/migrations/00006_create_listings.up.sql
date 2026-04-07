CREATE TABLE IF NOT EXISTS listings (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    url TEXT NOT NULL,
    url_hash TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL,
    description TEXT,
    raw_price TEXT,
    raw_html TEXT,
    scraped_at TIMESTAMPTZ DEFAULT NOW(),

    -- enrichment fields
    title_normalized TEXT,
    price_amount NUMERIC,
    price_currency TEXT,
    condition TEXT,
    category TEXT,
    location_city TEXT,
    specs JSONB,
    deal_score INT,
    deal_reasoning TEXT,
    is_suspicious BOOLEAN,
    suspicious_reason TEXT,
    enrichment_status TEXT NOT NULL DEFAULT 'pending',
    enriched_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS listings_enrichment_status ON listings(enrichment_status);
CREATE INDEX IF NOT EXISTS listings_deal_score ON listings(deal_score);
