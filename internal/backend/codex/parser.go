package codex

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"asura/internal/backend"
)

// RawEvent 代表 Codex JSONL 事件之原始結構
type RawEvent struct {
	Type     string                 `json:"type"`
	ThreadID string                 `json:"thread_id,omitempty"`
	Message  string                 `json:"message,omitempty"`
	Error    string                 `json:"error,omitempty"`
	Item     map[string]interface{} `json:"item,omitempty"`
	Usage    map[string]interface{} `json:"usage,omitempty"`
}

// ParseJSONLLine 解析單行 Codex JSONL
func ParseJSONLLine(line string) (*RawEvent, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return nil, nil
	}
	var evt RawEvent
	if err := json.Unmarshal([]byte(trimmed), &evt); err != nil {
		return nil, err
	}
	return &evt, nil
}

// ExtractResultAndEvents 從 Codex 輸出的 stdout 與 stderr 中解析出結構化事件、最終回答與錯誤分類
func ExtractResultAndEvents(rawStdout, rawStderr string) (output string, partialOutput string, events []backend.Event, failureClass backend.FailureClassification, err error) {
	events = make([]backend.Event, 0)
	var sb strings.Builder
	var lastErr string
	hasAuth := false
	hasRateLimit := false

	scanner := bufio.NewScanner(strings.NewReader(rawStdout))
	for scanner.Scan() {
		line := scanner.Text()
		evt, pErr := ParseJSONLLine(line)
		if pErr != nil || evt == nil {
			// 檢查非 JSON 輸出的錯誤訊息（例如 stderr 或 CLI 直接輸出的 error log）
			checkErrorLine(line, &hasAuth, &hasRateLimit, &lastErr)
			continue
		}

		switch evt.Type {
		case "item.completed", "item.started":
			if item := evt.Item; item != nil {
				itemType, _ := item["type"].(string)
				switch itemType {
				case "agent_message":
					if text, ok := item["text"].(string); ok && text != "" {
						if sb.Len() > 0 && !strings.HasSuffix(sb.String(), "\n") {
							sb.WriteString("\n")
						}
						sb.WriteString(text)
						events = append(events, backend.NewTextEvent(text))
					}
				case "command_execution":
					cmdStr, _ := item["command"].(string)
					status, _ := item["status"].(string)
					events = append(events, backend.NewToolEvent(cmdStr, status))
				case "file_change":
					status, _ := item["status"].(string)
					events = append(events, backend.NewToolEvent("file_change", status))
				}
			}
		case "error":
			errMsg := evt.Message
			if errMsg == "" {
				errMsg = evt.Error
			}
			if errMsg != "" {
				lastErr = errMsg
				events = append(events, backend.NewErrorEvent(errMsg))
				checkErrorLine(errMsg, &hasAuth, &hasRateLimit, &lastErr)
			}
		}
	}

	// 亦掃描 stderr
	stderrScanner := bufio.NewScanner(strings.NewReader(rawStderr))
	for stderrScanner.Scan() {
		checkErrorLine(stderrScanner.Text(), &hasAuth, &hasRateLimit, &lastErr)
	}

	extracted := strings.TrimSpace(sb.String())
	partialOutput = extracted

	if hasRateLimit {
		failureClass = backend.FailureRateLimit
		err = errors.New("backend error: rate limit exceeded")
		return "", partialOutput, events, failureClass, err
	}

	if hasAuth {
		failureClass = backend.FailureAuth
		err = errors.New("backend error: authentication failed")
		return "", partialOutput, events, failureClass, err
	}

	if lastErr != "" {
		failureClass = backend.FailureExecution
		err = fmt.Errorf("backend error: %s", lastErr)
		return "", partialOutput, events, failureClass, err
	}

	if extracted == "" {
		failureClass = backend.FailureEmptyResult
		err = errors.New("codex produced empty result")
		return "", "", events, failureClass, err
	}

	output = extracted
	failureClass = backend.FailureNone
	return output, partialOutput, events, failureClass, nil
}

func checkErrorLine(line string, hasAuth, hasRateLimit *bool, lastErr *string) {
	lower := strings.ToLower(line)
	if strings.Contains(lower, "rate limit") || strings.Contains(lower, "429") || strings.Contains(lower, "quota exceeded") {
		*hasRateLimit = true
	}
	if strings.Contains(lower, "unauthorized") || strings.Contains(lower, "invalid api key") || strings.Contains(lower, "authentication") || strings.Contains(lower, "login required") {
		*hasAuth = true
	}
	if strings.Contains(lower, "error=") || strings.Contains(lower, "error:") {
		if *lastErr == "" {
			*lastErr = line
		}
	}
}
