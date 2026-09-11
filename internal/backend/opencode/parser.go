package opencode

import (
	"encoding/json"
	"strings"
)

type TokenUsage struct {
	Total     int `json:"total"`
	Input     int `json:"input"`
	Output    int `json:"output"`
	Reasoning int `json:"reasoning,omitempty"`
}

type Event struct {
	Type        string     `json:"type"`
	Timestamp   int64      `json:"timestamp"`
	SessionID   string     `json:"sessionID"`
	Text        string     `json:"text,omitempty"`
	Tool        string     `json:"tool,omitempty"`
	ToolStatus  string     `json:"tool_status,omitempty"`
	Tokens      TokenUsage `json:"tokens,omitempty"`
	IsRateLimit bool       `json:"is_rate_limit,omitempty"`
	RawJSON     string     `json:"-"`
}

type rawEvent struct {
	Type      string `json:"type"`
	Timestamp int64  `json:"timestamp"`
	SessionID string `json:"sessionID"`
	Part      *struct {
		Type   string     `json:"type"`
		Text   string     `json:"text,omitempty"`
		Tool   string     `json:"tool,omitempty"`
		Reason string     `json:"reason,omitempty"`
		Tokens TokenUsage `json:"tokens,omitempty"`
		State  *struct {
			Status string `json:"status,omitempty"`
			Output string `json:"output,omitempty"`
		} `json:"state,omitempty"`
	} `json:"part,omitempty"`
	Error *struct {
		Message    string `json:"message,omitempty"`
		StatusCode int    `json:"statusCode,omitempty"`
	} `json:"error,omitempty"`
}

func ParseEventLine(line string) (*Event, error) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		return nil, nil
	}

	var raw rawEvent
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil, nil
	}

	evt := &Event{
		Type:      raw.Type,
		Timestamp: raw.Timestamp,
		SessionID: raw.SessionID,
		RawJSON:   trimmed,
	}

	if raw.Error != nil {
		if raw.Error.StatusCode == 429 || strings.Contains(strings.ToLower(raw.Error.Message), "rate limit") {
			evt.IsRateLimit = true
		}
	}

	if raw.Part != nil {
		if raw.Part.Text != "" {
			evt.Text = raw.Part.Text
		}
		if raw.Part.Tool != "" {
			evt.Tool = raw.Part.Tool
		}
		if raw.Part.State != nil && raw.Part.State.Status != "" {
			evt.ToolStatus = raw.Part.State.Status
		}
		if raw.Part.Tokens.Total > 0 {
			evt.Tokens = raw.Part.Tokens
		}
	}

	return evt, nil
}
