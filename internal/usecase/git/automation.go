package git

import (
	"context"
	"fmt"
	"strings"

	"github.com/patricksign/AgentClaw/internal/domain"
	"github.com/patricksign/AgentClaw/internal/port"
)

// Automation handles git operations for the pipeline:
// commit changes, push to remote, create branches.
type Automation struct {
	runner port.CommandRunner
}

// NewAutomation creates a git automation handler.
func NewAutomation(runner port.CommandRunner) *Automation {
	return &Automation{runner: runner}
}

// CommitAndPush stages all changes, commits with the given message, and pushes.
func (a *Automation) CommitAndPush(ctx context.Context, workDir, branch, commitMsg string) error {
	steps := []struct {
		name string
		args []string
	}{
		{"git add", []string{"add", "."}},
		{"git commit", []string{"commit", "-m", commitMsg}},
		{"git push", []string{"push", "-u", "origin", branch}},
	}

	for _, step := range steps {
		result, err := a.runner.Run(ctx, workDir, "git", step.args...)
		if err != nil {
			return fmt.Errorf("git %s: %w", step.name, err)
		}
		if result.ExitCode != 0 {
			// git commit returns exit 1 when there's nothing to commit — not an error.
			if step.name == "git commit" && strings.Contains(result.Stdout, "nothing to commit") {
				continue
			}
			return fmt.Errorf("git %s failed (exit %d): %s", step.name, result.ExitCode, result.Stderr)
		}
	}

	return nil
}

// CreateBranch creates and checks out a new branch.
func (a *Automation) CreateBranch(ctx context.Context, workDir, branchName string) error {
	result, err := a.runner.Run(ctx, workDir, "git", "checkout", "-b", branchName)
	if err != nil {
		return fmt.Errorf("git create branch: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("git checkout -b %s failed: %s", branchName, result.Stderr)
	}
	return nil
}

// ParseArtifacts extracts file artifacts from LLM output.
// Expects code blocks with file paths as comments:
//
//	// filepath: cmd/main.go
//	package main
//	...
//
// Returns artifacts with relative paths and content.
func ParseArtifacts(llmOutput string) []domain.Artifact {
	var artifacts []domain.Artifact
	lines := strings.Split(llmOutput, "\n")

	var currentPath string
	var currentContent strings.Builder
	inBlock := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Detect file path markers.
		if strings.HasPrefix(trimmed, "// filepath:") || strings.HasPrefix(trimmed, "# filepath:") {
			// Save previous artifact if any.
			if currentPath != "" && currentContent.Len() > 0 {
				artifacts = append(artifacts, domain.Artifact{
					Type:    artifactType(currentPath),
					Path:    currentPath,
					Content: strings.TrimSpace(currentContent.String()),
				})
			}
			currentPath = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(trimmed, "// filepath:"), "# filepath:"))
			currentContent.Reset()
			inBlock = true
			continue
		}

		// Detect code block boundaries.
		if strings.HasPrefix(trimmed, "```") {
			if inBlock && currentContent.Len() > 0 {
				// End of code block.
				if currentPath != "" {
					artifacts = append(artifacts, domain.Artifact{
						Type:    artifactType(currentPath),
						Path:    currentPath,
						Content: strings.TrimSpace(currentContent.String()),
					})
					currentPath = ""
					currentContent.Reset()
				}
				inBlock = false
			} else {
				inBlock = true
			}
			continue
		}

		if inBlock && currentPath != "" {
			currentContent.WriteString(line)
			currentContent.WriteByte('\n')
		}
	}

	// Flush last artifact.
	if currentPath != "" && currentContent.Len() > 0 {
		artifacts = append(artifacts, domain.Artifact{
			Type:    artifactType(currentPath),
			Path:    currentPath,
			Content: strings.TrimSpace(currentContent.String()),
		})
	}

	return artifacts
}

// WriteArtifacts writes all artifacts to disk via the FileWriter.
func WriteArtifacts(writer port.FileWriter, artifacts []domain.Artifact) error {
	for _, a := range artifacts {
		if a.Path == "" || a.Content == "" {
			continue
		}
		if err := writer.WriteFile(a.Path, a.Content); err != nil {
			return fmt.Errorf("write artifact %s: %w", a.Path, err)
		}
	}
	return nil
}

func artifactType(path string) string {
	if strings.HasSuffix(path, "_test.go") || strings.Contains(path, "test") {
		return "test"
	}
	if strings.HasSuffix(path, ".md") {
		return "doc"
	}
	return "code"
}
