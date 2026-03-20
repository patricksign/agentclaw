# AgentClaw

An autonomous multi-agent pipeline that converts a Trello card into working code, tests, documentation, and a GitHub PR — with zero manual intervention.

## Vision

AgentClaw orchestrates a team of specialised AI agents to execute a full software development cycle from a single Trello card. Each agent has a dedicated role (idea, architect, breakdown, coding, test, review, docs, deploy, notify), shares memory across tasks, and communicates through a priority queue and real-time event bus. The system self-heals by learning from past failures and injecting known resolutions into future agent prompts.

## How It Works

```
Trello Card
    │
    ▼
POST /api/trigger
    │
    ├─ 1. Fetch card (title + description)
    ├─ 2. Idea Agent (Opus)       → structured app concept
    ├─ 3. Breakdown Agent (Sonnet) → JSON task list (max 10)
    ├─ 4. Trello checklist        → one item per task
    ├─ 5. Role agents in sequence → coding / test / docs / review
    │       ├─ coding → MiniMax   (implementation code)
    │       ├─ test   → GLM-4     (table-driven tests)
    │       ├─ docs   → GLM-Flash (markdown documentation)
    │       └─ review → Sonnet    (PR review JSON)
    ├─ 6. GitHub PR               → draft PR per coding task
    └─ 7. Telegram / Slack        → pipeline complete notification
```

Each agent receives a rich memory context before executing its task:

| Layer | Source | Content |
|-------|--------|---------|
| 1 | `memory/project.md` | Project vision, stack, ADRs |
| 2 | SQLite `tasks` table | Last 5 completed tasks for the same role |
| 3 | SQLite RAG search | Related task titles and descriptions |
| 4 | `state/resolved/` | Known error patterns and their fixes |

---

## Core Features

### 1. Multi-Agent Pipeline
A 7-step automated pipeline triggered by a single API call. Agents are role-typed (idea, architect, breakdown, coding, test, review, docs, deploy, notify) and run on the model best suited to their role. The pipeline returns HTTP 202 immediately and runs entirely in the background.

### 2. Priority Queue with Dependency Tracking
Tasks are enqueued with a priority level and a `depends_on` list. The queue blocks each agent until all its dependencies are marked done. Per-role notification channels eliminate busy-polling — agents wake immediately when a matching task is ready.

### 3. 4-Layer Agent Memory
Every agent call is prefixed with a system prompt built from four memory layers: the project document, recent task history for the same role, keyword-matched RAG results from past tasks, and known error patterns from the resolved store. Agents never repeat known mistakes.

### 4. Resolved Error Pattern Store
When a task fails, the executor searches `state/resolved/index.json` for matching error patterns. If a match is found, the resolution summary is attached to the task metadata so the review endpoint can surface it immediately. The store is also searched before every agent invocation (layer 4 memory) to proactively avoid known failure modes.

Each error pattern is stored with:
- Normalised error string and searchable tags
- Affected agent roles and occurrence count
- One-line resolution summary
- Full detail file (`<6-hex-id>.md`) with root cause, fix steps, and prevention notes

### 5. Trello Integration
Full Trello REST client with OAuth header authentication. The pipeline creates an "AgentClaw Tasks" checklist on the card, adds one item per breakdown task, and marks each item complete as the corresponding agent finishes.

### 6. GitHub PR Lifecycle
For each coding task, the pipeline creates a feature branch (`feature/<task-id>-<title>`), opens a draft PR, and can submit reviews and merge. PR creation, review, and merge events are broadcast over the WebSocket event bus.

### 7. Real-Time Event Bus and WebSocket
An in-process pub/sub event bus broadcasts agent lifecycle events (spawned, killed, healthy), task events (queued, started, done, failed), token usage logs, and PR events. A WebSocket endpoint (`/ws`) streams these events to the dashboard frontend. Origin validation is enforced against the `CORS_ORIGIN` environment variable. The `/ws` endpoint is **excluded from rate limiting**.

### 8. Telegram and Slack Notifications
Nine notification methods covering task start, completion, failure, PR creation, PR review, PR merge, daily summary, checklist complete, and arbitrary HTML messages. Slack incoming webhook support is also included. Both clients are optional — the pipeline skips notifications silently if the env vars are absent.

### 9. Agent Pool with Supervisor
The agent pool maintains a set of named agents, each with a health-check supervisor goroutine. Unhealthy agents are automatically restarted up to `MaxRetries` times. Agents can be killed or restarted individually via the REST API. The pool tracks per-agent `done` channels to prevent double-kill races.

### 10. Metrics and Token Accounting
Every LLM call is logged with input tokens, output tokens, cost in USD, and duration. Aggregated stats are available per day or per date range via the metrics API.

