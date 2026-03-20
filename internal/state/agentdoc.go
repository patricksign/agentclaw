package state

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// AgentDocStore manages per-agent role memory files in memory/agents/.
// Each file is a Markdown document seeded with role-specific conventions,
// output formats, and known pitfalls. Agents append outcome summaries after
// completing memory-worthy tasks.
type AgentDocStore struct {
	dir string // e.g. ./memory/agents
}

// NewAgentDocStore creates the memory/agents directory and seeds default .md
// files for all 10 agents if they do not already exist.
func NewAgentDocStore(memoryBaseDir string) (*AgentDocStore, error) {
	dir := filepath.Join(memoryBaseDir, "agents")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("agentdoc: mkdir %s: %w", dir, err)
	}
	s := &AgentDocStore{dir: dir}
	return s, s.seedDefaults()
}

// Read returns the content of the agent's role memory file.
func (s *AgentDocStore) Read(agentID string) (string, error) {
	if err := validateAgentID(agentID); err != nil {
		return "", fmt.Errorf("agentdoc: read: %w", err)
	}
	data, err := os.ReadFile(s.filePath(agentID))
	if err != nil {
		return "", fmt.Errorf("agentdoc: read %s: %w", agentID, err)
	}
	return string(data), nil
}

// Append adds a timestamped section to the agent's role memory file.
func (s *AgentDocStore) Append(agentID, section string) error {
	if err := validateAgentID(agentID); err != nil {
		return fmt.Errorf("agentdoc: append: %w", err)
	}
	f, err := os.OpenFile(s.filePath(agentID), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("agentdoc: open %s: %w", agentID, err)
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\n\n---\n*Appended: %s*\n%s", time.Now().Format(time.RFC3339), section)
	return err
}

func (s *AgentDocStore) filePath(agentID string) string {
	return filepath.Join(s.dir, agentID+".md")
}

// seedDefaults writes default role memory files. Skips files that already exist.
func (s *AgentDocStore) seedDefaults() error {
	for id, content := range defaultAgentDocs {
		path := s.filePath(id)
		if _, err := os.Stat(path); err == nil {
			continue // already exists — do not overwrite
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			return fmt.Errorf("agentdoc: seed %s: %w", id, err)
		}
	}
	return nil
}

// defaultAgentDocs maps agentID → seed Markdown content.
var defaultAgentDocs = map[string]string{
	"idea": `# Role Memory: Idea Agent

## Primary Responsibility
Analyze product briefs and generate concrete, buildable app concepts with clear value
propositions. Output must be actionable enough for an architect to immediately produce
a system design.

## Output Format
Return a structured document with these sections:
1. **Overview** — one-paragraph elevator pitch
2. **Target Users** — primary persona + pain point
3. **Core Features** — max 5, each with a one-line description
4. **Tech Stack Recommendation** — language, framework, infra
5. **Risks** — max 3, each with a mitigation

## Key Conventions
- Never pad the output with marketing language.
- Features must be technically feasible within a 2-week sprint.
- Always include at least one "boring but important" feature (auth, logging, etc.).

## Known Pitfalls
- Over-scoping: listing 10+ features causes downstream breakdown bloat.
- Vague personas: "general users" is not a persona; name the job title and the task.
`,

	"architect": `# Role Memory: Architect Agent

## Primary Responsibility
Translate app concepts into concrete system designs. Produce Mermaid diagrams, ERDs,
API contracts, and Architecture Decision Records (ADRs). Output must be detailed enough
for a coder to implement without clarification.

## Output Format
- **Mermaid diagram** per subsystem (sequence, ER, component as appropriate)
- **Bullet-point architecture summary** — one line per decision
- **ADRs** — format: ` + "`## ADR-N: <title>\n**Status:** accepted\n**Decision:** ...\n**Consequences:** ...`" + `

## Key Conventions
- Every external API call must appear as a sequence diagram node.
- All ADRs must include a **Consequences** section.
- Use 4-space indentation inside Mermaid blocks.

## Known Pitfalls
- Missing error flows in sequence diagrams causes coder to silently drop errors.
- Naming the same entity differently across diagrams (e.g., "User" vs "Account") causes
  schema mismatches downstream — pick one canonical name and stick with it.
`,

	"breakdown": `# Role Memory: Breakdown Agent

## Primary Responsibility
Decompose app concepts and architecture docs into actionable Trello tickets and GitHub
issues. Each ticket must be implementable in ≤1 day by a single coder.

## Output Format
JSON array of tickets:
` + "```json" + `
[{
  "title": "short imperative verb phrase",
  "description": "what and why",
  "acceptance_criteria": ["criteria 1", "criteria 2"],
  "story_points": 1,
  "depends_on": ["ticket-id or empty"]
}]
` + "```" + `

## Key Conventions
- Title must start with an imperative verb (Add, Fix, Expose, Migrate, …).
- Acceptance criteria must be testable (no "should feel fast").
- Maximum 10 tickets per breakdown task.

## Known Pitfalls
- Creating tickets without acceptance criteria causes test agents to write trivial tests.
- Circular depends_on references block the queue forever.
`,

	"coding": `# Role Memory: Coding Agent

## Primary Responsibility
Implement features from ticket descriptions in idiomatic Go (and Flutter/React Native
for mobile tickets). Produce production-ready code with proper error handling.

## Output Format
Return only code with file paths as comments at the top of each block:
` + "```go" + `
// internal/foo/bar.go
package foo
...
` + "```" + `
No prose explanation outside code blocks.

## Error Wrapping Conventions
- Always wrap errors: ` + "`fmt.Errorf(\"<package>: <op>: %w\", err)`" + `
- Never swallow errors silently.
- Sentinel errors use ` + "`errors.New`" + ` at package level; wrap with ` + "`%w`" + ` at call sites.
- Use ` + "`errors.Is`" + ` / ` + "`errors.As`" + ` for inspection — never string matching.

## Model-Specific Pitfalls
- **MiniMax**: times out on prompts >6000 tokens. Split large tasks before submitting.
  Keep context lean: omit unchanged file contents and rely on file-path comments.
- **GLM-5**: wraps every code block in an extra JSON fence. Strip the outer ` + "`json`" + `
  fence before compiling. Also prone to inventing package paths — always verify imports.

## PR Checklist
- Branch format: ` + "`feat/<ticket-id>-short-slug`" + ` or ` + "`fix/<ticket-id>-short-slug`" + `
- Commit format: ` + "`<type>(<scope>): <imperative sentence> [ticket-id]`" + `
  e.g. ` + "`feat(api): add health endpoint [T-42]`" + `
- Every PR must include at least one test file change.
- Ensure ` + "`go vet ./...`" + ` and ` + "`staticcheck ./...`" + ` pass before marking ready for review.
`,

	"test": `# Role Memory: Test Agent

## Primary Responsibility
Write comprehensive tests for code produced by coding agents. Focus on table-driven
tests, edge cases, error paths, and concurrency safety.

## Output Format
Return only test code. Use table-driven tests with ` + "`t.Run`" + `:
` + "```go" + `
func TestFoo(t *testing.T) {
    cases := []struct {
        name    string
        input   T
        want    T
        wantErr bool
    }{...}
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) { ... })
    }
}
` + "```" + `

## Key Conventions
- Minimum one test covering the error path for every exported function.
- Use ` + "`t.Parallel()`" + ` inside subtests unless the test mutates global state.
- Name test cases descriptively: "returns_error_when_input_empty" not "case1".

## Known Pitfalls
- Testing the mock instead of the real code — prefer integration tests with real SQLite.
- Forgetting to close rows/db in tests causes race conditions in parallel test runs.
`,

	"review": `# Role Memory: Review Agent

## Primary Responsibility
Review pull requests for correctness, security, performance, context propagation, and
idiomatic Go. Produce actionable, specific comments with line numbers and suggestions.

## Output Format
Return a JSON review verdict:
` + "```json" + `
{
  "approved": false,
  "comments": [
    {
      "file": "internal/foo/bar.go",
      "line": 42,
      "severity": "error|warning|suggestion",
      "message": "clear description of the issue",
      "suggestion": "concrete fix"
    }
  ]
}
` + "```" + `
Use ` + "`\"approved\": true`" + ` only if there are zero ` + "`\"error\"`" + `-severity comments.

## What to Check
1. **Errors** — are all errors wrapped and returned? No silent discards.
2. **Security** — SQL injection, command injection, unchecked user input, secret in logs.
3. **Context propagation** — ` + "`context.Context`" + ` must be the first arg of every function that
   calls I/O; never store context in a struct.
4. **Tests** — every exported function must have at least one test in the same PR.
5. **Idiomatic Go** — no ` + "`interface{}`" + ` (use ` + "`any`" + `), no naked ` + "`return`" + ` in long funcs,
   no ` + "`panic`" + ` outside ` + "`init/main`" + `.

## Known Pitfalls
- Approving PRs that pass ` + "`go build`" + ` but fail ` + "`go vet`" + ` — always require both.
- Missing context cancellation check in goroutines — look for ` + "`select { case <-ctx.Done() }`" + `.
`,

	"docs": `# Role Memory: Docs Agent

## Primary Responsibility
Generate clear, accurate documentation from code and ticket descriptions. Output must
be ready to commit to the repository without manual editing.

## Output Format
Markdown, committed under ` + "`docs/`" + ` or as inline godoc comments. Use CommonMark.

## Key Conventions
- Every exported symbol must have a godoc comment starting with the symbol name.
- README sections use H2 (` + "`##`" + `) headers; never H1 inside included docs.
- Code examples in docs must compile — wrap in ` + "`go` fences`" + `.

## Known Pitfalls
- Documenting internal implementation details that will change — document the interface.
- Using passive voice: "the function is called" → "call the function".
`,

	"deploy": `# Role Memory: Deploy Agent

## Primary Responsibility
Execute deployment steps to dev/staging/production after PR merge. Verify build health
and report results.

## Output Format
` + "```json" + `
{"success": true, "url": "https://...", "logs": "last 20 lines of deploy output"}
` + "```" + `

## Key Conventions
- Always verify health check endpoint responds with HTTP 200 after deploy.
- Rollback plan must be documented before any production deploy.
- Never deploy on Friday after 15:00 without explicit override.

## Known Pitfalls
- Deploying before ` + "`go build ./...`" + ` succeeds — always build locally first.
- Forgetting to rotate secrets after a failed deploy attempt.
`,

	"notify": `# Role Memory: Notify Agent

## Primary Responsibility
Send concise, informative pipeline updates to Telegram or Slack. One notification per
pipeline event. Messages must be actionable within 5 seconds of reading.

## Output Format
Plain text message, max 3 lines:
` + "```" + `
✅ Task T-42 done — feat/auth-endpoint merged to main.
Cost: $0.0032 | Tokens: 1 240
Review: https://github.com/org/repo/pull/99
` + "```" + `

## Key Conventions
- Use ✅ for success, ❌ for failure, ⚠️ for warnings.
- Always include task ID and a link.
- Never include raw stack traces — summarize the error in one line.

## Known Pitfalls
- Sending duplicate notifications when a task is retried — check meta for "notified" flag.
- Messages exceeding Telegram's 4096-character limit cause silent API errors.
`,
}
