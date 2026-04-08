package model

import "time"

// ComponentPrice is a row from the component_prices table.
type ComponentPrice struct {
	ID             int64
	Name           string
	NameNormalized string
	Category       string
	PriceAmount    float64
	PriceCurrency  string
	SampleCount    int32
	ScrapedAt      time.Time
}
