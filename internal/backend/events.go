package backend

import (
	"time"
)

// EventType 定義標準化事件類型
type EventType string

const (
	EventTypeText       EventType = "text"
	EventTypeTool       EventType = "tool"
	EventTypeToolStatus EventType = "tool_status"
	EventTypeError      EventType = "error"
	EventTypeRateLimit  EventType = "rate_limit"
)

// Event 代表後端執行期間產出之正規化事件
type Event struct {
	Type      EventType         `json:"type"`
	Text      string            `json:"text,omitempty"`
	Tool      string            `json:"tool,omitempty"`
	Status    string            `json:"status,omitempty"`
	Error     string            `json:"error,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// NewTextEvent 建立純文字事件
func NewTextEvent(text string) Event {
	return Event{
		Type:      EventTypeText,
		Text:      text,
		Timestamp: time.Now(),
	}
}

// NewToolEvent 建立工具調用事件
func NewToolEvent(tool, status string) Event {
	return Event{
		Type:      EventTypeTool,
		Tool:      tool,
		Status:    status,
		Timestamp: time.Now(),
	}
}

// NewErrorEvent 建立錯誤事件
func NewErrorEvent(errStr string) Event {
	return Event{
		Type:      EventTypeError,
		Error:     errStr,
		Timestamp: time.Now(),
	}
}
