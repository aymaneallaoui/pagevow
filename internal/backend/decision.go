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

	// ServerUsage is the usage value as the server sent it: nil when it had none, so a trace records null as Python does.
	ServerUsage json.RawMessage `json:"-"`

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
	Answers   json.RawMessage
	Model     string
	LatencyMS int64
	Error     string

	model json.RawMessage
}

func legOf(res *response, ms int64) CascadeLeg {
	return CascadeLeg{Answers: res.Answers, Model: res.Model, LatencyMS: ms, model: res.modelRaw}
}

// MarshalJSON writes the leg in the key order of the Python trace: an error leg is error then latency_ms.
func (l CascadeLeg) MarshalJSON() ([]byte, error) {
	if l.Error != "" {
		return object{{"error", l.Error}, {"latency_ms", l.LatencyMS}}.MarshalJSON()
	}
	legs := object{}
	if len(l.Answers) > 0 {
		legs = append(legs, member{"answers", l.Answers})
	}
	legs = append(legs, member{"model", modelValue(l.Model, l.model)}, member{"latency_ms", l.LatencyMS})
	return legs.MarshalJSON()
}

// CascadeHit is the veto cache entry that replaced a verifier call.
type CascadeHit struct {
	Answers  json.RawMessage `json:"answers"`
	Model    string          `json:"model"`
	FromStep int             `json:"from_step"`

	model json.RawMessage
}

// MarshalJSON writes answers, model, from_step, with the model as the server sent it.
func (h CascadeHit) MarshalJSON() ([]byte, error) {
	return object{{"answers", h.Answers}, {"model", modelValue(h.Model, h.model)}, {"from_step", h.FromStep}}.MarshalJSON()
}

func modelValue(name string, raw json.RawMessage) any {
	if raw != nil {
		return raw
	}
	return name
}
