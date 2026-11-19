package omp

import (
	"asura/internal/backend"
	"asura/internal/backend/clisandbox"
)

func ExtractResultAndEvents(stdout, stderr string) (*backend.ExecutionResult, error) {
	return clisandbox.ParseAgentEvents("OMP", stdout, stderr)
}
