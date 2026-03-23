package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/patricksign/AgentClaw/internal/domain"
	"github.com/patricksign/AgentClaw/internal/port"
)

var _ port.SystemState = (*PGState)(nil)

type PGState struct {
	pool *pgxpool.Pool
}

func NewPGState(pool *pgxpool.Pool) *PGState {
	return &PGState{pool: pool}
}

// ─── Tasks ──────────────────────────────────────────────────────────────────

func (s *PGState) UpsertTask(ctx context.Context, t *domain.Task) error {
	questions, _ := json.Marshal(t.Questions)

	_, err := s.pool.Exec(ctx, `
		INSERT INTO tasks (
			id, title, description, agent_id, agent_role, status, phase,
			complexity, tags, understanding, assumptions, risks, questions,
			implement_plan, plan_approved_by, redirect_count, phase_started_at,
			output, input_tokens, output_tokens, cost_usd, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23
		) ON CONFLICT (id) DO UPDATE SET
			title=$2, description=$3, agent_id=$4, agent_role=$5, status=$6, phase=$7,
			complexity=$8, tags=$9, understanding=$10, assumptions=$11, risks=$12,
			questions=$13, implement_plan=$14, plan_approved_by=$15, redirect_count=$16,
			phase_started_at=$17, output=$18, input_tokens=$19, output_tokens=$20,
			cost_usd=$21, updated_at=$23`,
		t.ID, t.Title, t.Description, t.AgentID, t.AgentRole, t.Status, string(t.Phase),
		t.Complexity, t.Tags, t.Understanding, t.Assumptions, t.Risks, questions,
		t.ImplementPlan, t.PlanApprovedBy, t.RedirectCount, t.PhaseStartedAt,
		t.Output, t.InputTokens, t.OutputTokens, t.CostUSD, t.CreatedAt,
		time.Now(),
	)
	return err
}

func (s *PGState) GetTask(ctx context.Context, taskID string) (*domain.Task, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, title, description, agent_id, agent_role, status, phase,
		       complexity, tags, understanding, assumptions, risks, questions,
		       implement_plan, plan_approved_by, redirect_count, phase_started_at,
		       output, input_tokens, output_tokens, cost_usd, created_at, updated_at
		FROM tasks WHERE id = $1`, taskID)
	return scanPGTask(row)
}

func (s *PGState) ListTasks(ctx context.Context, f port.TaskFilter) ([]domain.Task, error) {
	query := `SELECT id, title, description, agent_id, agent_role, status, phase,
	                 complexity, tags, understanding, assumptions, risks, questions,
	                 implement_plan, plan_approved_by, redirect_count, phase_started_at,
	                 output, input_tokens, output_tokens, cost_usd, created_at, updated_at
	          FROM tasks WHERE 1=1`
	args := []interface{}{}
	idx := 1

	if f.AgentID != "" {
		query += fmt.Sprintf(" AND agent_id = $%d", idx)
		args = append(args, f.AgentID)
		idx++
	}
	if f.Status != "" {
		query += fmt.Sprintf(" AND status = $%d", idx)
		args = append(args, f.Status)
		idx++
	}
	if f.Phase != "" {
		query += fmt.Sprintf(" AND phase = $%d", idx)
		args = append(args, f.Phase)
		idx++
	}
	if f.Role != "" {
		query += fmt.Sprintf(" AND agent_role = $%d", idx)
		args = append(args, f.Role)
		idx++
	}

	query += " ORDER BY updated_at DESC"

	if f.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", idx)
		args = append(args, f.Limit)
		idx++
	}
	if f.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", idx)
		args = append(args, f.Offset)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var tasks []domain.Task
	for rows.Next() {
		t, err := scanPGTaskRow(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, *t)
	}
	return tasks, rows.Err()
}

func (s *PGState) UpdateTaskStatus(ctx context.Context, taskID, status string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE tasks SET status = $1, updated_at = $2 WHERE id = $3`,
		status, time.Now(), taskID)
	return err
}

// ─── Phase Checkpoints (explicit columns, not JSON blob) ────────────────────

