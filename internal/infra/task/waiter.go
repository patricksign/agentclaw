package task

import (
	"context"
	"fmt"
	"sync"

	"github.com/patricksign/AgentClaw/internal/domain"
	"github.com/patricksign/AgentClaw/internal/port"
)

// Compile-time check.
var _ port.TaskResultWaiter = (*EventWaiter)(nil)

// EventWaiter implements port.TaskResultWaiter by subscribing to
// domain events and waiting for task.done / task.failed.
type EventWaiter struct {
	bus port.DomainEventBus

	mu      sync.Mutex
	waiters map[string]chan waitResult
	closed  bool

	unsubDone   func()
	unsubFailed func()
}

type waitResult struct {
	result *domain.TaskResult
	err    error
}

// NewEventWaiter creates a waiter and starts listening for task completion events.
func NewEventWaiter(bus port.DomainEventBus) *EventWaiter {
	w := &EventWaiter{
		bus:     bus,
		waiters: make(map[string]chan waitResult),
	}

	w.unsubDone = bus.Subscribe("event-waiter-done", []domain.EventType{domain.EventTaskDone}, w.handleDone)
	w.unsubFailed = bus.Subscribe("event-waiter-failed", []domain.EventType{domain.EventTaskFailed}, w.handleFailed)

	return w
}

// Stop unsubscribes from the event bus and signals all pending waiters
// so they don't block forever.
func (w *EventWaiter) Stop() {
	if w.unsubDone != nil {
		w.unsubDone()
	}
	if w.unsubFailed != nil {
		w.unsubFailed()
	}

	// Signal all pending waiters that no more events will arrive.
	w.mu.Lock()
	w.closed = true
	for taskID, ch := range w.waiters {
		select {
		case ch <- waitResult{err: fmt.Errorf("waiter stopped: task %s will not receive events", taskID)}:
		default:
		}
	}
	w.mu.Unlock()
}

// RegisterAndWait registers a wait channel for the task, returning a function
// that blocks until the task completes. The registration happens synchronously
// (before return), so calling Dispatch AFTER this function returns is race-free.
func (w *EventWaiter) RegisterAndWait(ctx context.Context, taskID string) func() (*domain.TaskResult, error) {
	ch := make(chan waitResult, 1)

	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return func() (*domain.TaskResult, error) {
			return nil, fmt.Errorf("waiter is stopped")
		}
	}
	w.waiters[taskID] = ch
	w.mu.Unlock()

	return func() (*domain.TaskResult, error) {
		defer func() {
			w.mu.Lock()
			delete(w.waiters, taskID)
			w.mu.Unlock()
		}()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case wr := <-ch:
			return wr.result, wr.err
		}
	}
}

// WaitForResult blocks until the task completes or the context expires.
// For race-free usage in pipeline, use RegisterAndWait before Dispatch.
func (w *EventWaiter) WaitForResult(ctx context.Context, taskID string) (*domain.TaskResult, error) {
	waitFn := w.RegisterAndWait(ctx, taskID)
	return waitFn()
}

func (w *EventWaiter) handleDone(evt domain.Event) {
	w.mu.Lock()
	ch, ok := w.waiters[evt.TaskID]
	w.mu.Unlock()

	if !ok {
		return
	}

	result := &domain.TaskResult{
		TaskID:    evt.TaskID,
		Output:    evt.Payload["output"],
		ModelUsed: evt.Model,
	}

	select {
	case ch <- waitResult{result: result}:
	default:
	}
}

func (w *EventWaiter) handleFailed(evt domain.Event) {
	w.mu.Lock()
	ch, ok := w.waiters[evt.TaskID]
	w.mu.Unlock()

	if !ok {
		return
	}

	reason := evt.Payload["reason"]
	if reason == "" {
		reason = evt.Payload["message"]
	}

	select {
	case ch <- waitResult{err: fmt.Errorf("task %s failed: %s", evt.TaskID, reason)}:
	default:
	}
}