### 11. Rolling Weekly History Compression
A `Summarizer` (`internal/summarizer`) compresses each agent's completed task history into a 400-token memory document using Claude Sonnet. The summary is appended to `memory/agents/<role>.md` so future tasks benefit from accumulated institutional knowledge.

- Loads the last 50 `done`/`failed` tasks per role; skips if fewer than 10 exist
- Calls Sonnet with a focused system prompt: patterns used, pitfalls encountered, decisions made, modules touched
- Archives the raw task list to `state/old/summary-<agentID>-<YYYY-MM-DD>.md`
- Logs token usage and cost against the `summarizer-<agentID>` task ID
- Scheduled automatically every **Sunday at 02:00** via `robfig/cron/v3`
- Also triggerable on demand via `POST /api/state/compress`

### 12. Per-IP Rate Limiting
All REST endpoints are protected by a token-bucket rate limiter (10 requests per minute per IP). The WebSocket endpoint `/ws` is explicitly excluded. Clients that exceed the limit receive `HTTP 429` with a `Retry-After: 60` header. Idle IP buckets are purged every 5 minutes after 10 minutes of inactivity.

---

## API Reference

### Pipeline

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/trigger` | Start pipeline for a Trello card. Body: `{"workspace_id":"<board_id>","ticket_id":"<card_id>"}`. Returns 202. |

### Agents

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/agents` | List all agents and their current status |
| POST | `/api/agents/:id/restart` | Restart a specific agent |
| POST | `/api/agents/:id/kill` | Kill a specific agent |

### Tasks

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/tasks` | List all tasks (newest first) |
| POST | `/api/tasks` | Create and enqueue a task |
| GET | `/api/tasks/:id` | Get a single task |
| PATCH | `/api/tasks/:id` | Update task status |
| GET | `/api/tasks/:id/logs` | Token usage logs for a task |

### Memory

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/memory/project` | Read `project.md` |
| PATCH | `/api/memory/project` | Append a section to `project.md` |

### Resolved Error Patterns

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/state/resolved` | List all error patterns (sorted by occurrence count) |
| GET | `/api/state/resolved/:id` | Get full detail file for a pattern |
| PATCH | `/api/state/resolved/:id/resolve` | Mark a pattern as resolved |

### State / Compression

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/state/compress` | Compress agent history. Body: `{"agent_id":"","role":""}`. Empty `agent_id` compresses all roles. Returns `{"cost_usd": float, "summary_length": int}`. Requires `X-Admin-Token` header when `ADMIN_TOKEN` env var is set. |

