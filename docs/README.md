# AgentClaw

An autonomous multi-agent pipeline that converts a Trello card into working code, tests, documentation, and a GitHub PR — with zero manual intervention.

## Vision

AgentClaw orchestrates a team of specialised AI agents to execute a full software development cycle from a single Trello card. Each agent has a dedicated role (idea, architect, breakdown, coding, test, review, docs, deploy, notify), shares memory across tasks, and communicates through a priority queue and real-time event bus. The system self-heals by learning from past failures and injecting known resolutions into future agent prompts.

## How It Works

```
Trello Card
    |
    v
POST /api/trigger
    |
    +-- 1. Fetch card (title + description)
    +-- 2. Idea Agent (Opus)        -> structured app concept
    +-- 3. Breakdown Agent (Opus)   -> JSON task list (max 10)
    +-- 4. Trello checklist         -> one item per task
    +-- 5. Role agents execute:
    |       +-- coding -> MiniMax M2.5  (implementation code)
    |       |             fallback: Kimi K2.5 -> GLM-5
    |       +-- test   -> Sonnet        (blind tests — must NOT read implementation)
    |       +-- docs   -> MiniMax M2.5  (markdown documentation)
    |       |             fallback: Kimi K2.5 -> GLM-5
    |       +-- review -> Sonnet        (PR review JSON)
    +-- 6. GitHub PR                -> draft PR per coding task
    +-- 7. Telegram / Slack         -> pipeline complete notification
```

### Role Assignments (K2.5 Spec)

| Role | Model | Responsibility |
|------|-------|----------------|
| **Orchestration** | Claude Opus | Idea, breakdown, plan review, final review |
| **Coordination** | Claude Sonnet 4.6 | Test writing (blind), code review |
| **Implementation** | MiniMax M2.5 | Write code + documentation |
| **Fallback L1** | Kimi K2.5 (MoonshotAI) | Replaces MiniMax when unavailable |
| **Fallback L2** | GLM-5 (Z.AI/Zhipu) | Replaces Kimi when unavailable |
| **Infrastructure** | GLM-4.5-Flash | Deploy, notify |

### Worker Fallback Chain

```
MiniMax M2.5 --[fail]--> Kimi K2.5 --[fail]--> GLM-5 --[fail]--> halt + notify human
     |                       |                     |
     +-- 3 retries          +-- 3 retries         +-- 3 retries
         10s -> 30s -> 60s      10s -> 30s -> 60s     10s -> 30s -> 60s
```

- **Retryable errors:** server_down, no_response, timeout
- **Non-retryable errors (4xx):** invalid_request, auth_error — skip to next model immediately
- **Rule 4:** MiniMax is always retried first for each new task

### Pre-Execution Protocol (Phase Pipeline)

Every task passes through 4 phases before producing output:

```
understand -> clarify -> plan -> implement -> done
```

| Phase | What happens | Can suspend? |
|-------|-------------|:---:|
| **Understand** | LLM restates task, lists assumptions, risks, questions | No |
| **Clarify** | Unresolved questions escalated via chain: cache -> sonnet -> opus -> human | Yes (waits for human) |
| **Plan** | Agent generates plan, supervisor (Opus) reviews: APPROVED or REDIRECT | Yes (max 3 redirects) |
| **Implement** | LLM executes the approved plan | No |

Plan redirect preserves understanding + questions (no full restart).
Each phase saves a **checkpoint** before suspend for exact resume.

### Escalation Chain

```
GLM-flash -> Sonnet -> Opus -> Human
Haiku/GLM-5/MiniMax/Kimi -> Sonnet -> Opus -> Human
```

Each escalation saves a PhaseCheckpoint (SQLite) so the task resumes from exactly where it stopped.

### Memory Context (Tiered Loading)

Each agent receives a context budget based on task complexity:

| Tier | Complexity | Content | Token Budget |
|:---:|:---:|---------|:---:|
| 1 | S, M, L | AgentDoc, ScopeManifest, Scratchpad (24h) | ~500 |
| 2 | M, L | RecentByRole, ResolvedStore top-3, ProjectDoc | ~1500 |
| 3 | L only | ScopeStore.ReadAll (cross-agent), ADRs | ~3000 |

Budget enforcement trims in priority order: ADRs -> RelevantCode -> ProjectDoc -> AgentDoc.
Total budgets: S=2000, M=6000, L=12000 tokens.

