package port

import (
	"context"

	"github.com/patricksign/AgentClaw/internal/domain"
)

// TaskDispatcher submits a task for asynchronous execution.
// The actual execution is handled by a worker that picks from the queue.
type TaskDispatcher interface {
	Dispatch(ctx context.Context, task *domain.Task) error
}

// TaskResultWaiter waits for a specific task to complete.
// Decouples the "submit" concern from the "wait for result" concern.
type TaskResultWaiter interface {
	// WaitForResult registers a wait channel and blocks until the task
	// completes or ctx expires. For race-free usage, call before Dispatch.
	WaitForResult(ctx context.Context, taskID string) (*domain.TaskResult, error)
}
