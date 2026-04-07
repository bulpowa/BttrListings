# BttrListings

Scrapes OLX marketplace listings, enriches them with a local LLM (deal score, condition, price, category), and exposes a queryable API.

## Two-machine setup

```
Machine A (any PC)          Machine B (GPU PC — 16GB VRAM)
─────────────────────       ──────────────────────────────
Postgres                    Ollama
API server                  gemma4:27b model
Scraper (goroutine)
River workers
```

The scraper runs on Machine A. It fetches OLX pages, inserts listings into Postgres, and enqueues enrichment jobs. River workers on Machine A call Ollama on Machine B over the local network.

---

## Machine B — Ollama (GPU PC)

Install Ollama: https://ollama.com/download

```bash
ollama serve
ollama pull gemma4:27b
```

Make sure port `11434` is open on Machine B's firewall so Machine A can reach it.

---

## Machine A — API + Scraper

### 1. Clone and configure

```bash
git clone https://github.com/bulpowa/BttrListings.git
cd BttrListings
cp .env.example .env
```

Edit `.env`:

```env
POSTGRES_PASSWORD=pick-a-password
JWT_SECRET=run-openssl-rand-hex-32-and-paste-here

# IP of Machine B (your GPU PC)
OLLAMA_HOST=http://192.168.1.50:11434
OLLAMA_MODEL=gemma4:27b

# OLX search pages to scrape — comma-separated
# Find these by searching OLX and copying the URL from the results page
SCRAPER_URLS=https://www.olx.pl/elektronika/komputery/,https://www.olx.pl/elektronika/telefony/
```

### 2. Start

```bash
docker compose up -d
```

This starts Postgres and the API. On first run, all database migrations apply automatically.

### 3. Verify

```bash
# Should return {"status":"ok","ollama":"ok"}
curl http://localhost:8080/health

# Listings appear here after the first scrape (runs on startup, then every 15 min)
curl http://localhost:8080/listings
```

---

## Finding OLX search URLs

1. Go to olx.pl (or your country's OLX)
2. Search for a category — e.g. "laptopy" or "GPU"
3. Copy the URL from the address bar
4. Paste it into `SCRAPER_URLS` in `.env`

Multiple URLs: `SCRAPER_URLS=https://olx.pl/q/laptop/,https://olx.pl/q/gpu/`

> **Note:** The HTML parser in `internal/scraper/scraper.go` is a generic stub. If listings aren't being detected, you'll need to adapt `parseListings()` to match the HTML structure of your OLX region.

---

## API

| Endpoint | Description |
|----------|-------------|
| `GET /health` | Server + Ollama status |
| `GET /listings?limit=20&offset=0` | All enriched listings |
| `GET /listings/:id` | Single listing with full enrichment fields |
| `POST /register` | Create account |
| `POST /login` | Get JWT |
| `POST /admin/verify` | Verify a user (admin only) |
| `GET /admin/getUnverified` | List unverified users (admin only) |
| `POST /admin/listings/:id/re-enrich` | Re-queue a listing for LLM enrichment (admin only) |

Admin routes require a JWT from an account with `role = admin`.

---

## Enrichment fields

Each listing gets these fields populated by the LLM after scraping:

| Field | Type | Description |
|-------|------|-------------|
| `title_normalized` | string | Cleaned-up title |
| `price_amount` | number | Extracted price |
| `price_currency` | string | e.g. PLN, EUR |
| `condition` | string | `new`, `like_new`, `good`, `fair`, `poor` |
| `category` | string | Detected category |
| `location_city` | string | City extracted from listing |
| `specs` | object | Key-value specs (RAM, storage, etc.) |
| `deal_score` | 1–10 | 9–10 = great deal, 1–2 = suspicious |
| `deal_reasoning` | string | Why the score was given |
| `is_suspicious` | bool | Flagged if price outlier or vague description |
| `enrichment_status` | string | `pending` → `done` |

---

## Logs

```bash
# Watch everything
docker compose logs -f

# Just the API
docker compose logs -f api
```

---

## Stop / reset

```bash
# Stop
docker compose down

# Stop and wipe the database
docker compose down -v
```
