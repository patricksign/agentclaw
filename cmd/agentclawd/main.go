package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/patricksign/AgentClaw/internal/adapter"
	"github.com/patricksign/AgentClaw/internal/api"
	"github.com/patricksign/AgentClaw/internal/domain"
	infratask "github.com/patricksign/AgentClaw/internal/infra/task"
	"github.com/patricksign/AgentClaw/internal/integrations/github"
	"github.com/patricksign/AgentClaw/internal/integrations/pipeline"
	"github.com/patricksign/AgentClaw/internal/integrations/trello"
	"github.com/patricksign/AgentClaw/internal/llm"
	"github.com/patricksign/AgentClaw/internal/state"
	"github.com/patricksign/AgentClaw/internal/summarizer"
)

const maxTaskRetries = 3

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	log.Info().Msg("AgentClaw starting...")

	// ── Config ─────────────────────────────────────────────────────────────
	addr := getenv("ADDR", ":8080")
	dbPath := getenv("DB_PATH", "./AgentClaw.db")
	projectPath := getenv("PROJECT_PATH", "./memory/project.md")
	statePath := getenv("STATE_PATH", "./state")
	pricingPath := getenv("PRICING_PATH", "./pricing/agent-pricing.json")
	agentsConfigPath := getenv("AGENTS_CONFIG", "./config/agents.json")

	// ── Layer 4: Infrastructure ───────────────────────────────────────────
	infra, cleanupInfra := wireInfra(pricingPath, statePath, dbPath, projectPath)
	defer cleanupInfra()

	// ── Layer 2: Use Cases ────────────────────────────────────────────────
	_ = wireUseCase(infra)

	// ── Legacy Agent Layer ────────────────────────────────────────────────
	legacy := wireLegacy(infra, agentsConfigPath)

	// ── Event-Driven: Domain Event Bus + Subscribers ──────────────────────
	subs := wireSubscribers(infra, legacy, legacy.q)

	// Dispatcher publishes events on the domain bus.
	dispatcher := infratask.NewQueueDispatcher(subs.domainBus)

	// Task result waiter (listens for task.done / task.failed on domain bus).
	waiter := infratask.NewEventWaiter(subs.domainBus)

	// ── Background Context ────────────────────────────────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ── Queue Workers ─────────────────────────────────────────────────────
	var workerWg sync.WaitGroup
	roles := []string{
		"idea", "architect", "breakdown",
		"coding", "test", "review",
		"docs", "deploy", "notify",
	}
	for _, role := range roles {
		workerWg.Add(1)
		go func(r string) {
			defer workerWg.Done()
			runWorker(ctx, r, legacy.q, legacy.exec)
		}(role)
	}
	log.Info().Strs("roles", roles).Msg("queue workers started")

	// ── Trello Idea Board Poller ──────────────────────────────────────────
	ideaBoardID := getenv("TRELLO_IDEA_BOARD_ID", "")
	doneListID := getenv("TRELLO_DONE_LIST_ID", "")
	if ideaBoardID != "" {
		trelloAPIKey := getenv("TRELLO_API_KEY", "")
		trelloToken := getenv("TRELLO_TOKEN", "")
		go pollTrelloIdeas(ctx, ideaBoardID, doneListID, trelloAPIKey, trelloToken, legacy.q, 30*time.Second)
		log.Info().Str("board", ideaBoardID).Msg("Trello idea board poller started")
	}

	// ── Trello Trigger Client ─────────────────────────────────────────────
	trelloKey := getenv("TRELLO_KEY", getenv("TRELLO_API_KEY", ""))
	trelloToken := getenv("TRELLO_TOKEN", "")
	var triggerTrelloClient *trello.Client
	if trelloKey != "" && trelloToken != "" {
		var err error
		triggerTrelloClient, err = trello.New(trelloKey, trelloToken)
		if err != nil {
			log.Warn().Err(err).Msg("trigger Trello client init failed — /api/trigger will be unavailable")
		}
	}

	// GitHub client for pipeline PR creation.
	var ghClient *github.Client
	if github.IsConfigured() {
		if gh, err := github.New(); err == nil {
			ghClient = gh
		} else {
			log.Warn().Err(err).Msg("GitHub client init failed — pipeline PRs disabled")
		}
	}

	// ── HTTP + WebSocket API ──────────────────────────────────────────────
	telegramToken := getenv("TELEGRAM_BOT_TOKEN", "")
	telegramChatID := getenv("TELEGRAM_CHAT_ID", "")
	srv := api.NewServer(legacy.pool, legacy.q, legacy.exec, infra.coreMem, legacy.bus, triggerTrelloClient, telegramToken, telegramChatID)

	// Pipeline service now uses port interfaces.
	pipelineSvc := pipeline.NewService(triggerTrelloClient, ghClient, dispatcher, waiter, subs.domainBus)
	srv.SetTriggerService(pipelineSvc)

	// ── Summarizer + Weekly Cron ──────────────────────────────────────────
	anthropicKey := getenv("ANTHROPIC_API_KEY", "")
	summarizerRouter := llm.NewRouterWithEnv(map[string]string{"ANTHROPIC_API_KEY": anthropicKey})
	sum := summarizer.New(infra.coreMem, infra.coreMem.AgentDoc(), summarizerRouter, statePath)
	srv.SetSummarizer(sum)

	cronScheduler := cron.New()
	summarizerConfigs := []domain.AgentConfig{
		{ID: "idea", Role: "idea"},
		{ID: "architect", Role: "architect"},
		{ID: "breakdown", Role: "breakdown"},
		{ID: "coding", Role: "coding"},
		{ID: "test", Role: "test"},
		{ID: "review", Role: "review"},
		{ID: "docs", Role: "docs"},
		{ID: "deploy", Role: "deploy"},
		{ID: "notify", Role: "notify"},
	}
	if _, err := cronScheduler.AddFunc("0 2 * * 0", func() {
		cronCtx, cronCancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cronCancel()
		if cost, cerr := sum.CompressAll(cronCtx, summarizerConfigs); cerr != nil {
			log.Error().Err(cerr).Msg("cron: CompressAll failed")
		} else {
			log.Info().Float64("cost_usd", cost).Msg("cron: CompressAll completed")
		}
	}); err != nil {
		log.Error().Err(err).Msg("failed to schedule summarizer cron")
	}
	cronScheduler.Start()
	defer cronScheduler.Stop()

	// ── HTTP Server ───────────────────────────────────────────────────────
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	go func() {
		log.Info().Str("addr", addr).Msg("API server listening")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server error")
		}
	}()

	// ── Graceful Shutdown ─────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("shutting down AgentClaw...")

	// 1. Cancel context — stops workers and pollers.
	cancel()

	// 1b. Wait for all queue workers to exit.
	workerWg.Wait()
	log.Info().Msg("all queue workers stopped")

	// 2. Stop HTTP server — no new requests.
	httpCtx, httpCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer httpCancel()
	if err := httpServer.Shutdown(httpCtx); err != nil {
		log.Error().Err(err).Msg("HTTP shutdown error")
	}
	log.Info().Msg("HTTP server stopped")

	srv.Shutdown()
	log.Info().Msg("WebSocket hub stopped")

	// 3. Stop agents — no new events will be published.
	agentCtx, agentCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer agentCancel()
	legacy.pool.ShutdownAll(agentCtx)
	log.Info().Msg("all agents stopped")

	// 4. Stop event-driven layer (proper ordering):
	//    a) Waiter first — signals pending waiters to unblock.
	//    b) Subscribers — unsubscribe handlers from bus.
	//    c) Domain bus — drain in-flight handler goroutines.
	waiter.Stop()
	log.Info().Msg("event waiter stopped")

	stopSubscribers(subs)

	subs.domainBus.Stop()
	log.Info().Msg("domain event bus drained")

	// 5. Infra cleanup (deferred cleanupInfra closes DB).
	log.Info().Msg("AgentClaw stopped")
}

