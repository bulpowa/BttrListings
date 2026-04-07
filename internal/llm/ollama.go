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

const systemPrompt = `You are a marketplace listing parser. Extract structured fields from the listing below.
Respond ONLY with valid JSON matching this schema exactly. Do not add commentary.

Schema:
{
  "title_normalized": "string",
  "price_amount": number,
  "price_currency": "string",
  "condition": "new|like_new|good|fair|poor",
  "category": "string",
  "location_city": "string",
  "specs": {"key": "value"},
  "deal_score": 1-10,
  "deal_reasoning": "string",
  "is_suspicious": true|false,
  "suspicious_reason": "string or null"
}

Scoring guide for deal_score (1-10):
- 9-10: Pristine condition, priced 20%+ below typical market value for this category
- 7-8: Good condition, fair or slightly below market price
- 5-6: Average condition and price
- 3-4: Poor condition or overpriced
- 1-2: Suspicious listing (missing photos, vague description, price outlier)

Set is_suspicious=true if: no photos mentioned, price is >3x or <20% of typical,
description is <10 words, or listing appears copy-pasted.`

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
