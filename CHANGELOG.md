# Changelog

All notable changes to BttrListings are documented here.

## [0.1.0.1] - 2026-04-07

### Added
- Unit tests for `EnrichListingWorker.Work()`: 7 cases covering happy path, listing not found, already enriched, LLM error, invalid JSON retry (verifies 2-attempt behaviour), DB error, and fallback from `raw_html` to title+description+price
- Handler tests for listing endpoints and admin guard regression: 9 cases covering `GET /listings`, `GET /listings/:id` (OK, invalid ID, not found), and `POST /admin/listings/:id/re-enrich` (no token → 401, user token → 403, admin token → 202/404/500)

## [0.1.0.0] - 2026-04-07

### Added
- LLM enrichment pipeline: every scraped listing is enriched by a local Ollama model with structured fields (condition, price, category, location, deal score 1-10, suspicious flag)
- PostgreSQL backend replacing SQLite — migrations for all five existing tables plus a new `listings` table with enrichment columns
- River job queue for background enrichment — 2 concurrent workers, 25-retry backoff, River UI dashboard available
- `OllamaClient` with 2-level retry (Ollama-level + River-level) and automatic text pre-processing (HTML strip, 8000-char truncation)
- Atomic scraper → River insert: listing insert and enrichment job enqueue happen in a single Postgres transaction
- `GET /listings` and `GET /listings/:id` — returns enriched listings with all structured fields
- `POST /admin/listings/:id/re-enrich` — admin endpoint to re-queue any listing for re-enrichment
- `/health` now reports Ollama reachability alongside server status
- `OLLAMA_HOST` and `OLLAMA_MODEL` env vars for two-machine setup (API + Postgres on Machine A, Ollama GPU on Machine B)
- Graceful shutdown: SIGTERM drains in-progress River jobs before exit

### Fixed
- Admin route security bug: `/verify` and `/getUnverified` were registered on the root router instead of `adminGroup`, allowing any authenticated user to call them

### Changed
- Migrated from SQLite to PostgreSQL — `DB_FILE` env var replaced by `DATABASE_URL`
- All SQL queries updated from `?` to `$N` positional parameters
- `CreateUser` now uses `RETURNING id` instead of `LastInsertId()` (not supported in Postgres)
