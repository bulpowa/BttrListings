package model

// EnrichedScores holds all scores computed in Go after LLM extraction.
// These are derived from real market data — never hallucinated by the LLM.
type EnrichedScores struct {
	MarketScore      *float64 // ask / sum(component market prices); nil when no data available
	DealScore        int      // 1-10, derived from MarketScore + condition
	DealReasoning    string   // human-readable explanation of the score
	IsSuspicious     bool
	SuspiciousReason *string
}
