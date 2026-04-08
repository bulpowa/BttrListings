# TODOS

## P1

### Component price infrastructure
**What:** New `component_prices` table: `(id, name, category, price_amount, price_currency, source_url,
url_hash, scraped_at)`. A new `ComponentPriceScraper` runs as a scheduled River job (every 6h) for a
seed list of target items (e.g. "RTX 3060", "iPhone 13", "MacBook Air M1"). It searches OLX with
category + keyword, takes the median of the top 10 listings, and upserts into `component_prices`.
Seed list configured via env var `COMPONENT_SEEDS` (comma-separated "name:category" pairs) or a
`component_seeds.json` file.
**Why:** Without real market prices, deal_score is an LLM guess. This is the data foundation that makes
every alert trustworthy. Seed it before you start scraping listings and day-1 data already has context.
**Effort:** L (human: ~2 days / CC: ~30min) | **Priority:** P1 | **Depends on:** nothing

### Market-based deal score
**What:** After component price data exists, update the enrichment worker to compute a second score:
`market_score = ask_price / median_component_price` for the matched component. Store as `market_score`
column (float, nullable). Update the alert threshold to fire when either `deal_score >= 8` OR
`market_score <= 0.65` (asking ≤65% of market). Update the dashboard to show market_score alongside
deal_score. LLM deal_score stays as a fallback for listings with no matched component price.
**Why:** Objective, data-driven. "This GPU is 58% of current OLX median" is a fact, not a vibe.
Reduces false-positive alerts dramatically. Critical before going public.
**Effort:** M (human: ~1 day / CC: ~20min) | **Priority:** P1 | **Depends on:** component price infrastructure


## P2

### Structured listing search
**What:** `GET /listings` with filter query params: `condition`, `price_max`, `price_min`, `category`,
`deal_score_min`, `page`, `limit`.
**Why:** The entire point of enrichment is queryability. Without filters, you just have a big JSON blob.
Required for a public-facing product.
**Context:** Enrichment writes `deal_score`, `condition`, `category` to `listings` table. Add Postgres
index on these columns (`CREATE INDEX listings_condition_score ON listings(condition, deal_score)`) before
implementing to avoid full table scans.
**Effort:** M (human: ~1 day / CC: ~15min) | **Priority:** P2 | **Depends on:** enrichment pipeline


## P3

### Price history tracking
**What:** Track price changes for the same listing over time. If the scraper sees a listing that already exists
but with a different price, insert a row into `listing_price_history` instead of ignoring the update.
**Why:** Know when a seller drops their price. Real signal for a good deal timing.
**Effort:** M | **Priority:** P3 | **Depends on:** dedup strategy (url_hash)

### Natural language listing search
**What:** "find me a GPU under 150 euros, good condition, listed this week" → structured query.
Use the same local Ollama instance to parse the query into filter params, then hit the structured search endpoint.
**Why:** The 10x version of search. Makes the whole system feel magical.
**Effort:** L | **Priority:** P3 | **Depends on:** structured listing search

## Completed

### Deal alerts
**What:** After enrichment, if `deal_score >= 8`, POST to `ALERT_WEBHOOK_URL` env var. ntfy.sh compatible
(Title/Priority/Tags headers). Fire-and-forget goroutine — never blocks the enrichment job. No-op when
`ALERT_WEBHOOK_URL` is empty. 4 tests in `internal/alert/alert_test.go`.
**Completed:** v0.2.0.0 (2026-04-07)

### Write enrichment worker tests
**What:** 7 unit tests for `EnrichListingWorker.Work()` (happy path, not found, already enriched, LLM error,
invalid JSON retry, DB error, fallback to title). 9 handler tests covering listings endpoints and admin guard
(no token → 401, user token → 403, admin token → 202, not found → 404, service error → 500).
**Completed:** v0.2.0.0 (2026-04-07)

### Fix admin route registration bug
**What:** `VerifyUser` and `GetUnverifiedUsers` moved onto `adminGroup` behind `AdminGuard`.
**Completed:** v0.1.0.0 (2026-04-07)
