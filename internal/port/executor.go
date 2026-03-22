package port

import (
	"context"

	"github.com/patricksign/AgentClaw/internal/adapter"
)

// AgentPool abstracts agent lifecycle management.
// Implemented by internal/adapter.Pool.
//
// LEGACY: Uses adapter.Agent/adapter.Status/adapter.Task types because the Pool
// and Executor are not yet migrated to domain types. These interfaces will
// be updated when agent/ is migrated. See also: port/queue.go.
type AgentPool interface {
	Spawn(a adapter.Agent) error
	Kill(id string) error
	Restart(id string) error
	Get(id string) (adapter.Agent, bool)
	GetByRole(role string) []adapter.Agent
	All() []adapter.Agent
	StatusAll() map[string]adapter.Status
	ShutdownAll(ctx context.Context)
}

// TaskExecutor abstracts task execution on agents.
// Implemented by internal/adapter.Executor.
type TaskExecutor interface {
	Execute(ctx context.Context, task *adapter.Task) error
	ResumeTask(ctx context.Context, taskID string) error
}

// EventSubscriber abstracts event bus subscription (read side).
// Implemented by internal/adapter.EventBus.
type EventSubscriber interface {
	Subscribe(id string) (<-chan adapter.Event, func())
}

// EventPublisher abstracts event bus publishing (write side).
// Implemented by internal/adapter.EventBus.
type EventPublisher interface {
	Publish(evt adapter.Event)
}

// EventBus combines subscribe + publish for components that need both.
type EventBus interface {
	EventSubscriber
	EventPublisher
}