func (s *PGState) SaveCheckpoint(ctx context.Context, cp *domain.PhaseCheckpoint) error {
	accumulated, _ := json.Marshal(cp.Accumulated)
	lastMsgs, _ := json.Marshal(cp.LastMessages)

	_, err := s.pool.Exec(ctx, `
		INSERT INTO phase_checkpoints (
			task_id, agent_id, phase, step_index, step_name,
			accumulated, pending_query, pending_query_id,
			suspended_model, escalated_to,
			last_system_prompt, last_messages, last_response,
			input_tokens, output_tokens, cost_usd, saved_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		ON CONFLICT (task_id) DO UPDATE SET
			agent_id=$2, phase=$3, step_index=$4, step_name=$5,
			accumulated=$6, pending_query=$7, pending_query_id=$8,
			suspended_model=$9, escalated_to=$10,
			last_system_prompt=$11, last_messages=$12, last_response=$13,
			input_tokens=$14, output_tokens=$15, cost_usd=$16, saved_at=$17`,
		cp.TaskID, cp.AgentID, string(cp.Phase), cp.StepIndex, cp.StepName,
		accumulated, cp.PendingQuery, cp.PendingQueryID,
		cp.SuspendedModel, cp.EscalatedTo,
		cp.LastSystemPrompt, lastMsgs, cp.LastResponse,
		cp.InputTokens, cp.OutputTokens, cp.CostUSD, time.Now(),
	)
	return err
}

func (s *PGState) LoadCheckpoint(ctx context.Context, taskID string) (*domain.PhaseCheckpoint, error) {
	var cp domain.PhaseCheckpoint
	var accumulated, lastMsgs []byte
	var phase string

	err := s.pool.QueryRow(ctx, `
		SELECT task_id, agent_id, phase, step_index, step_name,
		       accumulated, pending_query, pending_query_id,
		       suspended_model, escalated_to,
		       last_system_prompt, last_messages, last_response,
		       input_tokens, output_tokens, cost_usd, saved_at
		FROM phase_checkpoints WHERE task_id = $1`, taskID).Scan(
		&cp.TaskID, &cp.AgentID, &phase, &cp.StepIndex, &cp.StepName,
		&accumulated, &cp.PendingQuery, &cp.PendingQueryID,
		&cp.SuspendedModel, &cp.EscalatedTo,
		&cp.LastSystemPrompt, &lastMsgs, &cp.LastResponse,
		&cp.InputTokens, &cp.OutputTokens, &cp.CostUSD, &cp.SavedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("load checkpoint: %w", err)
	}

	cp.Phase = domain.ExecutionPhase(phase)

	if len(accumulated) > 0 {
		if err := json.Unmarshal(accumulated, &cp.Accumulated); err != nil {
			return nil, fmt.Errorf("unmarshal checkpoint accumulated: %w", err)
		}
	}
	if len(lastMsgs) > 0 {
		if err := json.Unmarshal(lastMsgs, &cp.LastMessages); err != nil {
			return nil, fmt.Errorf("unmarshal checkpoint last_messages: %w", err)
		}
	}
	return &cp, nil
}

func (s *PGState) DeleteCheckpoint(ctx context.Context, taskID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM phase_checkpoints WHERE task_id = $1`, taskID)
	return err
}

// ─── Token & Cost Logs ──────────────────────────────────────────────────────

func (s *PGState) AppendTokenLog(ctx context.Context, log port.TokenLog) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO token_logs (
			task_id, agent_id, agent_role, model, phase,
			input_tokens, output_tokens, cache_tokens,
			cost_usd, cost_mode, logged_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		log.TaskID, log.AgentID, log.AgentRole, log.Model, log.Phase,
		log.InputTokens, log.OutputTokens, log.CacheTokens,
		log.CostUSD, log.CostMode, log.LoggedAt)
	return err
}

func (s *PGState) GetCostByPeriod(ctx context.Context, from, to string) ([]port.CostRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT agent_id, model, DATE(logged_at)::TEXT AS date,
		       SUM(cost_usd)::FLOAT8 AS total_cost,
		       SUM(input_tokens) AS total_in,
		       SUM(output_tokens) AS total_out
		FROM token_logs
		WHERE logged_at >= $1::TIMESTAMPTZ AND logged_at <= $2::TIMESTAMPTZ
		GROUP BY agent_id, model, DATE(logged_at)
		ORDER BY date DESC`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []port.CostRow
	for rows.Next() {
		var r port.CostRow
		if err := rows.Scan(&r.AgentID, &r.Model, &r.Date, &r.TotalCost, &r.TotalIn, &r.TotalOut); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *PGState) GetTodayCost(ctx context.Context, agentID string) (float64, error) {
	var cost float64
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(cost_usd)::FLOAT8, 0)
		FROM token_logs
		WHERE agent_id = $1 AND logged_at >= CURRENT_DATE`, agentID).Scan(&cost)
	return cost, err
}

// ─── Users ──────────────────────────────────────────────────────────────────

func (s *PGState) UpsertUser(ctx context.Context, u port.User) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO users (id, email, name, tenant_id, plan, created_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (id) DO UPDATE SET email=$2, name=$3, tenant_id=$4, plan=$5`,
		u.ID, u.Email, u.Name, u.TenantID, u.Plan, u.CreatedAt)
	return err
}

