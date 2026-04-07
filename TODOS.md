# TODOS

## P1

### Component price infrastructure (foundation for market-based scoring)
**What:** New `component_prices` table (component_name, avg_price, min_price, sample_count, condition, updated_at). New River job type `ScrapeComponentPricesArgs` that takes a search term, hits OLX, aggregates prices from results, and upserts into the table. Seed with ~50 common electronics search terms (RTX 5060, Ryzen 7500F, iPhone 13, etc.). Schedule hourly via River's cron or a ticker in main.go.
**Why:** Everything in the component pricing vision depends on this table existing. Without live market data, deal_score is pure LLM guesswork — poorly calibrated for BGN and stale. This is the foundation.
**Effort:** M (human: ~1 week / CC: ~45 min) | **Priority:** P1 | **Depends on:** nothing

### LLM component extraction (extend enrichment schema)
**What:** Extend `systemPrompt` in `internal/llm/ollama.go` to output a `components` field: `[{"name": "RTX 5060", "quantity": 1}, {"name": "Ryzen 7500F", "quantity": 1}]`. Add `Components []ComponentItem` to `ExtractionResult`. Add `components JSONB` column to `listings` table (migration). Store extracted components in `UpdateEnrichment`.
**Why:** The LLM already parses listing text — extracting a component list is a small prompt change. This feeds the market score calculation without any extra LLM calls.
**Effort:** S (human: ~2h / CC: ~15 min) | **Priority:** P1 | **Depends on:** component price infrastructure

### Market-based deal score
**What:** In `EnrichListingWorker.Work()`, after LLM extracts components, look up each component in `component_prices` and sum the market prices. Compute `market_delta = (sum_parts - ask_price) / sum_parts`. Store as `market_delta NUMERIC` on the listing. Override `deal_score` with a formula derived from `market_delta` when data is available (e.g. delta > 0.20 → score 9, delta > 0.10 → score 7). Fall back to LLM heuristic score when no component data exists.
**Why:** Replaces "LLM vibes" with verifiable math. A listing 20% below sum of parts is a real signal. This is the primary user value of the whole feature.
**Effort:** S (human: ~3h / CC: ~15 min) | **Priority:** P1 | **Depends on:** LLM component extraction, component price infrastructure

## P2

### Structured listing search
**What:** `GET /listings` with filter query params: `condition`, `price_max`, `price_min`, `category`,
`deal_score_min`, `page`, `limit`.
**Why:** The entire point of enrichment is queryability. Without filters, you just have a big JSON blob.
**Context:** Enrichment writes `deal_score`, `condition`, `category` to `listings` table. Add Postgres
index on these columns (`CREATE INDEX listings_condition_score ON listings(condition, deal_score)`) before
implementing to avoid full table scans.
**Effort:** M (human: ~1 day / CC: ~15min) | **Priority:** P2 | **Depends on:** enrichment pipeline


## P2 (component pricing — phase 2)

### Cross-listing rank
**What:** For simple (non-bundle) listings, after enrichment normalize the title to a canonical form (LLM: "what product is this exactly?" → "iPhone 13 128GB"). Count how many active listings on OLX have the same canonical product. Rank this listing by price among them. Store `canonical_product TEXT`, `market_rank INT`, `market_total INT` on the listing. Show in UI: "3rd cheapest of 14 iPhone 13 128GB listings."
**Why:** Most compelling UX signal. "3rd cheapest of 14" is more actionable than "score: 7."
**Effort:** M (human: ~2 days / CC: ~20 min) | **Priority:** P2 | **Depends on:** component price infrastructure (for category-scoped search)

### Price trend per component
**What:** Instead of overwriting `component_prices`, append rows to a `component_price_history` table (component_name, avg_price, sample_count, condition, scraped_at). Show trend in UI: "RTX 5060 avg down 8% in last 30 days." ASCII sparkline in dashboard.
**Why:** Timing signal — a falling component price means "wait." A rising one means "buy now." Extends the existing Price history tracking idea (P3 below) to components specifically.
**Effort:** M (human: ~1 day / CC: ~15 min) | **Priority:** P2 | **Depends on:** component price infrastructure

### Component-aware deal alerts
**What:** Extend `internal/alert/alert.go` to fire when `market_delta > 0.15` (15% below sum of parts) instead of (or in addition to) `deal_score >= 8`. Alert body includes: ask price, sum-of-parts, delta %, and component breakdown.
**Why:** Current alerts fire on LLM score. Market-delta alerts are far more precise — they only fire when math confirms the deal, not LLM vibes.
**Effort:** S (human: ~2h / CC: ~10 min) | **Priority:** P2 | **Depends on:** market-based deal score

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
