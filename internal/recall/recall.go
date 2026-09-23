package recall

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"asura/internal/contextwin"
	"asura/internal/storage"
)

const PolicyVersion = "lexical-v1"
const Deadline = 250 * time.Millisecond

type Policy struct {
	MaxCandidates   int   `json:"max_candidates"`
	MaxContentBytes int   `json:"max_content_bytes"`
	MaxQueryBytes   int   `json:"max_query_bytes"`
	MaxQueryTerms   int   `json:"max_query_terms"`
	MaxItems        int   `json:"max_items"`
	MaxBytes        int   `json:"max_bytes"`
	MaxTokens       int64 `json:"max_estimated_tokens"`
}

func DefaultPolicy() Policy {
	return Policy{MaxCandidates: 256, MaxContentBytes: 8192, MaxQueryBytes: 4096,
		MaxQueryTerms: 128, MaxItems: 4, MaxBytes: 8192, MaxTokens: 2048}
}

type Selection struct {
	ID     string `json:"id"`
	Score  int    `json:"score"`
	Reason string `json:"reason"`
}

type Budget struct {
	FormattedBytes   int                       `json:"formatted_bytes"`
	EstimatedTokens  int64                     `json:"estimated_prompt_tokens"`
	EstimationMethod string                    `json:"estimation_method"`
	FirstStage       contextwin.StageAdmission `json:"first_stage"`
}

type Decision struct {
	Mode               string            `json:"mode"`
	PolicyVersion      string            `json:"policy_version"`
	Policy             Policy            `json:"policy"`
	ExplicitIDs        []string          `json:"explicit_ids"`
	AutoIDs            []string          `json:"auto_ids"`
	Selected           []Selection       `json:"selected"`
	CandidatesRead     int               `json:"candidates_read"`
	CandidatesScored   int               `json:"candidates_scored"`
	CandidateTruncated bool              `json:"candidate_truncated"`
	QueryTruncated     bool              `json:"query_truncated"`
	Skipped            map[string]int    `json:"skipped"`
	Budget             Budget            `json:"budget"`
	ContextConfig      contextwin.Config `json:"context_config"`
}

type Input struct {
	Workspace          string
	Prompt             string
	ExplicitIDs        []string
	Candidates         []storage.Memory
	CandidateTruncated bool
	ContextConfig      contextwin.Config
	Measure            func([]storage.Memory) Budget
}

type Result struct {
	Memories []storage.Memory
	Decision Decision
}

// Select 使用 2026-09-22 核准的固定政策；Measure 僅計算真實 prompt，不執行後端。
func Select(ctx context.Context, in Input) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	policy := DefaultPolicy()
	result := Result{Decision: Decision{Mode: "bounded", PolicyVersion: PolicyVersion, Policy: policy,
		ExplicitIDs: append([]string{}, in.ExplicitIDs...), AutoIDs: []string{}, Selected: []Selection{},
		CandidateTruncated: in.CandidateTruncated, Skipped: map[string]int{}, ContextConfig: in.ContextConfig}}
	d := &result.Decision
	prompt := in.Prompt
	if len(prompt) > policy.MaxQueryBytes {
		end := policy.MaxQueryBytes
		for end > 0 && !utf8.RuneStart(prompt[end]) {
			end--
		}
		prompt = prompt[:end]
		d.QueryTruncated = true
	}
	query := map[string]bool{}
	for _, term := range terms(prompt) {
		if query[term] {
			continue
		}
		if len(query) == policy.MaxQueryTerms {
			d.QueryTruncated = true
			break
		}
		query[term] = true
	}
	explicit := map[string]bool{}
	for _, id := range in.ExplicitIDs {
		explicit[id] = true
	}
	candidates := append([]storage.Memory(nil), in.Candidates...)
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
	if len(candidates) > policy.MaxCandidates {
		candidates = candidates[:policy.MaxCandidates]
		d.CandidateTruncated = true
	}
	d.CandidatesRead = len(candidates)
	type scored struct {
		memory storage.Memory
		score  int
	}
	ranked := []scored{}
	for _, m := range candidates {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		reason := ""
		switch {
		case m.Workspace != in.Workspace:
			reason = "invalid_metadata"
		case explicit[m.ID]:
			reason = "duplicate"
		case len(m.ID) > 256 || len(m.Source) > 128 || len(m.Content) > policy.MaxContentBytes:
			reason = "oversize"
		case m.ID == "" || m.CreatedAt.IsZero() || !utf8.ValidString(m.ID) || !utf8.ValidString(m.Source) || !utf8.ValidString(m.Content):
			reason = "invalid_metadata"
		}
		if reason != "" {
			d.Skipped[reason]++
			continue
		}
		d.CandidatesScored++
		score := 0
		matched := map[string]bool{}
		for _, term := range terms(m.Content) {
			if query[term] && !matched[term] {
				score++
				matched[term] = true
			}
		}
		if score == 0 {
			d.Skipped["no_match"]++
			continue
		}
		ranked = append(ranked, scored{m, score})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].memory.ID < ranked[j].memory.ID
	})
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	d.Budget = in.Measure(nil)
	for _, candidate := range ranked {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if len(result.Memories) == policy.MaxItems {
			d.Skipped["item_limit"]++
			continue
		}
		trial := append(append([]storage.Memory(nil), result.Memories...), candidate.memory)
		budget := in.Measure(trial)
		reason := ""
		switch {
		case budget.FormattedBytes > policy.MaxBytes:
			reason = "byte_limit"
		case budget.EstimatedTokens > policy.MaxTokens:
			reason = "token_limit"
		case budget.FirstStage.MaxTokens > 0 && budget.FirstStage.Estimate.TotalEstimatedTokens > budget.FirstStage.MaxTokens:
			reason = "stage_limit"
		}
		if reason != "" {
			d.Skipped[reason]++
			continue
		}
		result.Memories = trial
		d.Budget = budget
		d.AutoIDs = append(d.AutoIDs, candidate.memory.ID)
		d.Selected = append(d.Selected, Selection{ID: candidate.memory.ID, Score: candidate.score, Reason: "lexical_overlap"})
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return result, nil
}

func terms(text string) []string {
	out := []string{}
	var run []rune
	flush := func() {
		if len(run) > 0 && unicode.Is(unicode.Han, run[0]) {
			for i := 1; i < len(run); i++ {
				out = append(out, string(run[i-1:i+1]))
			}
		} else if len(run) >= 2 {
			out = append(out, string(run))
		}
		run = nil
	}
	for _, r := range strings.ToLower(text) {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if len(run) > 0 && unicode.Is(unicode.Han, r) != unicode.Is(unicode.Han, run[0]) {
			flush()
		}
		run = append(run, r)
	}
	flush()
	return out
}
