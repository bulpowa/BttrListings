package handler

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

// HandleUI renders a plain HTML dashboard showing recent listings and their enrichment status.
// Auto-refreshes every 5 seconds. No JS frameworks, no external CSS.
func (h *Handler) HandleUI(c echo.Context) error {
	ctx := c.Request().Context()

	listings, err := h.service.Listing.GetListings(ctx, 100, 0)
	if err != nil {
		return c.HTML(http.StatusInternalServerError, "<pre>db error: "+err.Error()+"</pre>")
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

	sb.WriteString(`<!doctype html><html><head>
<meta charset="utf-8">
<meta http-equiv="refresh" content="5">
<title>BttrListings</title>
<style>
body{font-family:monospace;margin:20px;background:#f5f5f5}
table{border-collapse:collapse;width:100%}
th,td{border:1px solid #ccc;padding:4px 8px;text-align:left;font-size:13px}
th{background:#eee}
tr:nth-child(even){background:#fafafa}
.pending{color:#888}
.done{color:#060}
.failed{color:#c00}
.score-high{color:#060;font-weight:bold}
.score-mid{color:#a60}
.score-low{color:#c00}
.suspicious{color:#c00}
</style>
</head><body>`)

	// Stats bar
	sb.WriteString(fmt.Sprintf(
		`<p><b>BttrListings</b> &mdash; %d total &nbsp;|&nbsp; <span class="pending">%d pending</span> &nbsp;|&nbsp; <span class="done">%d done</span>`,
		len(listings), pending, done,
	))
	if failed > 0 {
		sb.WriteString(fmt.Sprintf(` &nbsp;|&nbsp; <span class="failed">%d failed</span>`, failed))
	}
	sb.WriteString(fmt.Sprintf(` &nbsp;|&nbsp; <small>refreshes every 5s &mdash; %s</small></p>`, time.Now().Format("15:04:05")))

	// Table
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

		// Price
		price := ""
		if l.PriceAmount != nil && l.PriceCurrency != nil {
			price = fmt.Sprintf("%.0f %s", *l.PriceAmount, *l.PriceCurrency)
		} else if l.RawPrice != nil {
			price = *l.RawPrice
		}

		// Deal score
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

		// Market score — show as percentage of market (lower = better deal)
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

		// Condition
		cond := ""
		if l.Condition != nil {
			cond = *l.Condition
		}

		// Category
		cat := ""
		if l.Category != nil {
			cat = *l.Category
		}

		// Title — link to OLX, truncate if long
		title := l.Title
		if len(title) > 60 {
			title = title[:57] + "..."
		}
		suspicious := ""
		if l.IsSuspicious != nil && *l.IsSuspicious {
			suspicious = ` <span class="suspicious">[!]</span>`
		}
		titleCell := fmt.Sprintf(`<a href="%s" target="_blank">%s</a>%s`, l.URL, escapeHTML(title), suspicious)

		// Scraped at — relative
		age := time.Since(l.ScrapedAt)
		var agoStr string
		switch {
		case age < time.Minute:
			agoStr = fmt.Sprintf("%ds ago", int(age.Seconds()))
		case age < time.Hour:
			agoStr = fmt.Sprintf("%dm ago", int(age.Minutes()))
		default:
			agoStr = fmt.Sprintf("%dh ago", int(age.Hours()))
		}

		sb.WriteString(fmt.Sprintf(
			`<tr><td>%d</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td class="%s">%s</td><td>%s</td></tr>`,
			i+1, titleCell, price, scoreCell, mktCell, cond, cat, statusClass, l.EnrichmentStatus, agoStr,
		))
	}

	sb.WriteString(`</table></body></html>`)

	return c.HTML(http.StatusOK, sb.String())
}

// escapeHTML replaces the five HTML-special characters so titles don't break the table.
func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&#34;")
	s = strings.ReplaceAll(s, "'", "&#39;")
	return s
}
