package backend

import "encoding/json"

// Escalation reasons and the source a cascade decision was taken from.
const (
	ReasonDone       = "done"
	ReasonBlocked    = "blocked"
	ReasonTargetConf = "target_conf"

	UsedPrimary  = "primary"
	UsedVerifier = "verifier"
	UsedCache    = "cache"
)

// Decision is the validated choice of one model call; JSON names match the Python decision and its trace fields.
type Decision struct {
	Choice                 string             `json:"choice"`
	Operation              string             `json:"operation"`
	Target                 *string            `json:"target"`
	Confidence             float64            `json:"confidence"`
	Probabilities          map[string]float64 `json:"probabilities"`
	TargetIDs              map[string]string  `json:"target_ids"`
	OperationProbabilities map[string]float64 `json:"operation_probabilities"`
	TargetProbabilities    map[string]float64 `json:"target_probabilities"`
	TargetConfidence       *float64           `json:"target_confidence"`
	RawAnswers             json.RawMessage    `json:"raw_answers"`
	Model                  string             `json:"model"`
	Usage                  json.RawMessage    `json:"usage"`
	LatencyMS              int64              `json:"latency_ms"`
	Request                json.RawMessage    `json:"request"`
	Cascade                *Cascade           `json:"cascade,omitempty"`

	// VetoKey is the cache key of the request; pass it to VetoCache.Forget after the action changed the page.
	VetoKey VetoKey `json:"-"`
}

// Cascade records why the verifier was asked and whose answer was used.
type Cascade struct {
	Reason   string      `json:"reason"`
	Used     string      `json:"used"`
	Primary  CascadeLeg  `json:"primary"`
	Verifier *CascadeLeg `json:"verifier,omitempty"`
	Cache    *CascadeHit `json:"cache,omitempty"`
}

// CascadeLeg is one model call inside a cascade: its answers, or the error that ended it.
type CascadeLeg struct {
	Answers   json.RawMessage `json:"answers,omitempty"`
	Model     string          `json:"model,omitempty"`
	LatencyMS int64           `json:"latency_ms"`
	Error     string          `json:"error,omitempty"`
}

// CascadeHit is the veto cache entry that replaced a verifier call.
type CascadeHit struct {
	Answers  json.RawMessage `json:"answers"`
	Model    string          `json:"model"`
	FromStep int             `json:"from_step"`
}
