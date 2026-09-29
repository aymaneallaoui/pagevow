package trace

import (
	"math"
	"slices"
)

// Meta is the content of <run_id>.meta.json.
type Meta struct {
	Goal        string  `json:"goal"`
	URL         string  `json:"url"`
	Status      string  `json:"status"`
	Steps       int     `json:"steps"`
	ElapsedMS   int     `json:"elapsed_ms"`
	Verified    *bool   `json:"verified"`
	Error       *string `json:"error,omitempty"`
	RawResponse *string `json:"raw_response,omitempty"`
	Reason      *string `json:"reason,omitempty"`
}

// Stats are the latency and token totals of a run; a nil field means nothing was recorded.
type Stats struct {
	LatencyP50MS     *int `json:"jev_latency_p50_ms"`
	LatencyMaxMS     *int `json:"jev_latency_max_ms"`
	InputTokensTotal *int `json:"input_tokens_total"`
}

// FinishOption adds an optional field to the meta file.
type FinishOption func(*Meta)

// WithError records the error that ended the run.
func WithError(err error) FinishOption {
	return func(m *Meta) {
		if err != nil {
			message := err.Error()
			m.Error = &message
		}
	}
}

// WithRawResponse records the start of the model reply that caused the error.
func WithRawResponse(raw string) FinishOption {
	return func(m *Meta) { m.RawResponse = &raw }
}

// WithReason records why the run ended BLOCKED.
func WithReason(reason string) FinishOption {
	return func(m *Meta) { m.Reason = &reason }
}

// median rounds half to even, like Python's round(statistics.median(values)).
func median(values []int) int {
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return int(math.RoundToEven(float64(sorted[mid-1]+sorted[mid]) / 2))
}
