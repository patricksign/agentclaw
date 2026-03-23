package port

import (
	"context"

	"github.com/patricksign/AgentClaw/internal/domain"
)

// ─────────────────────────────────────────────
// REDIS — Short-term Memory
// ─────────────────────────────────────────────

type ShortTermMemory interface {
	SetContextWindow(ctx context.Context, taskID string, msgs []domain.LLMMessage, ttl int) error
	GetContextWindow(ctx context.Context, taskID string) ([]domain.LLMMessage, error)
	AppendToContextWindow(ctx context.Context, taskID string, msg domain.LLMMessage) error

	AcquireQueueLock(ctx context.Context, agentID string, ttlSec int) (bool, error)
	ReleaseQueueLock(ctx context.Context, agentID string) error
	IsQueueLocked(ctx context.Context, agentID string) (bool, error)

	SetAgentTask(ctx context.Context, agentID, taskID string) error
	GetAgentTask(ctx context.Context, agentID string) (string, error)
	ClearAgentTask(ctx context.Context, agentID string) error

	CachePhase(ctx context.Context, taskID string, cp *domain.PhaseCheckpoint) error
	GetCachedPhase(ctx context.Context, taskID string) (*domain.PhaseCheckpoint, error)
	InvalidatePhase(ctx context.Context, taskID string) error
}

// ─────────────────────────────────────────────
// QDRANT — Semantic Memory
// ─────────────────────────────────────────────

type SemanticMemory interface {
	UpsertCodeChunk(ctx context.Context, chunk CodeChunk) error
	UpsertCodeChunks(ctx context.Context, chunks []CodeChunk) error
	UpsertDocument(ctx context.Context, doc SemanticDoc) error

	SearchSimilarCode(ctx context.Context, query string, role string, limit int) ([]CodeChunk, error)
	SearchResolvedPatterns(ctx context.Context, question string, role string, limit int) ([]ResolvedPattern, error)
	SearchDocs(ctx context.Context, query string, collection string, limit int) ([]SemanticDoc, error)

	DeleteByTaskID(ctx context.Context, taskID string) error
}

// ─────────────────────────────────────────────
// NEO4J — Knowledge Graph
// ─────────────────────────────────────────────

type KnowledgeGraph interface {
	UpsertTask(ctx context.Context, node TaskNode) error
	LinkTaskDependency(ctx context.Context, fromTaskID, toTaskID string, rel TaskRelation) error
	GetBlockingTasks(ctx context.Context, taskID string) ([]TaskNode, error)
	GetDependentTasks(ctx context.Context, taskID string) ([]TaskNode, error)

	UpsertModule(ctx context.Context, mod ModuleNode) error
	LinkModuleImport(ctx context.Context, fromMod, toMod string) error
	GetModuleSubgraph(ctx context.Context, moduleID string, depth int) (*ModuleGraph, error)
	FindCircularImports(ctx context.Context, moduleID string) ([][]string, error)

	UpsertAgentScope(ctx context.Context, scope AgentScope) error
	GetScopeConflicts(ctx context.Context, agentID string, paths []string) ([]ScopeConflict, error)

	RecordTaskOutcome(ctx context.Context, agentID, taskID, role string, success bool, costUSD float64) error
	GetAgentSkillSummary(ctx context.Context, agentID string) (*AgentSkill, error)
}

// ─────────────────────────────────────────────
// POSTGRESQL — System State
// ─────────────────────────────────────────────

type SystemState interface {
	UpsertTask(ctx context.Context, t *domain.Task) error
	GetTask(ctx context.Context, taskID string) (*domain.Task, error)
	ListTasks(ctx context.Context, f TaskFilter) ([]domain.Task, error)
	UpdateTaskStatus(ctx context.Context, taskID, status string) error

	SaveCheckpoint(ctx context.Context, cp *domain.PhaseCheckpoint) error
	LoadCheckpoint(ctx context.Context, taskID string) (*domain.PhaseCheckpoint, error)
	DeleteCheckpoint(ctx context.Context, taskID string) error

	AppendTokenLog(ctx context.Context, log TokenLog) error
	GetCostByPeriod(ctx context.Context, from, to string) ([]CostRow, error)
	GetTodayCost(ctx context.Context, agentID string) (float64, error)

	UpsertUser(ctx context.Context, u User) error
	GetUser(ctx context.Context, userID string) (*User, error)

	AppendEvent(ctx context.Context, evt domain.Event) error
	QueryEvents(ctx context.Context, taskID string, limit int) ([]domain.Event, error)
}

// ─────────────────────────────────────────────
// Value types
// ─────────────────────────────────────────────

type CodeChunk struct {
	ID        string
	TaskID    string
	FilePath  string
	Content   string
	Language  string
	Role      string
	Vector    []float32
	UpdatedAt int64
}

type SemanticDoc struct {
	ID         string
	Collection string
	Title      string
	Content    string
	Tags       []string
	Role       string
	Vector     []float32
}

type ResolvedPattern struct {
	ID              string
	Question        string
	Answer          string
	Role            string
	OccurrenceCount int
	Score           float32
}

type TaskNode struct {
	TaskID     string
	Title      string
	Status     string
	Phase      string
	AgentID    string
	Role       string
	Complexity string
}

type TaskRelation string

const (
	RelDependsOn TaskRelation = "DEPENDS_ON"
	RelBlockedBy TaskRelation = "BLOCKED_BY"
	RelSubtaskOf TaskRelation = "SUBTASK_OF"
	RelRelatedTo TaskRelation = "RELATED_TO"
)

type ModuleNode struct {
	ModuleID string
	Name     string
	Package  string
	FilePath string
}

type ModuleGraph struct {
	Root       ModuleNode
	Imports    []ModuleNode
	ImportedBy []ModuleNode
}

type AgentScope struct {
	AgentID      string
	Role         string
	Owns         []string
	MustNotTouch []string
	DependsOn    []string
}

type ScopeConflict struct {
	Path         string
	ConflictWith string
	ConflictRole string
}

type AgentSkill struct {
	AgentID       string
	Role          string
	TotalTasks    int
	SuccessRate   float64
	AvgCostUSD    float64
	AvgDurationMs int64
}

type TokenLog struct {
	TaskID       string
	AgentID      string
	AgentRole    string
	Model        string
	Phase        string
	InputTokens  int64
	OutputTokens int64
	CacheTokens  int64
	CostUSD      float64
	CostMode     string
	LoggedAt     string
}

type CostRow struct {
	AgentID   string
	Model     string
	Date      string
	TotalCost float64
	TotalIn   int64
	TotalOut  int64
}

type User struct {
	ID        string
	Email     string
	Name      string
	TenantID  string
	Plan      string
	CreatedAt string
}

