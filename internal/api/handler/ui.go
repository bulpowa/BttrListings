package handler

import (
	"fmt"
	"net/http"
	"strings"
	"time"

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
.suspicious{color:#c00}
.tabs{margin-bottom:12px}
.tab{display:inline-block;padding:6px 16px;border:1px solid #ccc;background:#eee;
     text-decoration:none;color:#333;margin-right:4px;border-radius:3px 3px 0 0}
.tab.active{background:#fff;border-bottom:1px solid #fff;font-weight:bold;color:#000}
.fresh{color:#060}.stale{color:#a60}.very-stale{color:#c00}
`

// HandleUI renders a tabbed HTML dashboard.
// Tab state is carried in ?tab= so the meta-refresh preserves it.
func (h *Handler) HandleUI(c echo.Context) error {
	tab := c.QueryParam("tab")
	if tab != "components" {
		tab = "listings"
	}

	var body string
	var err error
	if tab == "components" {
		body, err = h.renderComponentsTab(c)
	} else {
		body, err = h.renderListingsTab(c)
	}
	if err != nil {
		return c.HTML(http.StatusInternalServerError, "<pre>error: "+err.Error()+"</pre>")
	}

	refreshURL := "/?tab=" + tab
	html := fmt.Sprintf(`<!doctype html><html><head>
<meta charset="utf-8">
<meta http-equiv="refresh" content="5;url=%s">
<title>BttrListings</title>
<style>%s</style>
</head><body>
<div class="tabs">
  <a class="tab%s" href="/?tab=listings">Listings</a>
  <a class="tab%s" href="/?tab=components">Component Prices</a>
</div>
%s
</body></html>`,
		refreshURL,
		uiStyles,
		tabActive(tab == "listings"),
		tabActive(tab == "components"),
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

		cond := ""
		if l.Condition != nil {
			cond = *l.Condition
		}
		cat := ""
		if l.Category != nil {
			cat = *l.Category
		}

		title := l.Title
		if len(title) > 60 {
			title = title[:57] + "..."
		}
		suspicious := ""
		if l.IsSuspicious != nil && *l.IsSuspicious {
			suspicious = ` <span class="suspicious">[!]</span>`
		}
		titleCell := fmt.Sprintf(`<a href="%s" target="_blank">%s</a>%s`, l.URL, escapeHTML(title), suspicious)

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
	sb.WriteString(`</table>`)
	return sb.String(), nil
}

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
		var agoStr string
		switch {
		case age < time.Minute:
			agoStr = fmt.Sprintf("%ds ago", int(age.Seconds()))
		case age < time.Hour:
			agoStr = fmt.Sprintf("%dm ago", int(age.Minutes()))
		default:
			agoStr = fmt.Sprintf("%dh ago", int(age.Hours()))
		}

		// Colour-code freshness: green <6h, amber 6-24h, red >24h
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
			ageCls, agoStr,
		))
	}
	sb.WriteString(`</table>`)
	return sb.String(), nil
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
