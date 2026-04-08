package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const systemPrompt = `You are a marketplace listing parser for OLX.bg. Extract structured fields from the listing text.
Respond ONLY with valid JSON matching this schema exactly. No markdown, no commentary, no explanation.

CRITICAL RULES:
- Extract ONLY what is explicitly stated. NEVER guess, infer, or invent specs.
- If a detail is absent, use "" for strings, 0 for price_amount, [] for arrays, {} for specs.
- Correct obvious spelling mistakes in brand/model names ("rysen" → "Ryzen", "geforce" → "GeForce")
  but do NOT change the actual model referenced.
- Listings may be in Bulgarian, English, or mixed. Extract the same way regardless of language.

Schema:
{
  "title_normalized": "string",
  "price_amount": number,
  "price_currency": "string",
  "condition": "new|open_box|like_new|good|fair|poor|damaged",
  "category": "string",
  "location_city": "string",
  "specs": {"key": "value"},
  "deal_score": 1-10,
  "deal_reasoning": "string",
  "is_suspicious": true|false,
  "suspicious_reason": "string or null",
  "components": ["canonical model name", ...]
}

FIELD RULES:

title_normalized: Clean, concise product name. Remove seller noise ("urgent", "DM me", emojis).
  "Продавам RTX 3080 😊 бързо!!" → "RTX 3080"

price_amount / price_currency:
  - Strip currency symbols and separators: "1 200 лв." → 1200, "BGN"
  - BGN (лв / лева) → currency "BGN". EUR (€) → "EUR". USD ($) → "USD".
  - If multiple prices appear (original + asking), use the asking/sale price.
  - Ignore "OBO", "firm", "negotiable" — extract the number only.
  - No price stated → price_amount: 0, price_currency: "".

condition (pick ONE):
  - new: sealed box, never used
  - open_box: opened but unused
  - like_new: used, no visible wear
  - good: normal use, minor cosmetic wear, fully functional
  - fair: visible wear, may have minor issues but works
  - poor: significant wear or functionality issues
  - damaged: broken, cracked, for parts, not working
  Default to "good" for secondhand marketplace listings unless another condition is stated.

specs: Put granular details here that don't fit other fields. For PC listings: RAM speed, storage
  brand, GPU brand/variant. For phones: color, storage tier. Keep keys short ("ram_speed", "color").
  Do NOT duplicate info that already appears in components, title_normalized, or condition.

components: Canonical model names for market price lookup.
  - Shortest unambiguous identifier: "RTX 4060" not "NVIDIA GeForce RTX 4060 Gaming OC 8GB".
  - Suffixes that affect price ARE part of the identifier: "RTX 4060 Ti", "RTX 4060 Super",
    "iPhone 13 128GB" vs "iPhone 13 256GB".
  - Normalize marketing noise away: "RTX 4060 Minecraft Edition" → "RTX 4060".
  - Full PC: list CPU and GPU separately — ["i7-12700K", "RTX 3070"].
  - Output [] for listings with no market-priceable components (furniture, cars, clothing).

deal_score (1-10):
  - 9-10: Excellent condition, priced 20%+ below typical market for this item
  - 7-8: Good condition, fair or slightly below market
  - 5-6: Average condition and price
  - 3-4: Poor condition or noticeably overpriced
  - 1-2: Very poor condition, extreme price, or suspicious

is_suspicious / suspicious_reason:
  Set is_suspicious=true and explain in suspicious_reason if ANY of:
  - Price is under 20% or over 3x typical market value for this item
  - Description is fewer than 10 words
  - Listing appears copy-pasted or templated (no personal details)
  - Urgent language with unusually low price ("бързо", "спешно" + suspiciously cheap)

EXAMPLES:

Input: "RTX 3080 10GB Founders Edition. Купена от Technomarket преди 1 година, работи перфектно. 850 лв. гр. София"
Output:
{
  "title_normalized": "NVIDIA RTX 3080 10GB",
  "price_amount": 850,
  "price_currency": "BGN",
  "condition": "good",
  "category": "GPU",
  "location_city": "Sofia",
  "specs": {"variant": "Founders Edition"},
  "deal_score": 7,
  "deal_reasoning": "Good condition card, price is reasonable for the market",
  "is_suspicious": false,
  "suspicious_reason": null,
  "components": ["RTX 3080"]
}

Input: "Gaming PC: i7-12700K, RTX 3070 8GB MSI Gaming X, 32GB DDR4 3200MHz, 1TB Samsung 980 NVMe, be quiet! Pure Base 500, 750W Seasonic Gold. 2 200 лв. Без монитор, без периферия."
Output:
{
  "title_normalized": "Gaming PC i7-12700K RTX 3070",
  "price_amount": 2200,
  "price_currency": "BGN",
  "condition": "good",
  "category": "PC",
  "location_city": "",
  "specs": {"ram": "32GB DDR4 3200MHz", "storage": "1TB Samsung 980 NVMe", "psu": "750W Seasonic Gold", "case": "be quiet! Pure Base 500", "gpu_variant": "MSI Gaming X"},
  "deal_score": 6,
  "deal_reasoning": "Complete build at average market price, no peripherals included",
  "is_suspicious": false,
  "suspicious_reason": null,
  "components": ["i7-12700K", "RTX 3070"]
}

Input: "iPhone 14 Pro 128gb перфектен 150лв бързо"
Output:
{
  "title_normalized": "iPhone 14 Pro 128GB",
  "price_amount": 150,
  "price_currency": "BGN",
  "condition": "like_new",
  "category": "Phone",
  "location_city": "",
  "specs": {},
  "deal_score": 2,
  "deal_reasoning": "Price is far below typical market value for this model",
  "is_suspicious": true,
  "suspicious_reason": "Price of 150 BGN is under 20% of typical market value for iPhone 14 Pro 128GB; urgent language",
  "components": ["iPhone 14 Pro 128GB"]
}`

