package handler

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"OlxScraper/internal/model"

	"github.com/labstack/echo/v4"
)

const uiStyles = `
body{font-family:monospace;margin:20px;background:#f5f5f5}
table{border-collapse:collapse;width:100%}
th,td{border:1px solid #ccc;padding:4px 8px;text-align:left;font-size:13px}
th{background:#eee}
tr:nth-child(even){background:#fafafa}
.pending{color:#888}.done{color:#060}.failed{color:#c00}
.score-high{color:#060;font-weight:bold}.score-mid{color:#a60}.score-low{color:#c00}
.suspicious{color:#c00;font-weight:bold}
.tabs{margin-bottom:12px}
.tab{display:inline-block;padding:6px 16px;border:1px solid #ccc;background:#eee;
     text-decoration:none;color:#333;margin-right:4px;border-radius:3px 3px 0 0}
.tab.active{background:#fff;border-bottom:1px solid #fff;font-weight:bold;color:#000}
.fresh{color:#060}.stale{color:#a60}.very-stale{color:#c00}
.detail-box{background:#fff;border:1px solid #ccc;padding:12px 16px;margin-bottom:16px;max-width:800px}
.detail-box h2{margin:0 0 10px 0;font-size:15px}
.detail-grid{display:grid;grid-template-columns:160px 1fr;gap:4px 12px;font-size:13px}
.detail-grid .k{color:#666}
.detail-grid .v{color:#000}
.tag{display:inline-block;background:#e0e0e0;border-radius:3px;padding:1px 6px;margin:2px 2px 0 0;font-size:12px}
.back{display:inline-block;margin-bottom:12px;text-decoration:none;color:#555;font-size:13px}
`

// HandleUI renders a tabbed HTML dashboard.
// Tab state is in ?tab= so the meta-refresh preserves it.
func (h *Handler) HandleUI(c echo.Context) error {
	tab := c.QueryParam("tab")
	idStr := c.QueryParam("id")

	// Normalise tab
	switch tab {
	case "components", "listing":
	default:
		tab = "listings"
	}

	var body string
	var err error
	var refreshURL string

	switch tab {
	case "listing":
		id, parseErr := strconv.ParseInt(idStr, 10, 64)
		if parseErr != nil || id <= 0 {
			return c.Redirect(http.StatusFound, "/?tab=listings")
		}
		refreshURL = fmt.Sprintf("/?tab=listing&id=%d", id)
		body, err = h.renderListingDetailTab(c, id)
	case "components":
		refreshURL = "/?tab=components"
		body, err = h.renderComponentsTab(c)
	default:
		refreshURL = "/?tab=listings"
		body, err = h.renderListingsTab(c)
	}

	if err != nil {
		return c.HTML(http.StatusInternalServerError, "<pre>error: "+err.Error()+"</pre>")
	}

	// Only show the top-level tabs for listings and components, not for the detail drilldown.
	tabBar := fmt.Sprintf(`<div class="tabs">
  <a class="tab%s" href="/?tab=listings">Listings</a>
  <a class="tab%s" href="/?tab=components">Component Prices</a>
</div>`, tabActive(tab == "listings"), tabActive(tab == "components"))

	html := fmt.Sprintf(`<!doctype html><html><head>
<meta charset="utf-8">
<meta http-equiv="refresh" content="5;url=%s">
<title>BttrListings</title>
<style>%s</style>
</head><body>
%s
%s
</body></html>`,
		refreshURL,
		uiStyles,
		tabBar,
		body,
	)
	return c.HTML(http.StatusOK, html)
}

func tabActive(active bool) string {
	if active {
		return " active"
	}
	return ""
}