// ── Scope Defaults ──────────────────────────────────────────────────────────

func initDefaultScopes(ss *state.ScopeStore) error {
	manifests := []state.ScopeManifest{
		{AgentID: "idea", Owns: []string{"memory/project.md (app concept section)"}, MustNotTouch: []string{"internal/", "cmd/", "go.mod"}, InterfacesWith: map[string]string{"architect": "passes structured app concept for system design", "breakdown": "concept is used as breakdown input"}, CurrentFocus: "generate concrete, buildable app concepts from Trello briefs"},
		{AgentID: "architect", Owns: []string{"memory/project.md (ADR section)", "docs/architecture/"}, DependsOn: []string{"idea"}, MustNotTouch: []string{"internal/", "cmd/", "go.mod"}, InterfacesWith: map[string]string{"idea": "receives app concept", "breakdown": "passes Mermaid diagrams and ADRs for ticket decomposition"}, CurrentFocus: "produce Mermaid diagrams, ERDs, API contracts, and ADRs"},
		{AgentID: "breakdown", Owns: []string{"Trello checklists and ticket JSON"}, DependsOn: []string{"idea", "architect"}, MustNotTouch: []string{"internal/", "cmd/", "go.mod", "docs/architecture/"}, InterfacesWith: map[string]string{"architect": "receives system design docs", "coding": "tickets are consumed by coding agents", "test": "tickets describe acceptance criteria for test agents"}, CurrentFocus: "decompose app concept into actionable Trello tickets (max 10)"},
		{AgentID: "coding", Owns: []string{"internal/", "cmd/", "vendor/"}, DependsOn: []string{"breakdown"}, MustNotTouch: []string{"docs/", "memory/project.md", "static/"}, InterfacesWith: map[string]string{"breakdown": "receives implementation tickets", "test": "produces code that test agents verify", "review": "produces PRs that review agents inspect"}, CurrentFocus: "implement features from tickets in idiomatic Go"},
		{AgentID: "test", Owns: []string{"*_test.go files", "testdata/"}, DependsOn: []string{"coding"}, MustNotTouch: []string{"internal/ (non-test files)", "cmd/", "docs/", "memory/project.md"}, InterfacesWith: map[string]string{"coding": "receives implementation code to write tests for", "review": "test results inform the review decision"}, CurrentFocus: "write table-driven tests covering edge cases and error paths"},
		{AgentID: "review", Owns: []string{"GitHub PR review comments"}, DependsOn: []string{"coding", "test"}, MustNotTouch: []string{"internal/", "cmd/", "docs/", "memory/project.md"}, InterfacesWith: map[string]string{"coding": "reviews PRs opened by coding agents", "test": "incorporates test results into review decision", "deploy": "approved PRs are handed to deploy agent"}, CurrentFocus: "review PRs for correctness, security, performance, and idiomatic Go"},
		{AgentID: "docs", Owns: []string{"docs/", "*.md files (except memory/project.md)"}, DependsOn: []string{"coding"}, MustNotTouch: []string{"internal/", "cmd/", "go.mod", "memory/project.md"}, InterfacesWith: map[string]string{"coding": "documents the code produced by coding agents", "review": "documentation may be reviewed alongside code"}, CurrentFocus: "generate README sections, godoc comments, API docs, and usage examples"},
		{AgentID: "deploy", Owns: []string{"Dockerfile", "docker-compose.yml", "Makefile (deploy targets)"}, DependsOn: []string{"review"}, MustNotTouch: []string{"internal/", "cmd/", "docs/", "memory/project.md"}, InterfacesWith: map[string]string{"review": "deploys after approved PR merge", "notify": "signals deploy result to notify agent"}, CurrentFocus: "execute deployment steps and verify health check"},
		{AgentID: "notify", Owns: []string{"Telegram/Slack notification messages"}, DependsOn: []string{"deploy"}, MustNotTouch: []string{"internal/", "cmd/", "docs/", "memory/project.md"}, InterfacesWith: map[string]string{"deploy": "receives deploy result to notify about", "review": "notifies on PR review outcomes"}, CurrentFocus: "send concise pipeline completion notifications to Telegram and Slack"},
	}

	for _, m := range manifests {
		if err := ss.Write(m); err != nil {
			return err
		}
	}
	return nil
}

