package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/patricksign/AgentClaw/internal/domain"
	"github.com/patricksign/AgentClaw/internal/port"
	"github.com/redis/go-redis/v9"
)

const (
	keyContextWindow   = "ctx:window:%s"  // ctx:window:{taskID}  → JSON []LLMMessage
	keyQueueLock       = "queue:lock:%s"  // queue:lock:{agentID} → "1"
	keyAgentTask       = "agent:task:%s"  // agent:task:{agentID} → taskID
	keyPhaseCache      = "phase:cache:%s" // phase:cache:{taskID} → JSON PhaseCheckpoint
	defaultContextTTL  = 8 * time.Hour
	defaultPhaseTTL    = 4 * time.Hour
	maxContextMessages = 200
)

var _ port.ShortTermMemory = (*RedisMemory)(nil)

type RedisMemory struct{ client *redis.Client }

func NewRedisMemory(c *redis.Client) *RedisMemory { return &RedisMemory{client: c} }

// ─── Context Window ─────────────────────────────────────────────────────────

func (r *RedisMemory) SetContextWindow(ctx context.Context, taskID string, msgs []domain.LLMMessage, ttlSec int) error {
	b, err := json.Marshal(msgs)
	if err != nil {
		return err
	}
	ttl := time.Duration(ttlSec) * time.Second
	if ttl <= 0 {
		ttl = defaultContextTTL
	}
	return r.client.Set(ctx, fmt.Sprintf(keyContextWindow, taskID), b, ttl).Err()
}

func (r *RedisMemory) GetContextWindow(ctx context.Context, taskID string) ([]domain.LLMMessage, error) {
	b, err := r.client.Get(ctx, fmt.Sprintf(keyContextWindow, taskID)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var msgs []domain.LLMMessage
	return msgs, json.Unmarshal(b, &msgs)
}

func (r *RedisMemory) AppendToContextWindow(ctx context.Context, taskID string, msg domain.LLMMessage) error {
	// Atomic read-modify-write via Lua script to avoid TOCTOU race (#69).
	script := redis.NewScript(`
		local key = KEYS[1]
		local newMsg = ARGV[1]
		local maxLen = tonumber(ARGV[2])
		local ttl = tonumber(ARGV[3])

		local raw = redis.call('GET', key)
		local msgs = {}
		if raw then
			msgs = cjson.decode(raw)
		end

		local parsed = cjson.decode(newMsg)
		table.insert(msgs, parsed)

		if #msgs > maxLen then
			local start = #msgs - maxLen + 1
			local trimmed = {}
			for i = start, #msgs do
				table.insert(trimmed, msgs[i])
			end
			msgs = trimmed
		end

		redis.call('SET', key, cjson.encode(msgs), 'EX', ttl)
		return #msgs
	`)

	msgJSON, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	key := fmt.Sprintf(keyContextWindow, taskID)
	return script.Run(ctx, r.client, []string{key},
		string(msgJSON), maxContextMessages, int(defaultContextTTL.Seconds()),
	).Err()
}

// ─── Queue Lock (distributed mutex for 5 agents) ───────────────────────────

func (r *RedisMemory) AcquireQueueLock(ctx context.Context, agentID string, ttlSec int) (bool, error) {
	ok, err := r.client.SetNX(ctx, //nolint:staticcheck
		fmt.Sprintf(keyQueueLock, agentID),
		"1",
		time.Duration(ttlSec)*time.Second,
	).Result()
	return ok, err
}

func (r *RedisMemory) ReleaseQueueLock(ctx context.Context, agentID string) error {
	return r.client.Del(ctx, fmt.Sprintf(keyQueueLock, agentID)).Err()
}

func (r *RedisMemory) IsQueueLocked(ctx context.Context, agentID string) (bool, error) {
	n, err := r.client.Exists(ctx, fmt.Sprintf(keyQueueLock, agentID)).Result()
	return n > 0, err
}

// ─── Agent Task ─────────────────────────────────────────────────────────────

func (r *RedisMemory) SetAgentTask(ctx context.Context, agentID, taskID string) error {
	return r.client.Set(ctx, fmt.Sprintf(keyAgentTask, agentID), taskID, defaultContextTTL).Err()
}

func (r *RedisMemory) GetAgentTask(ctx context.Context, agentID string) (string, error) {
	v, err := r.client.Get(ctx, fmt.Sprintf(keyAgentTask, agentID)).Result()
	if err == redis.Nil {
		return "", nil
	}
	return v, err
}

func (r *RedisMemory) ClearAgentTask(ctx context.Context, agentID string) error {
	return r.client.Del(ctx, fmt.Sprintf(keyAgentTask, agentID)).Err()
}

// ─── Phase Cache ────────────────────────────────────────────────────────────

func (r *RedisMemory) CachePhase(ctx context.Context, taskID string, cp *domain.PhaseCheckpoint) error {
	b, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	return r.client.Set(ctx, fmt.Sprintf(keyPhaseCache, taskID), b, defaultPhaseTTL).Err()
}

func (r *RedisMemory) GetCachedPhase(ctx context.Context, taskID string) (*domain.PhaseCheckpoint, error) {
	b, err := r.client.Get(ctx, fmt.Sprintf(keyPhaseCache, taskID)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cp domain.PhaseCheckpoint
	return &cp, json.Unmarshal(b, &cp)
}

func (r *RedisMemory) InvalidatePhase(ctx context.Context, taskID string) error {
	return r.client.Del(ctx, fmt.Sprintf(keyPhaseCache, taskID)).Err()
}
