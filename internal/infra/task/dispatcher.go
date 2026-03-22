package task

import (
	"context"
	"time"

	"github.com/patricksign/AgentClaw/internal/domain"
	"github.com/patricksign/AgentClaw/internal/port"
)

// Compile-time check.
var _ port.TaskDispatcher = (*QueueDispatcher)(nil)

// QueueDispatcher implements port.TaskDispatcher by publishing a domain event.
// The event is picked up by TaskDeliverySubscriber which routes to the queue.
// This dispatcher also supports direct queue push for the legacy path.
type QueueDispatcher struct {
	bus port.DomainEventBus
}

// NewQueueDispatcher creates a dispatcher that publishes task events.
func NewQueueDispatcher(bus port.DomainEventBus) *QueueDispatcher {
	return &QueueDispatcher{bus: bus}
}

// Dispatch publishes EventTaskSubmitted so subscribers can route the task.
func (d *QueueDispatcher) Dispatch(ctx context.Context, task *domain.Task) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	if d.bus != nil {
		d.bus.Publish(domain.Event{
			Type:      domain.EventTaskSubmitted,
			Channel:   domain.StatusChannel,
			TaskID:    task.ID,
			AgentRole: task.AgentRole,
			Payload: map[string]string{
				"title":       task.Title,
				"description": task.Description,
				"complexity":  task.Complexity,
				"status":      task.Status,
			},
			OccurredAt: time.Now(),
		})
	}
	return nil
}