// renderListingsTab renders the main listings table.
func (h *Handler) renderListingsTab(c echo.Context) (string, error) {
	ctx := c.Request().Context()
	listings, err := h.service.Listing.GetListings(ctx, 100, 0)
	if err != nil {
		return "", err
	}

	var pending, done, failed int
	for _, l := range listings {
		switch l.EnrichmentStatus {
		case "done":
			done++
		case "failed":
			failed++
		default:
			pending++
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(
		`<p><b>%d total</b> &nbsp;|&nbsp; <span class="pending">%d pending</span> &nbsp;|&nbsp; <span class="done">%d done</span>`,
		len(listings), pending, done,
	))
	if failed > 0 {
		sb.WriteString(fmt.Sprintf(` &nbsp;|&nbsp; <span class="failed">%d failed</span>`, failed))
	}
	sb.WriteString(fmt.Sprintf(` &nbsp;|&nbsp; <small>%s</small></p>`, time.Now().Format("15:04:05")))

	sb.WriteString(`<table>
<tr><th>#</th><th>title</th><th>price</th><th>score</th><th>mkt</th><th>condition</th><th>category</th><th>status</th><th>scraped</th></tr>
`)
	for i, l := range listings {
		statusClass := "pending"
		if l.EnrichmentStatus == "done" {
			statusClass = "done"
		} else if l.EnrichmentStatus == "failed" {
			statusClass = "failed"
		}

		price := ""
		if l.PriceAmount != nil && l.PriceCurrency != nil {
			price = fmt.Sprintf("%.0f %s", *l.PriceAmount, *l.PriceCurrency)
		} else if l.RawPrice != nil {
			price = *l.RawPrice
		}

		scoreCell := ""
		if l.DealScore != nil {
			cls := "score-low"
			if *l.DealScore >= 8 {
				cls = "score-high"
			} else if *l.DealScore >= 5 {
				cls = "score-mid"
			}
			scoreCell = fmt.Sprintf(`<span class="%s">%d</span>`, cls, *l.DealScore)
		}

		mktCell := ""
		if l.MarketScore != nil {
			pct := int(*l.MarketScore * 100)
			cls := "score-low"
			if pct <= 65 {
				cls = "score-high"
			} else if pct <= 85 {
				cls = "score-mid"
			}
			mktCell = fmt.Sprintf(`<span class="%s">%d%%</span>`, cls, pct)
		}

		cond := deref(l.Condition)
		cat := deref(l.Category)

		title := l.Title
		if len(title) > 55 {
			title = title[:52] + "..."
		}
		suspicious := ""
		if l.IsSuspicious != nil && *l.IsSuspicious {
			suspicious = ` <span class="suspicious">[!]</span>`
		}
		// Title links to OLX; detail icon links to the LLM output page.
		titleCell := fmt.Sprintf(
			`<a href="%s" target="_blank">%s</a>%s &nbsp;<a href="/?tab=listing&id=%d" title="LLM output">🔍</a>`,
			l.URL, escapeHTML(title), suspicious, l.ID,
		)

		age := relativeTime(time.Since(l.ScrapedAt))

		sb.WriteString(fmt.Sprintf(
			`<tr><td>%d</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td class="%s">%s</td><td>%s</td></tr>`,
			i+1, titleCell, price, scoreCell, mktCell, cond, cat, statusClass, l.EnrichmentStatus, age,
		))
	}
	sb.WriteString(`</table>`)
	return sb.String(), nil
}

// renderListingDetailTab shows all LLM-extracted and computed fields for one listing.
func (h *Handler) renderListingDetailTab(c echo.Context, id int64) (string, error) {
	ctx := c.Request().Context()
	l, err := h.service.Listing.GetListingByID(ctx, id)
	if err != nil {
		return "", err
	}
	if l == nil {
		return `<p>Listing not found.</p>`, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`<a class="back" href="/?tab=listings">← back to listings</a>`))

	sb.WriteString(`<div class="detail-box">`)
	sb.WriteString(fmt.Sprintf(`<h2>Listing #%d &mdash; %s</h2>`, l.ID, escapeHTML(l.Title)))

	sb.WriteString(`<div class="detail-grid">`)

	// ── Raw scraped data ──────────────────────────────────────────
	row := func(k, v string) {
		sb.WriteString(fmt.Sprintf(`<span class="k">%s</span><span class="v">%s</span>`, k, v))
	}

	row("URL", fmt.Sprintf(`<a href="%s" target="_blank">%s</a>`, l.URL, escapeHTML(l.URL)))
	row("Scraped", l.ScrapedAt.Format("2006-01-02 15:04:05"))
	if l.RawPrice != nil {
		row("Raw price", escapeHTML(*l.RawPrice))
	}
	row("Enrichment", enrichmentStatusHTML(l))
	if l.EnrichedAt != nil {
		row("Enriched at", l.EnrichedAt.Format("2006-01-02 15:04:05"))
	}

	sb.WriteString(`</div>`) // end detail-grid

	// ── LLM extracted facts ───────────────────────────────────────
	if l.EnrichmentStatus == "done" {
		sb.WriteString(`<hr style="margin:10px 0;border:none;border-top:1px solid #ddd">`)
		sb.WriteString(`<b style="font-size:13px">LLM extracted facts</b>`)
		sb.WriteString(`<div class="detail-grid" style="margin-top:6px">`)

		if l.TitleNormalized != nil {
			row("Title normalized", escapeHTML(*l.TitleNormalized))
		}
		if l.PriceAmount != nil && l.PriceCurrency != nil {
			row("Price", fmt.Sprintf("%.2f %s", *l.PriceAmount, *l.PriceCurrency))
		}
		if l.Condition != nil {
			row("Condition", *l.Condition)
		}
		if l.Category != nil {
			row("Category", *l.Category)
		}
		if l.LocationCity != nil && *l.LocationCity != "" {
			row("City", *l.LocationCity)
		}

		// Specs
		if len(l.Specs) > 0 {
			var specsHTML strings.Builder
			// Sort keys for stable output
			keys := make([]string, 0, len(l.Specs))
			for k := range l.Specs {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				specsHTML.WriteString(fmt.Sprintf(`<span class="tag">%s: %s</span>`,
					escapeHTML(k), escapeHTML(l.Specs[k])))
			}
			row("Specs", specsHTML.String())
		}

		sb.WriteString(`</div>`) // end detail-grid facts

		// ── Computed scores ───────────────────────────────────────
		sb.WriteString(`<hr style="margin:10px 0;border:none;border-top:1px solid #ddd">`)
		sb.WriteString(`<b style="font-size:13px">Computed scores</b>`)
		sb.WriteString(`<div class="detail-grid" style="margin-top:6px">`)

		if l.DealScore != nil {
			cls := scoreClass(int(*l.DealScore))
			row("Deal score", fmt.Sprintf(`<span class="%s">%d / 10</span>`, cls, *l.DealScore))
		}
		if l.DealReasoning != nil && *l.DealReasoning != "" {
			row("Reasoning", escapeHTML(*l.DealReasoning))
		}
		if l.MarketScore != nil {
			pct := int(*l.MarketScore * 100)
			cls := scoreClass(marketScoreToDeal(pct))
			row("Market score", fmt.Sprintf(`<span class="%s">%d%% of market</span>`, cls, pct))
		}

		suspicious := "no"
		if l.IsSuspicious != nil && *l.IsSuspicious {
			suspicious = `<span class="suspicious">YES</span>`
		}
		row("Suspicious", suspicious)
		if l.SuspiciousReason != nil && *l.SuspiciousReason != "" {
			row("Reason", escapeHTML(*l.SuspiciousReason))
		}

		sb.WriteString(`</div>`) // end detail-grid scores
	}

	sb.WriteString(`</div>`) // end detail-box

	// ── Description (collapsed) ───────────────────────────────────
	if l.Description != nil && *l.Description != "" {
		sb.WriteString(fmt.Sprintf(
			`<details style="max-width:800px;margin-top:8px"><summary style="cursor:pointer;font-size:13px">Raw description</summary><pre style="white-space:pre-wrap;font-size:12px;background:#fff;border:1px solid #ccc;padding:8px;margin-top:4px">%s</pre></details>`,
			escapeHTML(*l.Description),
		))
	}

	return sb.String(), nil
}

