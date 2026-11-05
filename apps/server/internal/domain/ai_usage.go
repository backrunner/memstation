package domain

import "time"

type AIUsageQuery struct {
	Since    time.Time `json:"since"`
	Until    time.Time `json:"until"`
	Stage    string    `json:"stage"`
	Strategy string    `json:"strategy"`
	Status   string    `json:"status"`
	Model    string    `json:"model"`
}
type AIUsageGroup struct {
	Day             string `json:"day"`
	Stage           string `json:"stage"`
	Strategy        string `json:"strategy"`
	Model           string `json:"model"`
	Authority       string `json:"authority"`
	Status          string `json:"status"`
	Calls           int64  `json:"calls"`
	KnownUsageCalls int64  `json:"known_usage_calls"`
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	DurationMS      int64  `json:"duration_ms"`
	Retries         int64  `json:"retries"`
}
type CurrencyCost struct {
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
	Calls    int64  `json:"calls"`
}
type AIUsageReport struct {
	Query         AIUsageQuery   `json:"query"`
	Groups        []AIUsageGroup `json:"groups"`
	Costs         []CurrencyCost `json:"costs"`
	UnpricedCalls int64          `json:"unpriced_calls"`
}
