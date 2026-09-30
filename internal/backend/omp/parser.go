package omp

import (
	"asura/internal/backend"
	"bufio"
	"encoding/json"
	"errors"
	"strings"
)

type message struct {
	Role         string `json:"role"`
	StopReason   string `json:"stopReason"`
	ErrorMessage string `json:"errorMessage"`
	Content      []struct {
		Type string `json:"type"`
		Text string `json:"text"`
		Name string `json:"name"`
	} `json:"content"`
}

func textOf(m message) string {
	var s strings.Builder
	for _, c := range m.Content {
		if c.Type == "text" {
			s.WriteString(c.Text)
		}
	}
	return s.String()
}

// ExtractResultAndEvents 2026-09-30：BADAPT-007，raw format 的判斷限於 adapter。
func ExtractResultAndEvents(stdout, stderr string) (*backend.ExecutionResult, error) {
	r := &backend.ExecutionResult{UsageSource: backend.ProvenanceUnknown}
	var committed strings.Builder
	pending := ""
	complete := false
	failure := ""
	scan := bufio.NewScanner(strings.NewReader(stdout))
	scan.Buffer(make([]byte, 65536), 8*1024*1024)
	for scan.Scan() {
		if strings.TrimSpace(scan.Text()) == "" {
			continue
		}
		var e struct {
			Type             string  `json:"type"`
			Message          message `json:"message"`
			AssistantMessage message `json:"assistantMessage"`
		}
		if json.Unmarshal(scan.Bytes(), &e) != nil {
			failure = "invalid OMP event"
			continue
		}
		switch e.Type {
		case "message_update":
			if e.AssistantMessage.Role == "assistant" {
				pending = textOf(e.AssistantMessage)
			}
		case "message_end":
			if e.Message.Role != "assistant" {
				continue
			}
			pending = ""
			text := textOf(e.Message)
			committed.WriteString(text)
			if text != "" {
				r.Events = append(r.Events, backend.NewTextEvent(text))
			}
			for _, c := range e.Message.Content {
				if c.Type == "toolCall" {
					r.Events = append(r.Events, backend.NewToolEvent(c.Name, "requested"))
				}
			}
			if e.Message.StopReason == "error" || e.Message.StopReason == "aborted" {
				failure = e.Message.ErrorMessage
				if failure == "" {
					failure = "OMP message failed"
				}
			}
			if e.Message.StopReason == "stop" {
				complete = true
			}
		case "auto_retry_start":
			failure = "unexpected CLI retry"
		}
	}
	if scan.Err() != nil {
		failure = "OMP event stream could not be read"
	}
	r.PartialOutput = committed.String() + pending
	if failure == "" && strings.TrimSpace(stderr) != "" {
		failure = "OMP execution failed"
	}
	if failure != "" {
		r.FailureClass = backend.FailureExecution
		switch failure {
		case "ChatGPT rate limit exceeded.":
			r.FailureClass = backend.FailureRateLimit
		}
		if strings.Contains(failure, "No API key found") || strings.Contains(failure, "authentication") || strings.Contains(failure, "Unauthorized") {
			r.FailureClass = backend.FailureAuth
		}
		r.Events = append(r.Events, backend.NewErrorEvent(failure))
		return r, errors.New(failure)
	}
	if !complete {
		r.FailureClass = backend.FailureExecution
		return r, errors.New("OMP result incomplete")
	}
	if strings.TrimSpace(r.PartialOutput) == "" {
		r.FailureClass = backend.FailureEmptyResult
		return r, errors.New("OMP result empty")
	}
	r.Output = r.PartialOutput
	return r, nil
}