// renderComponentsTab renders the component prices table.
func (h *Handler) renderComponentsTab(c echo.Context) (string, error) {
	ctx := c.Request().Context()
	prices, err := h.service.ComponentPrice.GetAll(ctx)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(
		`<p><b>%d components tracked</b> &nbsp;|&nbsp; <small>%s</small></p>`,
		len(prices), time.Now().Format("15:04:05"),
	))

	sb.WriteString(`<table>
<tr><th>#</th><th>name</th><th>category</th><th>median price</th><th>samples</th><th>scraped</th></tr>
`)
	for i, cp := range prices {
		age := time.Since(cp.ScrapedAt)
		ageCls := "fresh"
		if age > 24*time.Hour {
			ageCls = "very-stale"
		} else if age > 6*time.Hour {
			ageCls = "stale"
		}
		sb.WriteString(fmt.Sprintf(
			`<tr><td>%d</td><td>%s</td><td>%s</td><td>%.0f %s</td><td>%d</td><td class="%s">%s</td></tr>`,
			i+1, escapeHTML(cp.Name), escapeHTML(cp.Category),
			cp.PriceAmount, cp.PriceCurrency,
			cp.SampleCount,
			ageCls, relativeTime(age),
		))
	}
	sb.WriteString(`</table>`)
	return sb.String(), nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func relativeTime(age time.Duration) string {
	switch {
	case age < time.Minute:
		return fmt.Sprintf("%ds ago", int(age.Seconds()))
	case age < time.Hour:
		return fmt.Sprintf("%dm ago", int(age.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(age.Hours()))
	}
}

func scoreClass(score int) string {
	if score >= 8 {
		return "score-high"
	} else if score >= 5 {
		return "score-mid"
	}
	return "score-low"
}

// marketScoreToDeal converts a market percentage to a rough deal score for colouring.
func marketScoreToDeal(pct int) int {
	switch {
	case pct <= 65:
		return 9
	case pct <= 85:
		return 6
	default:
		return 3
	}
}

func enrichmentStatusHTML(l *model.Listing) string {
	switch l.EnrichmentStatus {
	case "done":
		return `<span class="done">done</span>`
	case "failed":
		return `<span class="failed">failed</span>`
	default:
		return `<span class="pending">pending</span>`
	}
}

// escapeHTML replaces the five HTML-special characters so content doesn't break the page.
func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&#34;")
	s = strings.ReplaceAll(s, "'", "&#39;")
	return s
}
