package alexa

import "strings"

type RequestEnvelope struct {
	Version string         `json:"version"`
	Session Session        `json:"session"`
	Context Context        `json:"context"`
	Request RequestPayload `json:"request"`
}

type Session struct {
	New         bool        `json:"new"`
	SessionID   string      `json:"sessionId"`
	Application Application `json:"application"`
	User        User        `json:"user"`
}

type Context struct {
	System System `json:"System"`
}

type System struct {
	Application Application `json:"application"`
	User        User        `json:"user"`
}

type Application struct {
	ApplicationID string `json:"applicationId"`
}

type User struct {
	UserID string `json:"userId"`
}

type RequestPayload struct {
	Type      string  `json:"type"`
	RequestID string  `json:"requestId"`
	Timestamp string  `json:"timestamp"`
	Locale    string  `json:"locale"`
	Intent    *Intent `json:"intent,omitempty"`
	Reason    string  `json:"reason,omitempty"`
}

type Intent struct {
	Name  string          `json:"name"`
	Slots map[string]Slot `json:"slots,omitempty"`
}

type Slot struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

func (e RequestEnvelope) UserID() string {
	if strings.TrimSpace(e.Session.User.UserID) != "" {
		return e.Session.User.UserID
	}
	if strings.TrimSpace(e.Context.System.User.UserID) != "" {
		return e.Context.System.User.UserID
	}
	return "unknown"
}

func (e RequestEnvelope) SessionID() string {
	return e.Session.SessionID
}

func (e RequestEnvelope) ApplicationID() string {
	if strings.TrimSpace(e.Session.Application.ApplicationID) != "" {
		return e.Session.Application.ApplicationID
	}
	return e.Context.System.Application.ApplicationID
}

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
