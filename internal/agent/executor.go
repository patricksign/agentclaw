package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/patricksign/agentclaw/internal/state"
	"github.com/rs/zerolog/log"
)

// MemoryStore interface — avoids circular import with the memory package.
// BuildContext returns a MemoryContext with []*Task to avoid copying mutex values.
type MemoryStore interface {
	BuildContext(agentID, role, taskTitle, complexity string) MemoryContext
	SaveTask(t *Task) error
	UpdateTaskStatus(id string, status TaskStatus) error
	AddTokens(taskID string, in, out int64, cost float64) error
	LogTokenUsage(taskID, agentID, model string, in, out int64, cost float64, durationMs int64) error
	// Resolved returns the ResolvedStore for error-pattern lookups. May be nil.
	Resolved() *state.ResolvedStore
	// AppendAgentDoc appends an outcome summary to the role memory file.
	// Implementations that do not support this may return nil silently.
	AppendAgentDoc(role, section string) error
	// AddScratchpadEntry appends an entry to the shared team scratchpad.
	AddScratchpadEntry(entry state.ScratchpadEntry) error
}

// Executor wires Pool + Queue + Memory + EventBus together for task execution.
type Executor struct {
	pool *Pool
	bus  *EventBus
	mem  MemoryStore
}

func NewExecutor(pool *Pool, bus *EventBus, mem MemoryStore) *Executor {
	return &Executor{pool: pool, bus: bus, mem: mem}
}

