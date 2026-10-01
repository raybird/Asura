package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"asura/internal/contextwin"
	"asura/internal/engine"
	"asura/internal/pipeline"
	"asura/internal/recall"
	"asura/internal/storage"
)

func recallFingerprint(workspace, prompt, mode string, ids []string, backend string) string {
	payload := struct {
		Domain      string
		Workspace   string
		Prompt      string
		Mode        string
		Backend     string
		ExplicitIDs []string
		Recall      string
	}{"asura-recall-request-v1", workspace, prompt, mode, backend, append([]string{}, ids...), "bounded"}
	encoded, _ := json.Marshal(payload)
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

func (d *Daemon) recallMemories(workspace, prompt, mode string, ids []string, explicit []storage.Memory) (recall.Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), recall.Deadline)
	defer cancel()
	candidates, truncated, err := d.store.RecallCandidates(ctx, workspace)
	if err != nil {
		return recall.Result{}, err
	}
	utilityPolicy := d.cfg.UtilityPolicy
	if utilityPolicy == "" {
		utilityPolicy = "observe"
	}
	var aggregates map[string]storage.UtilityAggregate
	if utilityPolicy == "apply" {
		aggregateIDs := make([]string, 0, len(candidates))
		explicitSet := map[string]bool{}
		for _, id := range ids {
			explicitSet[id] = true
		}
		for _, m := range explicit {
			explicitSet[m.ID] = true
		}
		for _, m := range candidates {
			if !explicitSet[m.ID] {
				aggregateIDs = append(aggregateIDs, m.ID)
			}
		}
		aggregates, err = d.store.UtilityAggregates(ctx, workspace, aggregateIDs)
		if err != nil {
			return recall.Result{}, err
		}
	}
	estimator := contextwin.NewHeuristicEstimator(d.cfg.ContextConfig.ReserveRatio)
	admission := contextwin.NewAdmissionEngine(contextwin.NewStaticCatalog(), estimator)
	base := memoryContext(workspace, prompt, explicit)
	basePrompt := engine.ComposePrompt(prompt, base)
	baseTokens := estimator.Estimate("recall", basePrompt).EstimatedPromptTokens
	measure := func(auto []storage.Memory) recall.Budget {
		memories := append(append([]storage.Memory(nil), explicit...), auto...)
		sealed := memoryContext(workspace, prompt, memories)
		composed := engine.ComposePrompt(prompt, sealed)
		stage, stagePrompt := "striker", composed
		if mode == "three-face" {
			prepared, _ := pipeline.NewOracleStage(nil).Prepare(sealed)
			stage, stagePrompt = "oracle", prepared.Prompt
		}
		return recall.Budget{
			FormattedBytes:   len(composed) - len(basePrompt),
			EstimatedTokens:  estimator.Estimate("recall", composed).EstimatedPromptTokens - baseTokens,
			EstimationMethod: contextwin.EstimationMethodHeuristicV1,
			FirstStage:       admission.EvaluateStage(stage, stagePrompt, "", d.cfg.ContextConfig),
		}
	}
	result, err := recall.Select(ctx, recall.Input{UtilityPolicy: utilityPolicy, UtilityAggregates: aggregates, Workspace: workspace, Prompt: prompt, ExplicitIDs: ids, Candidates: candidates,
		CandidateTruncated: truncated, ContextConfig: d.cfg.ContextConfig, Measure: measure})
	if err != nil {
		return recall.Result{}, err
	}
	result.Decision.RecallID = engine.GenerateTaskID()
	result.Decision.UtilityPolicy = utilityPolicy
	result.Decision.UtilityVersion = storage.UtilityVersion
	return result, nil
}

func memoryContext(workspace, prompt string, memories []storage.Memory) *engine.ExecutionContext {
	result := &engine.ExecutionContext{Workspace: workspace, OriginalPrompt: prompt}
	for _, m := range memories {
		result.ProjectMemories = append(result.ProjectMemories, engine.ProjectMemorySnapshot{
			ID: m.ID, Workspace: m.Workspace, Content: m.Content, Source: m.Source, CreatedAt: m.CreatedAt,
		})
	}
	return result
}
