# TODOS

## P1

### Fix admin route registration bug
**What:** `internal/api/router/router.go:27-28` — `VerifyUser` and `GetUnverifiedUsers` are registered on
the root `e`, not on `adminGroup`. They're not protected by `AdminGuard`. Any user can call them.
**Why:** Security bug. Any authenticated user (not just admins) can verify/unverify other users.
**Fix:** Move to `adminGroup.POST("/verify", ...)` and `adminGroup.GET("/getUnverified", ...)`
**Effort:** XS | **Priority:** P1 | **Depends on:** nothing

### Write enrichment worker tests
**What:** Unit test for `EnrichListingWorker.Work()` with mocked Ollama. Test: happy path, invalid JSON retry,
listing not found (nil return), partial schema validation.
Also: dedup integration test (two INSERTs with same url_hash → 1 row), admin guard on re-enrich endpoint.
**Why:** Enrichment worker has 3+ failure modes. Zero tests means any of these will silently fail in production.
**Effort:** M (human: ~1 day / CC: ~20min) | **Priority:** P1 | **Depends on:** enrichment pipeline implementation

## P2

### Structured listing search
**What:** `GET /listings` with filter query params: `condition`, `price_max`, `price_min`, `category`,
`deal_score_min`, `page`, `limit`.
**Why:** The entire point of enrichment is queryability. Without filters, you just have a big JSON blob.
**Context:** Enrichment writes `deal_score`, `condition`, `category` to `listings` table. Add Postgres
index on these columns (`CREATE INDEX listings_condition_score ON listings(condition, deal_score)`) before
implementing to avoid full table scans.
**Effort:** M (human: ~1 day / CC: ~15min) | **Priority:** P2 | **Depends on:** enrichment pipeline

### Deal alerts
**What:** After enrichment, if `deal_score >= 8`, POST to a configurable webhook URL (`ALERT_WEBHOOK_URL` env var).
Support ntfy.sh format out of the box (simple POST with text body). Telegram and generic webhooks as fallback.
**Why:** The payoff for all the deal scoring work. Passive deal-finder.
**Context:** One HTTP call in `EnrichListingWorker.Work()` after the UPDATE succeeds. Fire-and-forget —
don't let a failed webhook retry block the enrichment job.
**Effort:** S (human: ~1h / CC: ~5min) | **Priority:** P2 | **Depends on:** enrichment pipeline

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