---

## Core Features

### 1. Multi-Agent Pipeline
A 7-step automated pipeline triggered by a single API call. Agents are role-typed and run on the model best suited to their role. The pipeline returns HTTP 202 immediately and runs entirely in the background.

### 2. Priority Queue with Dependency Tracking
Tasks are enqueued with a priority level and a `depends_on` list. The queue blocks each agent until all its dependencies are marked done. Per-role notification channels eliminate busy-polling.

### 3. Tiered Agent Memory with Budget Enforcement
Every agent call is prefixed with a system prompt built from tiered memory layers. Budget enforcement (`enforceTokenBudget`) proportionally trims sections if the total exceeds the complexity-tier budget.

### 4. Prompt Caching (Anthropic)
System prompts use 1-hour TTL caching for stable content (role identity, project context). Task-specific content uses ephemeral (5-min) TTL. Automatically enabled for all Anthropic models (Opus, Sonnet, Haiku) across both agent layer and usecase phases.

### 5. Resolved Error Pattern Store
When a task fails, the executor searches `state/resolved/index.json` for matching error patterns. Matches are injected into agent system prompts (layer 4 memory) to proactively avoid known failure modes.

### 6. Phase Checkpoints (SQLite)
When a phase suspends (waiting for human input, escalation), full execution state is persisted to a `checkpoints` table. On resume, the phase continues from the exact step without re-work.

### 7. Parallel Orchestration with Concurrency Limit
`ParallelOrchestrator` runs subtasks via `errgroup.SetLimit(4)`. Supports partial failure — completed results are returned even when some tasks fail. Combined error reports which tasks failed.

### 8. LLM Router (Factory Pattern)
All providers registered in a single `LLMProviders` map. Two call paths: `callAnthropic` (custom protocol) and `callOpenAICompat` (table-driven for MiniMax, GLM, Kimi). To add a new OpenAI-compatible provider: add 1 entry to `LLMProviders`.

### 9. Circuit Breaker + Fallback Notifications
Per-provider circuit breakers (5 failures -> 60s cooldown). Fallback events are emitted via callback and dispatched to Telegram:
- `EventFallbackTriggered` -> StatusChannel (async, non-blocking)
- `EventFallbackExhausted` -> HumanChannel (critical, must reach human)

### 10. Trello Integration
Full Trello REST client with OAuth header authentication. Creates checklists, marks items complete as agents finish.

### 11. GitHub PR Lifecycle
Feature branches, draft PRs, review submission, and merge. PR events broadcast over the WebSocket event bus.

### 12. Real-Time Event Bus and WebSocket
In-process pub/sub broadcasts agent lifecycle, task events, fallback events, and PR events. WebSocket endpoint `/ws` streams to the dashboard frontend. Excluded from rate limiting.

### 13. Telegram and Slack Notifications
Dual-channel Telegram (statusChatID for automation, humanChatID for Q&A). Notifications dispatched in goroutines (non-blocking) for status events, synchronously for critical events (plan failed, fallback exhausted).

### 14. Agent Pool with Supervisor
Health-check supervisor goroutine. Unhealthy agents auto-restart up to MaxRetries. Individual agent kill/restart via REST API.

### 15. Metrics and Token Accounting
Every LLM call logged with tokens, cost, and duration. Cost calculation uses the actual model that served the response (handles fallback pricing correctly).

### 16. Rolling Weekly History Compression
`Summarizer` compresses each agent's task history into a 400-token memory document using Sonnet. Scheduled Sunday 02:00 via cron.

### 17. Per-IP Rate Limiting
Token-bucket rate limiter (10 req/min/IP). WebSocket excluded. Idle buckets purged every 5 minutes.

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

### Scratchpad

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/scratchpad` | Read team scratchpad (last 24h) |
| POST | `/api/scratchpad` | Add entry to scratchpad |

### Resolved Error Patterns

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/state/resolved` | List all error patterns (sorted by occurrence count) |
| GET | `/api/state/resolved/:id` | Get full detail file for a pattern |
| PATCH | `/api/state/resolved/:id/resolve` | Mark a pattern as resolved |

