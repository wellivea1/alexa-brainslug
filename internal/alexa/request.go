package alexa

import "strings"

// RequestEnvelope is the JSON body Alexa posts to the skill endpoint.
//
// Only the fields this skill uses are modelled. Alexa sends considerably
// more, and unknown fields are ignored rather than rejected so that an
// Alexa-side addition cannot break the endpoint.
type RequestEnvelope struct {
	Version string         `json:"version"`
	Session Session        `json:"session"`
	Context Context        `json:"context"`
	Request RequestPayload `json:"request"`
}

// Session describes the conversation a request belongs to.
type Session struct {
	New         bool        `json:"new"`
	SessionID   string      `json:"sessionId"`
	Application Application `json:"application"`
	User        User        `json:"user"`
}

// Context carries device and system state.
type Context struct {
	System System `json:"System"`
}

// System is the context block holding the application and user.
type System struct {
	Application Application `json:"application"`
	User        User        `json:"user"`
}

// Application identifies the skill.
type Application struct {
	ApplicationID string `json:"applicationId"`
}

// User identifies the Alexa account. The ID is opaque and skill-scoped: it
// identifies the same person across sessions but reveals nothing about them.
type User struct {
	UserID string `json:"userId"`
}

// RequestPayload is the request itself.
type RequestPayload struct {
	Type      string  `json:"type"`
	RequestID string  `json:"requestId"`
	Timestamp string  `json:"timestamp"`
	Locale    string  `json:"locale"`
	Intent    *Intent `json:"intent,omitempty"`
	Reason    string  `json:"reason,omitempty"`
}

// Intent is the matched intent and its slots.
type Intent struct {
	Name  string          `json:"name"`
	Slots map[string]Slot `json:"slots,omitempty"`
}

// Slot is one captured slot value. Value is empty when Alexa matched the
// intent without filling the slot.
type Slot struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

// UserID returns the Alexa user identifier, or empty when neither location in
// the request carries one. Callers must reject an empty ID rather than placing
// unrelated requests into a shared anonymous history bucket.
func (e *RequestEnvelope) UserID() string {
	if id := strings.TrimSpace(e.Session.User.UserID); id != "" {
		return id
	}
	return strings.TrimSpace(e.Context.System.User.UserID)
}

// SessionID returns the session identifier, which may be empty.
func (e *RequestEnvelope) SessionID() string {
	return e.Session.SessionID
}

// ApplicationID returns the skill identifier, checking both places Alexa
// reports it.
func (e *RequestEnvelope) ApplicationID() string {
	if id := strings.TrimSpace(e.Session.Application.ApplicationID); id != "" {
		return id
	}
	return e.Context.System.Application.ApplicationID
}

// SlotValue returns a trimmed slot value, or empty if the slot is absent.
func (i *Intent) SlotValue(name string) string {
	if i == nil {
		return ""
	}
	slot, ok := i.Slots[name]
	if !ok {
		return ""
	}
	return strings.TrimSpace(slot.Value)
}