### Metrics

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/metrics/today` | Aggregated stats for today |
| GET | `/api/metrics/period?from=YYYY-MM-DD&to=YYYY-MM-DD` | Stats for a date range |

### WebSocket

| Path | Description |
|------|-------------|
| `/ws` | Real-time event stream. Emits JSON `Event` objects for all agent and task lifecycle changes. Not rate-limited. |

---

## Rate Limiting

All REST endpoints (excluding `/ws`) are rate-limited at **10 requests per minute per IP**.

| Behaviour | Detail |
|-----------|--------|
| Algorithm | Token bucket — 10-token burst, refilled proportionally over 60 seconds |
| Exceeded response | `HTTP 429 Too Many Requests` + `Retry-After: 60` header |
| IP extraction | `r.RemoteAddr` (direct connections); set `X-Forwarded-For` / `X-Real-IP` via a trusted reverse proxy for per-client isolation |
| Idle bucket cleanup | Every 5 minutes; buckets idle for more than 10 minutes are removed |
| Excluded path | `/ws` — WebSocket connections are long-lived and manage their own lifecycle |

---

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Language | Go 1.26 |
| HTTP server | `net/http` (stdlib) |
| WebSocket | `gorilla/websocket` |
| Database | SQLite via `mattn/go-sqlite3` (WAL mode) |
| Logging | `rs/zerolog` |
| LLM routing | Internal `llm.Router` (Anthropic Opus/Sonnet, MiniMax, GLM) |
| Task queue | In-process priority heap with per-role notification channels |
| State store | JSON files under `state/resolved/` (atomic writes via `os.Rename`) |
| Notifications | Telegram Bot API (HTML mode) + Slack incoming webhook |
| Source control | GitHub REST API v2022-11-28 |
| Project boards | Trello REST API (OAuth header auth) |
| Cron scheduler | `robfig/cron/v3` (weekly summary compression) |
| Rate limiting | Stdlib token bucket (per-IP, no external dependency) |

---

## Environment Variables

### Required

| Variable | Description |
|----------|-------------|
| `ANTHROPIC_API_KEY` | Anthropic API key for Opus and Sonnet models |
| `TRELLO_KEY` | Trello API key |
| `TRELLO_TOKEN` | Trello OAuth token |

### Optional

| Variable | Description |
|----------|-------------|
| `MINIMAX_API_KEY` | MiniMax API key (coding agent) |
| `GLM_API_KEY` | GLM API key (test and docs agents) |
| `GITHUB_TOKEN` | GitHub personal access token |
| `GITHUB_OWNER` | GitHub repository owner |
| `GITHUB_REPO` | GitHub repository name |
| `TELEGRAM_BOT_TOKEN` | Telegram bot token from @BotFather |
| `TELEGRAM_CHAT_ID` | Target Telegram chat or channel ID |
| `SLACK_WEBHOOK_URL` | Slack incoming webhook URL |
| `CORS_ORIGIN` | Allowed CORS origin (required for mutation endpoints and WebSocket) |
| `ADMIN_TOKEN` | When set, `POST /api/state/compress` requires `X-Admin-Token: <value>` |
| `DB_PATH` | SQLite database path (default: `./agentclaw.db`) |
| `PROJECT_PATH` | Project memory document path (default: `./memory/project.md`) |
| `SCOPE_PATH` | State base directory path (default: `./state`) |
| `ADDR` | HTTP listen address (default: `:8080`) |

---

## Directory Layout

```
agentclaw/
├── cmd/agentclawd/         # Main entrypoint (server, cron, dependency wiring)
├── internal/
│   ├── agent/              # Agent types, pool, executor, event bus, base agent
│   ├── api/                # HTTP server, WebSocket hub, route handlers, rate limiter
│   ├── integrations/
│   │   ├── github/         # GitHub REST client (PRs, branches, reviews, merge)
│   │   ├── pipeline/       # End-to-end pipeline orchestrator
│   │   ├── telegram/       # Telegram Bot API + Slack webhook client
│   │   └── trello/         # Trello REST client (cards, checklists, checklist items)
│   ├── llm/                # LLM router (model selection, retry, cost tracking)
│   ├── memory/             # 3-layer memory store (SQLite + project.md)
│   ├── queue/              # Priority queue with dependency tracking
│   ├── state/              # Resolved error pattern store, agent doc store, scratchpad
│   └── summarizer/         # Rolling history compressor (Summarizer struct + interfaces)
├── state/
│   ├── resolved/           # Error pattern index and detail files (runtime)
│   └── old/                # Archived raw task lists from weekly compression
├── memory/
│   ├── project.md          # Project memory document (agents read and write this)
│   └── agents/             # Per-role memory files (seeded defaults + appended summaries)
├── static/                 # Dashboard frontend
└── docs/                   # Documentation
```

---

## Architecture Decisions

- **Single-mutex ResolvedStore** — `state/resolved/index.json` is one file; a single `sync.Mutex` is sufficient and avoids per-ID lock complexity.
- **Atomic index writes** — all writes to `index.json` go through a temp-file + `os.Rename` to prevent partial reads during concurrent access.
- **In-memory pattern cache** — the resolved store loads `index.json` from disk once and keeps a hot cache; writes update both disk and cache atomically.
- **Per-role queue channels** — agents block on a role-specific `chan struct{}` rather than a global timer, giving O(1) wake-up latency when a matching task is pushed.
- **`context.Background()` for background pipeline** — the trigger goroutine uses `context.Background()` so the HTTP request context cancellation does not abort the pipeline.
- **WebSocket origin validation** — the upgrader checks the `Origin` header against `CORS_ORIGIN`; if unset, only same-host origins are accepted.
- **Summarizer uses interfaces, not concrete types** — `TaskStore`, `DocStore`, and `LLMRouter` interfaces decouple `internal/summarizer` from `internal/memory` and `internal/state`, preventing import cycles while keeping the compressor testable in isolation.
- **Token bucket with fractional-remainder preservation** — the rate limiter advances `lastSeen` by `refill * interval / max` instead of snapping to `now`, ensuring partial elapsed time accumulates correctly toward the next token and the effective rate is always exactly 10 req/min.
- **Cleanup goroutine releases global lock before probing buckets** — the rate limiter snapshots IP pointers under `rl.mu`, releases it, then inspects each `bucket.mu` independently to avoid an O(n) stall on every cleanup tick.
- **`sync.Once`-protected `Stop()`** — the rate limiter cleanup goroutine is safe to stop multiple times without panicking, which matters in tests that create and destroy `Server` instances.

---

*AgentClaw — agents that never forget, never repeat mistakes, never sleep.*