var (
	htmlTagRe    = regexp.MustCompile(`<[^>]+>`)
	whitespaceRe = regexp.MustCompile(`\s+`)
)

// PreprocessText strips HTML and truncates to ~2000 tokens before sending to the LLM.
func PreprocessText(rawHTML string) string {
	text := html.UnescapeString(htmlTagRe.ReplaceAllString(rawHTML, " "))
	text = whitespaceRe.ReplaceAllString(strings.TrimSpace(text), " ")
	if len(text) > 8000 {
		text = text[:8000]
	}
	return text
}

// OllamaClient calls an OpenAI-compatible LLM server (e.g. LM Studio) for structured JSON extraction.
type OllamaClient struct {
	host   string
	model  string
	client *http.Client
}

func NewOllamaClient(host, model string) *OllamaClient {
	return &OllamaClient{
		host:  strings.TrimRight(host, "/"),
		model: model,
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

// OpenAI-compatible request/response types.

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	Stream      bool          `json:"stream"`
}

type chatChoice struct {
	Message chatMessage `json:"message"`
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`
}

// Ping checks whether the LLM server is reachable.
func (c *OllamaClient) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.host+"/api/v1/models", nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("LLM server returned status %d", resp.StatusCode)
	}
	return nil
}

// IsReachable returns true if the LLM server is currently reachable.
func (c *OllamaClient) IsReachable(ctx context.Context) bool {
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return c.Ping(pingCtx) == nil
}

// Extract sends preprocessed listing text to the LLM and returns structured fields.
// It retries once with temperature=0.1 if the first response fails JSON validation.
func (c *OllamaClient) Extract(ctx context.Context, text string) (*ExtractionResult, error) {
	temperatures := []float64{0, 0.1}
	var lastErr error
	for _, temp := range temperatures {
		result, err := c.extractOnce(ctx, text, temp)
		if err == nil {
			return result, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("LLM extraction failed after retries: %w", lastErr)
}

func (c *OllamaClient) extractOnce(ctx context.Context, text string, temperature float64) (*ExtractionResult, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 110*time.Second)
	defer cancel()

	body, err := json.Marshal(chatRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: text},
		},
		Temperature: temperature,
		Stream:      false,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.host+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("LLM request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LLM status %d", resp.StatusCode)
	}

	var chatResp chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decode LLM response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("LLM returned no choices")
	}

	content := chatResp.Choices[0].Message.Content

	// Strip markdown code fences if the model wraps JSON in ```json ... ```
	if idx := strings.Index(content, "{"); idx > 0 {
		content = content[idx:]
	}
	if idx := strings.LastIndex(content, "}"); idx >= 0 && idx < len(content)-1 {
		content = content[:idx+1]
	}

	var result ExtractionResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil, fmt.Errorf("parse extraction JSON: %w", err)
	}

	return &result, nil
}
