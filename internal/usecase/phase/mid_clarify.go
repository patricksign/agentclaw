package phase

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/patricksign/AgentClaw/internal/domain"
	"github.com/patricksign/AgentClaw/internal/port"
)

// MidPhaseClarifyInstruction is appended to system prompts for implement/test/docs phases.
// It instructs the LLM to report missing info instead of guessing.
const MidPhaseClarifyInstruction = `
IMPORTANT: If you lack information needed to complete the task, do NOT guess or assume.
Instead, return ONLY compact JSON:
{"needs_clarification":true,"questions":["what you need to know"],"partial_output":"work done so far","context":"what you are doing and where you are stuck"}
Only return this when you genuinely cannot proceed. If you have enough info, return your normal output.`

// handleMidClarify checks if an LLM response contains a clarification request.
// If so, it escalates the questions up the chain, waits for answers, and returns
// the answer to be injected into the next LLM call.
//
// Returns:
//   - (answer, true, nil)  — clarification was needed and resolved
//   - ("", false, nil)     — no clarification needed, proceed normally
//   - ("", false, err)     — escalation failed
func handleMidClarify(
	ctx context.Context,
	pctx PhaseContext,
	llmOutput string,
	currentPhase domain.ExecutionPhase,
	stepIndex int,
	partialAccum map[string]string,
) (string, bool, error) {
	// Fast path: if the output doesn't contain the clarification marker, skip parsing.
	if !strings.Contains(llmOutput, "needs_clarification") {
		return "", false, nil
	}

	var result domain.MidClarifyResult
	if err := json.Unmarshal([]byte(stripMarkdownFences(llmOutput)), &result); err != nil {
		// Not valid JSON — treat as normal output.
		return "", false, nil
	}

	if !result.NeedsClarification || len(result.Questions) == 0 {
		return "", false, nil
	}

	// Build question text with context.
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Agent stuck during %s phase:\n", currentPhase))
	if result.Context != "" {
		sb.WriteString(fmt.Sprintf("Context: %s\n", result.Context))
	}
	sb.WriteString("Questions:\n")
	for i, q := range result.Questions {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, q))
	}
	questionText := sb.String()

	// Save checkpoint with partial output before escalating.
	accum := make(map[string]string, len(partialAccum)+2)
	for k, v := range partialAccum {
		accum[k] = v
	}
	accum["partial_output"] = result.PartialOutput
	accum["pending_questions"] = questionText
	saveCheckpoint(pctx, pctx.Task.ID, currentPhase, stepIndex, "mid_clarify", accum)

	// Notify status channel.
	dispatchEvent(ctx, pctx.Notifier, domain.Event{
		Type:      domain.EventQuestionAsked,
		Channel:   domain.StatusChannel,
		TaskID:    pctx.Task.ID,
		AgentID:   pctx.AgentCfg.ID,
		AgentRole: pctx.AgentCfg.Role,
		Model:     pctx.AgentCfg.Model,
		Payload:   map[string]string{"message": questionText},
	})

	// Escalate via the existing chain: agent model -> sonnet -> opus -> human.
	// The Escalator.Resolve already handles the full chain + checkpoint + 24h timeout.
	escalResult, err := pctx.Escalator.Resolve(ctx, port.EscalatorRequest{
		Question:    questionText,
		TaskContext: result.Context,
		AgentModel:  pctx.AgentCfg.Model,
		AgentRole:   pctx.AgentCfg.Role,
		TaskID:      pctx.Task.ID,
		QuestionID:  fmt.Sprintf("%s-mid-%s-%d", pctx.Task.ID, currentPhase, stepIndex),
	})
	if err != nil {
		return "", false, fmt.Errorf("mid_clarify: escalate: %w", err)
	}

	if escalResult.NeedsHuman {
		// Will be resumed when human answers — the phase is suspended.
		return "", false, fmt.Errorf("mid_clarify: suspended waiting for human")
	}

	// Build answer context to inject into next LLM call.
	answer := fmt.Sprintf("\n<!-- CLARIFICATION ANSWER (from %s) -->\n%s\n<!-- END CLARIFICATION -->\n",
		escalResult.AnsweredBy, escalResult.Answer)

	return answer, true, nil
}
