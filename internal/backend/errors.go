package backend

import (
	"errors"
	"fmt"
)

var (
	ErrBackendUnavailable = errors.New("backend unavailable")
	// ErrBackendNotFound 代表請求之 Backend 未註冊
	ErrBackendNotFound = errors.New("backend not found")
	// ErrUnsupportedCapability 代表 Backend 缺少執行該任務或角色所需之必要能力
	ErrUnsupportedCapability = errors.New("unsupported capability")
	// ErrProcessCancelled 代表進程因 Context 取消而終止
	ErrProcessCancelled = errors.New("process execution cancelled")
)

// ExecutionError 代表後端執行階段發生之結構化錯誤，攜帶失敗分類與部分輸出
type ExecutionError struct {
	BackendID     string
	Classification FailureClassification
	Err           error
	PartialOutput string
}

func (e *ExecutionError) Error() string {
	if e.PartialOutput != "" {
		return fmt.Sprintf("backend %s failed (%s): %v [partial output preserved: %d bytes]", e.BackendID, e.Classification, e.Err, len(e.PartialOutput))
	}
	return fmt.Sprintf("backend %s failed (%s): %v", e.BackendID, e.Classification, e.Err)
}

func (e *ExecutionError) Unwrap() error {
	return e.Err
}

// NewExecutionError 建立標準 ExecutionError
func NewExecutionError(backendID string, class FailureClassification, err error, partialOutput string) *ExecutionError {
	return &ExecutionError{
		BackendID:     backendID,
		Classification: class,
		Err:           err,
		PartialOutput: partialOutput,
	}
}
