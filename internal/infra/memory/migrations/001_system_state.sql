-- 001_system_state.sql
-- Initial schema: tasks, phase_checkpoints, token_logs, users, event_log, adr

-- Tasks (source of truth, replaces SQLite tasks table)
CREATE TABLE IF NOT EXISTS tasks (
    id               TEXT PRIMARY KEY,
    title            TEXT NOT NULL,
    description      TEXT,
    agent_id         TEXT,
    agent_role       TEXT,
    status           TEXT NOT NULL DEFAULT 'pending',
    phase            TEXT NOT NULL DEFAULT 'understand',
    complexity       TEXT NOT NULL DEFAULT 'M',
    tags             TEXT[],
    understanding    TEXT,
    assumptions      TEXT[],
    risks            TEXT[],
    questions        JSONB,
    implement_plan   TEXT,
    plan_approved_by TEXT,
    redirect_count   INT  DEFAULT 0,
    output           TEXT,
    input_tokens     BIGINT DEFAULT 0,
    output_tokens    BIGINT DEFAULT 0,
    cost_usd         NUMERIC(12,6) DEFAULT 0,
    phase_started_at TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_tasks_agent_id ON tasks(agent_id);
CREATE INDEX IF NOT EXISTS idx_tasks_status   ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_phase    ON tasks(phase);

-- Phase checkpoints (explicit columns, not JSON blob)
CREATE TABLE IF NOT EXISTS phase_checkpoints (
    task_id            TEXT PRIMARY KEY,
    agent_id           TEXT NOT NULL,
    phase              TEXT NOT NULL,
    step_index         INT  DEFAULT 0,
    step_name          TEXT,
    accumulated        JSONB,
    pending_query      TEXT,
    pending_query_id   TEXT,
    suspended_model    TEXT,
    escalated_to       TEXT,
    last_system_prompt TEXT,
    last_messages      JSONB,
    last_response      TEXT,
    input_tokens       BIGINT DEFAULT 0,
    output_tokens      BIGINT DEFAULT 0,
    cost_usd           NUMERIC(12,6) DEFAULT 0,
    saved_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Token & cost logs
CREATE TABLE IF NOT EXISTS token_logs (
    id            BIGSERIAL PRIMARY KEY,
    task_id       TEXT,
    agent_id      TEXT,
    agent_role    TEXT,
    model         TEXT,
    phase         TEXT,
    input_tokens  BIGINT,
    output_tokens BIGINT,
    cache_tokens  BIGINT DEFAULT 0,
    cost_usd      NUMERIC(12,6),
    cost_mode     TEXT,
    logged_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_token_logs_agent ON token_logs(agent_id, logged_at DESC);
CREATE INDEX IF NOT EXISTS idx_token_logs_date  ON token_logs(logged_at DESC);

-- Users / tenants
CREATE TABLE IF NOT EXISTS users (
    id         TEXT PRIMARY KEY,
    email      TEXT UNIQUE NOT NULL,
    name       TEXT,
    tenant_id  TEXT,
    plan       TEXT DEFAULT 'free',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Event audit log (append-only)
CREATE TABLE IF NOT EXISTS event_log (
    id          BIGSERIAL PRIMARY KEY,
    type        TEXT NOT NULL,
    channel     INT  DEFAULT 0,
    task_id     TEXT,
    agent_id    TEXT,
    agent_role  TEXT,
    model       TEXT,
    payload     JSONB,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_event_log_task ON event_log(task_id);
CREATE INDEX IF NOT EXISTS idx_event_log_time ON event_log(occurred_at DESC);

-- Architecture Decision Records
CREATE TABLE IF NOT EXISTS adr (
    id         BIGSERIAL PRIMARY KEY,
    title      TEXT NOT NULL,
    decision   TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