### State / Compression

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/state/compress` | Compress agent history. Returns `{"cost_usd": float, "summary_length": int}`. |

### Metrics

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/metrics/today` | Aggregated stats for today |
| GET | `/api/metrics/period?from=YYYY-MM-DD&to=YYYY-MM-DD` | Stats for a date range |
| GET | `/api/pricing` | Current model pricing table |

### WebSocket

| Path | Description |
|------|-------------|
| `/ws` | Real-time event stream. Emits JSON `Event` objects. Not rate-limited. |

---

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Language | Go 1.26 |
| HTTP server | `net/http` (stdlib) |
| WebSocket | `gorilla/websocket` |
| Database | SQLite via `mattn/go-sqlite3` (WAL mode) |
| Logging | `rs/zerolog` |
| LLM providers | Anthropic (Opus/Sonnet/Haiku), MiniMax, Kimi (MoonshotAI), GLM (Z.AI/Zhipu) |
| LLM routing | Factory pattern: `LLMProviders` registry + `callAnthropic` / `callOpenAICompat` |
| Task queue | In-process priority heap with per-role notification channels |
| State store | JSON files (state/), SQLite (tasks, checkpoints, token_logs) |
| Notifications | Telegram Bot API (dual-channel) + Slack incoming webhook |
| Source control | GitHub REST API v2022-11-28 |
| Project boards | Trello REST API (OAuth header auth) |
| Cron scheduler | `robfig/cron/v3` (weekly summary compression) |
| Rate limiting | Stdlib token bucket (per-IP, no external dependency) |

---

## Environment Variables

### Required

| Variable | Description |
|----------|-------------|
| `ANTHROPIC_API_KEY` | Anthropic API key for Opus, Sonnet, and Haiku models |
| `TRELLO_KEY` | Trello API key |
| `TRELLO_TOKEN` | Trello OAuth token |

### Optional

| Variable | Description |
|----------|-------------|
| `MINIMAX_API_KEY` | MiniMax API key (coding + docs agents) |
| `KIMI_API_KEY` | MoonshotAI API key (Kimi K2.5 fallback) |
| `GLM_API_KEY` | Z.AI/Zhipu API key (GLM-5/GLM-Flash) |
| `GITHUB_TOKEN` | GitHub personal access token |
| `GITHUB_OWNER` | GitHub repository owner |
| `GITHUB_REPO` | GitHub repository name |
| `TELEGRAM_BOT_TOKEN` | Telegram bot token from @BotFather |
| `TELEGRAM_CHAT_ID` | Target Telegram chat or channel ID |
| `STATUS_CHAT_ID` | Telegram channel for automated status events |
| `HUMAN_CHAT_ID` | Telegram channel for human Q&A and critical alerts |
| `SLACK_WEBHOOK_URL` | Slack incoming webhook URL |
| `CORS_ORIGIN` | Allowed CORS origin (for mutation endpoints and WebSocket) |
| `ADMIN_TOKEN` | When set, `POST /api/state/compress` requires `X-Admin-Token` header |
| `DB_PATH` | SQLite database path (default: `./AgentClaw.db`) |
| `PROJECT_PATH` | Project memory document path (default: `./memory/project.md`) |
| `STATE_PATH` | State base directory path (default: `./state`) |
| `PRICING_PATH` | Model pricing JSON path (default: `./pricing/agent-pricing.json`) |
| `AGENTS_CONFIG` | Agent config JSON path (default: `./config/agents.json`) |
| `ADDR` | HTTP listen address (default: `:8080`) |

---

## Directory Layout

