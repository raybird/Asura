package antigravity

import (
	"asura/internal/backend"
	"bufio"
	"encoding/json"
	"errors"
	"strings"
)

// ExtractResultAndEvents 2026-09-30：SUCCESS 與非空結果不能遮蔽 native timeout。
func ExtractResultAndEvents(stdout, stderr string) (*backend.ExecutionResult, error) {
	r := &backend.ExecutionResult{UsageSource: backend.ProvenanceUnknown}
	var partial strings.Builder
	resultCount := 0
	status := ""
	failure := ""
	invalid := false
	scan := bufio.NewScanner(strings.NewReader(stdout))
	scan.Buffer(make([]byte, 65536), 8*1024*1024)
	for scan.Scan() {
		if strings.TrimSpace(scan.Text()) == "" {
			continue
		}
		var e struct {
			Event string `json:"event"`
			Step  struct {
				Type  string `json:"step_type"`
				Text  string `json:"text_delta"`
				Tool  string `json:"tool_name"`
				State string `json:"state"`
			} `json:"step_update"`
			Result struct {
				Status   string           `json:"status"`
				Response string           `json:"response"`
				Error    string           `json:"error"`
				Usage    map[string]int64 `json:"usage"`
			} `json:"result"`
		}
		if json.Unmarshal(scan.Bytes(), &e) != nil {
			invalid = true
			continue
		}
		switch e.Event {
		case "step_update":
			if e.Step.Type == "agent_response" {
				partial.WriteString(e.Step.Text)
				if e.Step.Text != "" {
					r.Events = append(r.Events, backend.NewTextEvent(e.Step.Text))
				}
			}
			if e.Step.Tool != "" {
				r.Events = append(r.Events, backend.NewToolEvent(e.Step.Tool, e.Step.State))
			}
		case "result":
			resultCount++
			status = e.Result.Status
			failure = e.Result.Error
			r.PartialOutput = e.Result.Response
			if e.Result.Usage != nil {
				u := &backend.TokenUsage{}
				if v, ok := e.Result.Usage["input_tokens"]; ok {
					u.InputTokens = &v
				}
				if v, ok := e.Result.Usage["output_tokens"]; ok {
					u.OutputTokens = &v
				}
				if v, ok := e.Result.Usage["thinking_tokens"]; ok {
					u.ReasoningTokens = &v
				}
				if v, ok := e.Result.Usage["cache_read_tokens"]; ok {
					u.CachedTokens = &v
				}
				if v, ok := e.Result.Usage["total_tokens"]; ok {
					u.TotalTokens = &v
				}
				r.Usage = u
				r.UsageSource = backend.ProvenanceExact
			}
		}
	}
	if r.PartialOutput == "" {
		r.PartialOutput = partial.String()
	}
	if r.PartialOutput != "" && partial.Len() == 0 {
		r.Events = append(r.Events, backend.NewTextEvent(r.PartialOutput))
	}
	if scan.Err() != nil || invalid {
		failure = "invalid Antigravity event stream"
	}
	timeout := strings.Contains(stderr, "print timeout after ") && strings.Contains(stderr, "returning partial output")
	if timeout {
		failure = "Antigravity returned a partial result after timeout"
		r.UsageSource = backend.ProvenanceUnknown
		r.Usage = nil
	}
	carrierClass := backend.FailureExecution
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "AGY_ERROR: ") {
			var carrier struct {
				Status string `json:"status"`
				Kind   string `json:"code_kind"`
				Code   int    `json:"error_code"`
			}
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "AGY_ERROR: ")), &carrier) == nil {
				failure = "Antigravity terminal error"
				if carrier.Status == "UNAUTHENTICATED" || carrier.Kind == "grpc" && carrier.Code == 16 {
					carrierClass = backend.FailureAuth
				}
				if carrier.Status == "RESOURCE_EXHAUSTED" || carrier.Kind == "grpc" && carrier.Code == 8 {
					carrierClass = backend.FailureRateLimit
				}
			}
		}
	}
	if resultCount != 1 || status != "SUCCESS" || failure != "" {
		r.FailureClass = carrierClass
		// 2026-09-30：只依已知 terminal carrier 分類；unknown 保持 execution failure。
		if strings.Contains(failure, "authentication failed or timed out") {
			r.FailureClass = backend.FailureAuth
		}

		if failure == "" {
			failure = "Antigravity result incomplete or unsuccessful"
		}
		r.Events = append(r.Events, backend.NewErrorEvent(failure))
		return r, errors.New(failure)
	}
	if strings.TrimSpace(r.PartialOutput) == "" {
		r.FailureClass = backend.FailureEmptyResult
		return r, errors.New("Antigravity result empty")
	}
	r.Output = r.PartialOutput
	return r, nil
}
