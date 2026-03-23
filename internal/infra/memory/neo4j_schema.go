package memory

import (
	"context"
	"log/slog"
	"strings"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// EnsureNeo4jSchema creates uniqueness constraints and indexes.
// Each statement runs in its own transaction; "already exists" errors are ignored.
func EnsureNeo4jSchema(ctx context.Context, driver neo4j.DriverWithContext) error {
	statements := []string{
		// Uniqueness constraints
		`CREATE CONSTRAINT agent_id_unique IF NOT EXISTS FOR (a:Agent) REQUIRE a.id IS UNIQUE`,
		`CREATE CONSTRAINT task_id_unique  IF NOT EXISTS FOR (t:Task)  REQUIRE t.id IS UNIQUE`,
		`CREATE CONSTRAINT module_id_unique IF NOT EXISTS FOR (m:Module) REQUIRE m.id IS UNIQUE`,
		`CREATE CONSTRAINT skill_agent_unique IF NOT EXISTS FOR (s:Skill) REQUIRE s.agentID IS UNIQUE`,

		// Indexes
		`CREATE INDEX task_status_idx IF NOT EXISTS FOR (t:Task) ON (t.status)`,
		`CREATE INDEX task_role_idx   IF NOT EXISTS FOR (t:Task) ON (t.role)`,
		`CREATE INDEX module_pkg_idx  IF NOT EXISTS FOR (m:Module) ON (m.package)`,
	}

	sess := driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer sess.Close(ctx)

	for _, stmt := range statements {
		_, err := sess.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
			_, err := tx.Run(ctx, stmt, nil)
			return nil, err
		})
		if err != nil {
			if isAlreadyExistsErr(err) {
				slog.Debug("neo4j schema already exists", "statement", stmt)
				continue
			}
			return err
		}
	}
	return nil
}

func isAlreadyExistsErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "already exists") ||
		strings.Contains(msg, "EquivalentSchemaRuleAlreadyExists") ||
		strings.Contains(msg, "An equivalent constraint already exists")
}
