package phase

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/patricksign/AgentClaw/internal/domain"
	"github.com/patricksign/AgentClaw/internal/port"
)

const maxIdeaRounds = 10

const ideaClarifySystem = `You are a product analyst. Your job is to collect COMPLETE requirements from the human.

RULES:
- NEVER assume or fill in information the human has not explicitly provided.
- NEVER use default values. If the human has not said it, ASK.
- Ask ALL missing questions in one batch (do not ask one at a time).
- After each human reply, re-evaluate: is anything still missing or ambiguous?
- Only return ready=true when ALL required fields have explicit human answers.
- If the human's answer is vague (e.g. "whatever", "any"), ask again with specific options.

REQUIRED fields (must all have human-provided answers):
1. frontend_framework (Flutter, React Native, Web, SwiftUI, ...)
2. backend_language (Go, Node.js, Python, Java, ...)
3. repo_url (GitHub/GitLab repository URL)
4. repo_structure (mono-repo or multi-repo)
5. database (PostgreSQL, MySQL, MongoDB, Firebase, SQLite, ...)
6. auth_method (Google OAuth, email/password, phone OTP, ...)
7. target_platforms (iOS, Android, Web — can be multiple)
8. third_party_integrations (payment provider, maps, push notifications, etc. — "none" is a valid answer)

Return ONLY compact JSON (no whitespace, no markdown fences):
When NOT ready: {"ready":false,"questions":["question 1","question 2"]}
When ready:     {"ready":true,"concept":"structured concept summary","config":{"frontend_framework":"...","backend_language":"...","repo_url":"...","repo_structure":"...","database":"...","auth_method":"...","target_platforms":["..."],"integrations":["..."]}}`

// IdeaClarifyPhase runs the Opus <-> Human clarification loop.
// It asks the human for all missing project info, NEVER guessing.
type IdeaClarifyPhase struct{}

