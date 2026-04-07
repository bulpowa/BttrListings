package llm

// ExtractionResult is the structured JSON the LLM returns for each listing.
type ExtractionResult struct {
	TitleNormalized  string            `json:"title_normalized"`
	PriceAmount      float64           `json:"price_amount"`
	PriceCurrency    string            `json:"price_currency"`
	Condition        string            `json:"condition"`
	Category         string            `json:"category"`
	LocationCity     string            `json:"location_city"`
	Specs            map[string]string `json:"specs"`
	DealScore        int               `json:"deal_score"`
	DealReasoning    string            `json:"deal_reasoning"`
	IsSuspicious     bool              `json:"is_suspicious"`
	SuspiciousReason *string           `json:"suspicious_reason"`
}
