# TODOS

## P2

### Structured listing search
**What:** `GET /listings` with filter query params: `condition`, `price_max`, `price_min`, `category`,
`deal_score_min`, `page`, `limit`.
**Why:** The entire point of enrichment is queryability. Without filters, you just have a big JSON blob.
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