// ── Trello Poller ───────────────────────────────────────────────────────────

func pollTrelloIdeas(
	ctx context.Context,
	boardID, doneListID, apiKey, token string,
	q interface{ Push(task *adapter.Task) },
	interval time.Duration,
) {
	client, err := trello.New(apiKey, token)
	if err != nil {
		log.Error().Err(err).Msg("trello poller: client init failed — poller exiting")
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cards, err := client.GetBoardCards(ctx, boardID)
			if err != nil {
				log.Error().Err(err).Str("board", boardID).Msg("trello poller: failed to fetch cards")
				continue
			}

			for _, card := range cards {
				if doneListID != "" && card.IDList == doneListID {
					continue
				}

				log.Info().Str("card", card.ID).Str("title", card.Name).Msg("trello poller: new idea card found")

				ideaID := "T-trello-idea-" + safeTruncate(card.ID, 8)
				ideaTask := &adapter.Task{
					ID:          ideaID,
					Title:       card.Name,
					Description: card.Desc,
					AgentRole:   "idea",
					Priority:    adapter.PriorityNormal,
					Status:      adapter.TaskPending,
					CreatedAt:   time.Now(),
					Meta:        map[string]string{"trello_card_id": card.ID, "trello_card_url": card.ShortURL, "source": "trello_poller"},
				}

				breakdownID := "T-trello-breakdown-" + safeTruncate(card.ID, 8)
				breakdownTask := &adapter.Task{
					ID:          breakdownID,
					Title:       "Breakdown: " + card.Name,
					Description: card.Desc,
					AgentRole:   "breakdown",
					Priority:    adapter.PriorityNormal,
					Status:      adapter.TaskPending,
					DependsOn:   []string{ideaID},
					CreatedAt:   time.Now(),
					Meta:        map[string]string{"trello_card_id": card.ID, "trello_card_url": card.ShortURL, "source": "trello_poller"},
				}

				q.Push(ideaTask)
				q.Push(breakdownTask)

				if doneListID != "" {
					if err := client.MoveCard(ctx, card.ID, doneListID); err != nil {
						log.Warn().Err(err).Str("card", card.ID).Msg("trello poller: failed to move card to done list")
					}
				}
			}
		}
	}
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func safeTruncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

func newTrelloClient(apiKey, token string) (*trello.Client, error) {
	return trello.New(apiKey, token)
}