```
AgentClaw/
+-- cmd/agentclawd/            # Main entrypoint (server, cron, wiring)
+-- internal/
|   +-- agent/                 # Agent types, pool, executor, event bus, base agent
|   +-- api/                   # HTTP server, WebSocket hub, handlers, rate limiter
|   +-- domain/                # Entities: Task, Event, Result, Model, Checkpoint
|   +-- port/                  # Interfaces: LLMRouter, Notifier, TaskStore, CheckpointStore
|   +-- usecase/
|   |   +-- phase/             # Phase pipeline: understand, clarify, plan, implement
|   |   +-- escalation/        # Escalation chain + cache
|   |   +-- orchestrator/      # Router, hierarchical, parallel, loop orchestrators
|   +-- infra/
|   |   +-- llm/               # Port adapter for llm.Router
|   |   +-- state/             # File-backed StateStore, CheckpointStore
|   |   +-- memory/            # SQLite CheckpointStore adapter
|   |   +-- notification/      # TelegramDispatcher (port.Notifier)
|   |   +-- integrations/      # ReplyAdapter (escalation.HumanAsker)
|   +-- integrations/
|   |   +-- github/            # GitHub REST client
|   |   +-- pipeline/          # End-to-end pipeline orchestrator
|   |   +-- telegram/          # Telegram Bot API (single + dual channel)
|   |   +-- slack/             # Slack webhook client
|   |   +-- trello/            # Trello REST client
|   +-- llm/                   # LLM router (8 files):
|   |   +-- provider.go        #   LLMProviders registry (single source of truth)
|   |   +-- types.go           #   Request, Response, Router struct, FallbackEvent
|   |   +-- router.go          #   Call(), costCalc, providerForModel, Stats
|   |   +-- anthropic.go       #   callAnthropic (custom protocol)
|   |   +-- openai_compat.go   #   callOpenAICompat (table-driven)
|   |   +-- fallback.go        #   callWithFallback, retry, notification callback
|   |   +-- errors.go          #   isPermanentError, truncateErrorBody
|   |   +-- breaker.go         #   Per-provider circuit breaker
|   |   +-- pricing.go         #   CalcCost, CalcCostAdvanced, LoadPricing
|   +-- memory/                # Memory store (3 files):
|   |   +-- types.go           #   Store struct, TokenLog, PeriodStats
|   |   +-- context.go         #   BuildContext (tiered loading + budget enforcement)
|   |   +-- store.go           #   CRUD, migrations, checkpoints, token logs
|   +-- queue/                 # Priority queue with dependency tracking
|   +-- state/                 # Resolved errors, scope, scratchpad, agent docs, skills
|   +-- summarizer/            # Rolling history compressor
+-- config/
|   +-- agents.json            # Agent role/model configuration
+-- pricing/
|   +-- agent-pricing.json     # Per-model token pricing
+-- state/                     # Runtime state files
+-- memory/                    # Project doc + per-role memory files
+-- static/                    # Dashboard frontend
+-- docs/                      # Documentation
```

---

## Architecture Decisions

- **Unified LLMProviders registry** — all models (Anthropic + OpenAI-compatible) registered in one `map[string]llmProvider`. Adding a new provider requires one map entry, no router code changes.
- **Factory pattern for providers** — `callAnthropic` handles Anthropic's custom protocol; `callOpenAICompat` handles all OpenAI-compatible providers (MiniMax, GLM, Kimi) via the same function with table-driven config.
- **Fallback chain with checkpoint** — worker models (MiniMax -> Kimi -> GLM-5) retry with exponential backoff. Full context state is persisted before each handoff. MiniMax is always retried first for each new task.
- **Async notifications, sync for critical** — `dispatchEvent` runs in a goroutine (non-blocking). `dispatchCritical` runs synchronously for failure events (plan failed, fallback exhausted) to guarantee delivery.
- **Phase checkpoints in SQLite** — when a phase suspends, full state is saved to the `checkpoints` table (co-located with tasks). On resume, the phase continues from the exact step without re-work.
- **Tiered context with budget enforcement** — memory context is assembled in 3 tiers based on task complexity. `enforceTokenBudget` proportionally trims lowest-priority sections if the total exceeds the tier budget.
- **Prompt cache TTL strategy** — stable content (role identity, project doc) uses 1-hour TTL; task-specific content uses ephemeral (5-min) TTL. Saves ~90% on input tokens for cache hits.
- **Single-mutex ResolvedStore** — `state/resolved/index.json` is one file; a single `sync.Mutex` is sufficient.
- **Atomic writes** — all file stores use temp-file + `os.Rename` to prevent partial reads.
- **Per-role queue channels** — agents block on role-specific channels rather than polling, giving O(1) wake-up.
- **Circuit breaker per provider** — groups models by provider name (e.g. "anthropic", "minimax", "moonshot", "zhipu"). 5 consecutive failures -> 60s cooldown.
- **Parallel orchestration with partial failure** — `errgroup.SetLimit(4)` caps concurrency. Tasks that fail don't cancel siblings; partial results are returned alongside a combined error.

---

*AgentClaw — agents that never forget, never repeat mistakes, never sleep.*
