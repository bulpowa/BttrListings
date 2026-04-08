CREATE TABLE IF NOT EXISTS component_prices (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name            TEXT NOT NULL,
    name_normalized TEXT NOT NULL,
    category        TEXT NOT NULL DEFAULT '',
    price_amount    NUMERIC NOT NULL,
    price_currency  TEXT NOT NULL DEFAULT 'BGN',
    sample_count    INT NOT NULL DEFAULT 0,
    scraped_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(name_normalized)
);

CREATE INDEX IF NOT EXISTS component_prices_scraped_at ON component_prices(scraped_at);
CREATE INDEX IF NOT EXISTS component_prices_name_normalized ON component_prices(name_normalized);