// Execute runs a task on the first available agent for the required role.
func (e *Executor) Execute(ctx context.Context, task *Task) error {
	// Snapshot all fields needed for this execution under a single lock to
	// avoid data races with concurrent readers (HTTP status endpoints, etc.).
	task.Lock()
	taskID := task.ID
	taskRole := task.AgentRole
	taskTitle := task.Title
	taskComplexity := task.Complexity
	task.Unlock()

	candidates := e.pool.GetByRole(taskRole)
	if len(candidates) == 0 {
		return fmt.Errorf("no agent available for role: %s", taskRole)
	}
	// Prefer the first idle agent (GetByRole returns idle first).
	a := candidates[0]
	agentID := a.Config().ID

	log.Info().
		Str("task", taskID).
		Str("agent", agentID).
		Str("model", a.Config().Model).
		Msg("executing task")

	// Atomically update task fields before handing off to the agent.
	now := time.Now()
	task.Lock()
	task.Status = TaskRunning
	task.StartedAt = &now
	task.AssignedTo = agentID
	task.Unlock()

	if err := e.mem.SaveTask(task); err != nil {
		log.Error().Err(err).Str("task", taskID).Msg("SaveTask failed")
	}
	if err := e.mem.UpdateTaskStatus(taskID, TaskRunning); err != nil {
		log.Error().Err(err).Str("task", taskID).Msg("UpdateTaskStatus(running) failed")
	}

	e.bus.Publish(Event{
		Type:    EvtTaskStarted,
		AgentID: agentID,
		TaskID:  taskID,
	})

	// Scratchpad: announce task start.
	_ = e.mem.AddScratchpadEntry(state.ScratchpadEntry{
		AgentID:   agentID,
		Kind:      state.KindInProgress,
		Message:   taskTitle,
		TaskID:    taskID,
		Timestamp: time.Now(),
	})

	// Build tiered memory context using snapshotted fields.
	memCtx := e.mem.BuildContext(agentID, taskRole, taskTitle, taskComplexity)

	// Run with per-agent timeout.
	timeout := time.Duration(a.Config().TimeoutSecs) * time.Second
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := a.Run(runCtx, task, memCtx)
	if err != nil {
		task.Lock()
		task.Status = TaskFailed
		task.Unlock()

		if dbErr := e.mem.UpdateTaskStatus(taskID, TaskFailed); dbErr != nil {
			log.Error().Err(dbErr).Str("task", taskID).Msg("UpdateTaskStatus(failed) failed")
		}

		// Check whether this error matches a known resolution pattern.
		// If so, append the resolution hint to the task's Blockers-style meta
		// so the Opus review endpoint can surface it immediately.
		if rs := e.mem.Resolved(); rs != nil {
			if matches, serr := rs.Search(err.Error(), taskRole); serr == nil && len(matches) > 0 {
				best := matches[0]
				log.Info().
					Str("task", taskID).
					Str("pattern_id", best.ID).
					Str("resolution", best.ResolutionSummary).
					Msg("known error pattern matched")

				hint := fmt.Sprintf("ERROR: %s\n\nKNOWN FIX: %s\nSee: %s",
					err.Error(), best.ResolutionSummary, best.DetailFile)
				task.Lock()
				if task.Meta == nil {
					task.Meta = make(map[string]string)
				}
				task.Meta["resolution_hint"] = hint
				task.Unlock()
			}
		}

		// Scratchpad: record failure.
		_ = e.mem.AddScratchpadEntry(state.ScratchpadEntry{
			AgentID:   agentID,
			Kind:      state.KindWarning,
			Message:   err.Error(),
			TaskID:    taskID,
			Timestamp: time.Now(),
		})

		e.bus.Publish(Event{
			Type:    EvtTaskFailed,
			AgentID: agentID,
			TaskID:  taskID,
			Payload: err.Error(),
		})
		log.Error().Err(err).Str("task", taskID).Msg("task failed")
		return err
	}

	// Log token usage.
	if result != nil {
		addErr := e.mem.AddTokens(taskID, result.InputTokens, result.OutputTokens, result.CostUSD)
		if addErr != nil {
			log.Error().Err(addErr).Str("task", taskID).Msg("AddTokens failed")
		}
		logErr := e.mem.LogTokenUsage(
			taskID, agentID, a.Config().Model,
			result.InputTokens, result.OutputTokens,
			result.CostUSD, result.DurationMs,
		)
		if logErr != nil {
			log.Error().Err(logErr).Str("task", taskID).Msg("LogTokenUsage failed")
			if addErr == nil {
				log.Warn().Str("task", taskID).Msg("token accounting inconsistent: AddTokens succeeded but LogTokenUsage failed")
			}
		}
		e.bus.Publish(Event{
			Type:    EvtTokenLogged,
			AgentID: agentID,
			TaskID:  taskID,
			Payload: result,
		})
	}

	finished := time.Now()
	task.Lock()
	task.Status = TaskDone
	task.FinishedAt = &finished
	task.Unlock()

	if err := e.mem.UpdateTaskStatus(taskID, TaskDone); err != nil {
		log.Error().Err(err).Str("task", taskID).Msg("UpdateTaskStatus(done) failed")
	}

	e.bus.Publish(Event{
		Type:    EvtTaskDone,
		AgentID: agentID,
		TaskID:  taskID,
		Payload: result,
	})

	if result != nil {
		log.Info().
			Str("task", taskID).
			Float64("cost", result.CostUSD).
			Int64("tokens", result.InputTokens+result.OutputTokens).
			Msg("task done")
	}

	// Scratchpad: record handoff with next-role hint.
	_ = e.mem.AddScratchpadEntry(state.ScratchpadEntry{
		AgentID:   agentID,
		Kind:      state.KindHandoff,
		Message:   "done, next: " + nextRole(taskRole),
		TaskID:    taskID,
		Timestamp: time.Now(),
	})

	// Persist a one-line outcome summary to the agent's role memory doc for
	// architect/idea roles and for tasks tagged architecture, milestone, or adr.
	if isMemoryWorthy(task) {
		var cost float64
		if result != nil {
			cost = result.CostUSD
		}
		summary := fmt.Sprintf("**[%s] %s** — done (cost $%.4f)", taskID, taskTitle, cost)
		if aerr := e.mem.AppendAgentDoc(taskRole, summary); aerr != nil {
			log.Warn().Err(aerr).Str("task", taskID).Msg("AppendAgentDoc failed")
		}
	}

	return nil
}

// nextRole returns the conventional downstream role for a given upstream role.
func nextRole(role string) string {
	next := map[string]string{
		"idea":      "architect",
		"architect": "breakdown",
		"breakdown": "coding",
		"coding":    "test",
		"test":      "review",
		"review":    "deploy",
		"deploy":    "notify",
		"notify":    "—",
		"docs":      "review",
	}
	if n, ok := next[role]; ok {
		return n
	}
	return "—"
}

// isMemoryWorthy reports whether a completed task's outcome should be appended
// to the role's agent doc for future reference.
func isMemoryWorthy(task *Task) bool {
	task.Lock()
	role := task.AgentRole
	tags := task.Tags
	task.Unlock()

	if role == "architect" || role == "idea" {
		return true
	}
	for _, tag := range tags {
		switch strings.ToLower(tag) {
		case "architecture", "milestone", "adr":
			return true
		}
	}
	return false
}