func (s *PGState) GetUser(ctx context.Context, userID string) (*port.User, error) {
	var u port.User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, name, tenant_id, plan, created_at
		FROM users WHERE id = $1`, userID).
		Scan(&u.ID, &u.Email, &u.Name, &u.TenantID, &u.Plan, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

// ─── Event Audit Log (append-only) ─────────────────────────────────────────

func (s *PGState) AppendEvent(ctx context.Context, evt domain.Event) error {
	payload, _ := json.Marshal(evt.Payload)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO event_log (type, channel, task_id, agent_id, agent_role, model, payload, occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		string(evt.Type), int(evt.Channel), evt.TaskID, evt.AgentID,
		evt.AgentRole, evt.Model, payload, evt.OccurredAt)
	return err
}

func (s *PGState) QueryEvents(ctx context.Context, taskID string, limit int) ([]domain.Event, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT type, channel, task_id, agent_id, agent_role, model, payload, occurred_at
		FROM event_log WHERE task_id = $1
		ORDER BY occurred_at DESC LIMIT $2`, taskID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []domain.Event
	for rows.Next() {
		var e domain.Event
		var payloadBytes []byte
		var ch int
		var evtType string
		if err := rows.Scan(&evtType, &ch, &e.TaskID, &e.AgentID, &e.AgentRole, &e.Model, &payloadBytes, &e.OccurredAt); err != nil {
			return nil, err
		}
		e.Type = domain.EventType(evtType)
		e.Channel = domain.Channel(ch)
		if len(payloadBytes) > 0 {
			if err := json.Unmarshal(payloadBytes, &e.Payload); err != nil {
				slog.Warn("unmarshal event payload failed", "task_id", e.TaskID, "err", err)
			}
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// ─── Task scan helpers ──────────────────────────────────────────────────────

func scanPGTask(row pgx.Row) (*domain.Task, error) {
	var t domain.Task
	var phase string
	var questions []byte

	err := row.Scan(
		&t.ID, &t.Title, &t.Description, &t.AgentID, &t.AgentRole,
		&t.Status, &phase, &t.Complexity, &t.Tags, &t.Understanding,
		&t.Assumptions, &t.Risks, &questions,
		&t.ImplementPlan, &t.PlanApprovedBy, &t.RedirectCount, &t.PhaseStartedAt,
		&t.Output, &t.InputTokens, &t.OutputTokens, &t.CostUSD,
		&t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan task: %w", err)
	}

	t.Phase = domain.ExecutionPhase(phase)
	if len(questions) > 0 {
		if err := json.Unmarshal(questions, &t.Questions); err != nil {
			return nil, fmt.Errorf("unmarshal questions: %w", err)
		}
	}
	return &t, nil
}

func scanPGTaskRow(rows pgx.Rows) (*domain.Task, error) {
	var t domain.Task
	var phase string
	var questions []byte

	err := rows.Scan(
		&t.ID, &t.Title, &t.Description, &t.AgentID, &t.AgentRole,
		&t.Status, &phase, &t.Complexity, &t.Tags, &t.Understanding,
		&t.Assumptions, &t.Risks, &questions,
		&t.ImplementPlan, &t.PlanApprovedBy, &t.RedirectCount, &t.PhaseStartedAt,
		&t.Output, &t.InputTokens, &t.OutputTokens, &t.CostUSD,
		&t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan task row: %w", err)
	}

	t.Phase = domain.ExecutionPhase(phase)
	if len(questions) > 0 {
		if err := json.Unmarshal(questions, &t.Questions); err != nil {
			return nil, fmt.Errorf("unmarshal questions: %w", err)
		}
	}
	return &t, nil
}
