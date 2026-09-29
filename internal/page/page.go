// Package page holds the observed state of a browser page, as produced by the embedded snapshot script.
package page

import "encoding/json"

// Action kinds reported by the snapshot script.
const (
	KindClick  = "click"
	KindFill   = "fill"
	KindSelect = "select"
	KindEnter  = "enter"
	KindScroll = "scroll"
	KindWait   = "wait"
)

// Rect is an element's bounding box in CSS pixels.
type Rect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// Action is one thing the agent may do on the page; JSON names match the snapshot script.
type Action struct {
	ID           string  `json:"id"`
	Kind         string  `json:"kind"`
	Label        string  `json:"label"`
	Role         string  `json:"role,omitempty"`
	Node         int     `json:"node,omitempty"`
	Value        *string `json:"value,omitempty"`
	CurrentValue *string `json:"current_value,omitempty"`
	Checked      *string `json:"checked,omitempty"`
	Selected     *string `json:"selected,omitempty"`
	Expanded     *string `json:"expanded,omitempty"`
	Rect         *Rect   `json:"rect,omitempty"`
	Delta        int     `json:"delta,omitempty"`
}

// FrameInfo describes one visible iframe of the page; its content is not part of the snapshot.
type FrameInfo struct {
	URL string `json:"url"`
	// SameOrigin is false when the frame document cannot be read from the page.
	SameOrigin bool `json:"same_origin"`
	// Controls counts the focusable controls of a readable frame and is zero otherwise.
	Controls int `json:"controls"`
}

// Scroll is the vertical scroll position and the document height.
type Scroll struct {
	Y      float64 `json:"y"`
	Height float64 `json:"height"`
}

// State is one snapshot; Marker, PageKey and Guards are opaque values compared for freshness, never interpreted.
type State struct {
	URL            string                     `json:"url"`
	Title          string                     `json:"title"`
	Width          int                        `json:"w"`
	Height         int                        `json:"h"`
	Text           string                     `json:"text"`
	Scroll         Scroll                     `json:"scroll"`
	Actions        []Action                   `json:"actions"`
	Marker         json.RawMessage            `json:"marker"`
	PageKey        json.RawMessage            `json:"page_key"`
	Guards         map[string]json.RawMessage `json:"guards"`
	OmittedActions int                        `json:"omitted_actions"`
	Fingerprint    string                     `json:"fingerprint,omitempty"`
	// Frames is gathered apart from the snapshot; it is not part of the fingerprint and is never sent to a model.
	Frames []FrameInfo `json:"frames,omitempty"`
}

// HeldValue is the value a control holds: its current value when reported, otherwise its value.
func (a Action) HeldValue() string {
	if a.CurrentValue != nil {
		return *a.CurrentValue
	}
	if a.Value != nil {
		return *a.Value
	}
	return ""
}

// ActionByID returns the action with the given id.
func (s State) ActionByID(id string) (Action, bool) {
	for _, action := range s.Actions {
		if action.ID == id {
			return action, true
		}
	}
	return Action{}, false
}
