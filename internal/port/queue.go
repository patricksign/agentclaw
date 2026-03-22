package port

import (
	"context"

	"github.com/patricksign/AgentClaw/internal/adapter"
)

// TaskQueue abstracts the priority task queue.
// Implemented by internal/queue.Queue.
//
// LEGACY: This interface uses agent.Task because the queue, executor, and pool
// are not yet migrated to domain types. When agent/ is migrated to use
// domain.Task, this interface will be updated to use domain.Task as well.
// See also: port/executor.go (same legacy constraint).
type TaskQueue interface {
	Push(task *adapter.Task)
	Pop(ctx context.Context, role string) (*adapter.Task, error)
	MarkDone(taskID string)
	MarkFailed(task *adapter.Task, maxRetries int)
}