func (p *IdeaClarifyPhase) Run(ctx context.Context, pctx PhaseContext) domain.PhaseResult {
	task := pctx.Task

	// Load previous round context from checkpoint if resuming.
	var previousAnswers []string
	if cp := loadCheckpoint(pctx, task.ID); cp != nil && cp.Phase == domain.PhaseIdeaClarify {
		if prev := cp.GetAccumulated("answers"); prev != "" {
			previousAnswers = strings.Split(prev, "|||")
		}
	}

	for round := range maxIdeaRounds {
		// Build user message with idea + all accumulated human answers.
		userMsg := p.buildUserMessage(task, previousAnswers)

		// Save checkpoint before LLM call.
		saveCheckpoint(pctx, task.ID, domain.PhaseIdeaClarify, round, "opus_analyze", map[string]string{
			"answers": strings.Join(previousAnswers, "|||"),
			"round":   fmt.Sprintf("%d", round),
		})

		// Call Opus.
		callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		req := port.LLMRequest{
			Model:     domain.ModelOpus,
			System:    ideaClarifySystem,
			Messages:  []port.LLMMessage{{Role: "user", Content: userMsg}},
			MaxTokens: 2048,
			TaskID:    task.ID,
		}
		if domain.SupportsPromptCache(domain.ModelOpus) {
			req.CacheControl = &port.LLMCacheControl{
				CacheSystem: true,
				TTL:         domain.CacheTTLForContent("system"),
			}
		}
		resp, err := pctx.Router.Call(callCtx, req)
		cancel()
		if err != nil {
			return domain.PhaseResult{Err: fmt.Errorf("idea_clarify round %d: %w", round, err)}
		}

		// Parse Opus response.
		raw := stripMarkdownFences(resp.Content)
		var result domain.IdeaClarifyResult
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return domain.PhaseResult{Err: fmt.Errorf("idea_clarify: parse JSON round %d: %w", round, err)}
		}

		// Ready — all info collected.
		if result.Ready {
			task.Understanding = result.Concept
			task.Description = result.Concept
			task.Phase = domain.PhaseUnderstand

			// Save project config as accumulated context for downstream phases.
			configJSON, _ := json.Marshal(result.Config)
			saveCheckpoint(pctx, task.ID, domain.PhaseUnderstand, 0, "idea_complete", map[string]string{
				"project_config": string(configJSON),
				"concept":        result.Concept,
			})

			if err := pctx.TaskStore.SaveTask(task); err != nil {
				return domain.PhaseResult{Err: fmt.Errorf("idea_clarify: save task: %w", err)}
			}

			dispatchEvent(ctx, pctx.Notifier, domain.Event{
				Type:    domain.EventPhaseTransition,
				Channel: domain.StatusChannel,
				TaskID:  task.ID,
				AgentID: pctx.AgentCfg.ID,
				Payload: map[string]string{
					"message": fmt.Sprintf("Idea clarification complete after %d rounds — moving to understand", round+1),
				},
			})

			return domain.PhaseResult{Done: true}
		}

		// Not ready — ask human.
		if len(result.Questions) == 0 {
			return domain.PhaseResult{Err: fmt.Errorf("idea_clarify: opus returned ready=false with no questions")}
		}

		// Batch all questions into one Telegram message.
		questionText := fmt.Sprintf("Project setup — round %d/%d:\n", round+1, maxIdeaRounds)
		for i, q := range result.Questions {
			questionText += fmt.Sprintf("%d. %s\n", i+1, q)
		}

		// Save checkpoint before human wait.
		saveCheckpoint(pctx, task.ID, domain.PhaseIdeaClarify, round, "waiting_human", map[string]string{
			"answers":   strings.Join(previousAnswers, "|||"),
			"questions": questionText,
		})

		dispatchEvent(ctx, pctx.Notifier, domain.Event{
			Type:    domain.EventQuestionAsked,
			Channel: domain.HumanChannel,
			TaskID:  task.ID,
			AgentID: pctx.AgentCfg.ID,
			Payload: map[string]string{"message": questionText},
		})

		// Escalate to human — blocks until human replies or 24h timeout.
		escalResult, err := pctx.Escalator.Resolve(ctx, port.EscalatorRequest{
			Question:   questionText,
			TaskContext: task.Description,
			AgentModel: domain.ModelOpus,
			AgentRole:  "idea",
			TaskID:     task.ID,
			QuestionID: fmt.Sprintf("%s-idea-r%d", task.ID, round),
		})
		if err != nil {
			return domain.PhaseResult{Err: fmt.Errorf("idea_clarify: escalate round %d: %w", round, err)}
		}

		if escalResult.NeedsHuman {
			// 24h timeout — suspend.
			return domain.PhaseResult{Suspended: true}
		}

		// Accumulate human answer for next round.
		previousAnswers = append(previousAnswers, escalResult.Answer)
	}

	// Exhausted max rounds.
	_ = dispatchCritical(ctx, pctx.Notifier, domain.Event{
		Type:    domain.EventTaskFailed,
		Channel: domain.HumanChannel,
		TaskID:  task.ID,
		Payload: map[string]string{
			"message": fmt.Sprintf("Idea clarification failed — could not collect complete requirements after %d rounds", maxIdeaRounds),
		},
	})

	return domain.PhaseResult{Err: fmt.Errorf("idea_clarify: exhausted %d rounds without complete requirements", maxIdeaRounds)}
}

// buildUserMessage assembles the LLM prompt from the original idea + all human answers so far.
func (p *IdeaClarifyPhase) buildUserMessage(task *domain.Task, previousAnswers []string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Original idea: %s\n\n%s\n", task.Title, task.Description))

	if len(previousAnswers) > 0 {
		sb.WriteString("\n--- Human answers from previous rounds ---\n")
		for i, a := range previousAnswers {
			sb.WriteString(fmt.Sprintf("Round %d answer: %s\n\n", i+1, a))
		}
		sb.WriteString("--- End of previous answers ---\n\n")
		sb.WriteString("Re-evaluate: are ALL required fields now answered? If yes, return ready=true. If not, ask what's still missing.\n")
	}

	return sb.String()
}
