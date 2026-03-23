package memory

import (
	"context"
	"fmt"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/patricksign/AgentClaw/internal/port"
)

var _ port.KnowledgeGraph = (*Neo4jGraph)(nil)

type Neo4jGraph struct {
	driver neo4j.DriverWithContext
	db     string
}

func NewNeo4jGraph(driver neo4j.DriverWithContext) *Neo4jGraph {
	return &Neo4jGraph{driver: driver, db: "neo4j"}
}

func (n *Neo4jGraph) write(ctx context.Context) neo4j.SessionWithContext {
	return n.driver.NewSession(ctx, neo4j.SessionConfig{DatabaseName: n.db, AccessMode: neo4j.AccessModeWrite})
}

func (n *Neo4jGraph) read(ctx context.Context) neo4j.SessionWithContext {
	return n.driver.NewSession(ctx, neo4j.SessionConfig{DatabaseName: n.db, AccessMode: neo4j.AccessModeRead})
}

// ─── Task Dependency ────────────────────────────────────────────────────────

func (n *Neo4jGraph) UpsertTask(ctx context.Context, node port.TaskNode) error {
	sess := n.write(ctx)
	defer sess.Close(ctx)
	_, err := sess.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		_, err := tx.Run(ctx, `
			MERGE (t:Task {id: $id})
			SET t.title=$title, t.status=$status, t.phase=$phase,
			    t.agentID=$agentID, t.role=$role, t.complexity=$complexity,
			    t.updatedAt=datetime()
		`, map[string]any{
			"id": node.TaskID, "title": node.Title, "status": node.Status,
			"phase": node.Phase, "agentID": node.AgentID, "role": node.Role,
			"complexity": node.Complexity,
		})
		return nil, err
	})
	return err
}

// validTaskRelations is the allowlist for Cypher relationship types.
var validTaskRelations = map[port.TaskRelation]bool{
	port.RelDependsOn: true,
	port.RelBlockedBy: true,
	port.RelSubtaskOf: true,
	port.RelRelatedTo: true,
}

func (n *Neo4jGraph) LinkTaskDependency(ctx context.Context, fromID, toID string, rel port.TaskRelation) error {
	if !validTaskRelations[rel] {
		return fmt.Errorf("invalid task relation: %q", rel)
	}
	sess := n.write(ctx)
	defer sess.Close(ctx)
	_, err := sess.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		query := fmt.Sprintf(`
			MATCH (a:Task {id:$from}), (b:Task {id:$to})
			MERGE (a)-[r:%s]->(b)
			SET r.at=datetime()
		`, string(rel))
		_, err := tx.Run(ctx, query, map[string]any{"from": fromID, "to": toID})
		return nil, err
	})
	return err
}

func (n *Neo4jGraph) GetBlockingTasks(ctx context.Context, taskID string) ([]port.TaskNode, error) {
	sess := n.read(ctx)
	defer sess.Close(ctx)
	res, err := sess.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		r, err := tx.Run(ctx, `
			MATCH (blocker:Task)-[:DEPENDS_ON|BLOCKS]->(t:Task {id:$id})
			WHERE blocker.status <> 'done'
			RETURN blocker.id AS id, blocker.title AS title, blocker.status AS status,
			       blocker.phase AS phase, blocker.agentID AS agentID, blocker.role AS role
		`, map[string]any{"id": taskID})
		if err != nil {
			return nil, err
		}
		return collectTaskNodes(ctx, r)
	})
	if err != nil {
		return nil, err
	}
	return res.([]port.TaskNode), nil
}

func (n *Neo4jGraph) GetDependentTasks(ctx context.Context, taskID string) ([]port.TaskNode, error) {
	sess := n.read(ctx)
	defer sess.Close(ctx)
	res, err := sess.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		r, err := tx.Run(ctx, `
			MATCH (t:Task {id:$id})-[:DEPENDS_ON]->(dep:Task)
			RETURN dep.id AS id, dep.title AS title, dep.status AS status,
			       dep.phase AS phase, dep.agentID AS agentID, dep.role AS role
		`, map[string]any{"id": taskID})
		if err != nil {
			return nil, err
		}
		return collectTaskNodes(ctx, r)
	})
	if err != nil {
		return nil, err
	}
	return res.([]port.TaskNode), nil
}

// ─── Module Graph ───────────────────────────────────────────────────────────

func (n *Neo4jGraph) UpsertModule(ctx context.Context, mod port.ModuleNode) error {
	sess := n.write(ctx)
	defer sess.Close(ctx)
	_, err := sess.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		_, err := tx.Run(ctx, `
			MERGE (m:Module {id:$id})
			SET m.name=$name, m.package=$pkg, m.filePath=$path
		`, map[string]any{"id": mod.ModuleID, "name": mod.Name, "pkg": mod.Package, "path": mod.FilePath})
		return nil, err
	})
	return err
}

func (n *Neo4jGraph) LinkModuleImport(ctx context.Context, fromMod, toMod string) error {
	sess := n.write(ctx)
	defer sess.Close(ctx)
	_, err := sess.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		_, err := tx.Run(ctx, `
			MATCH (a:Module {id:$from}), (b:Module {id:$to})
			MERGE (a)-[:IMPORTS]->(b)
		`, map[string]any{"from": fromMod, "to": toMod})
		return nil, err
	})
	return err
}

func (n *Neo4jGraph) FindCircularImports(ctx context.Context, moduleID string) ([][]string, error) {
	sess := n.read(ctx)
	defer sess.Close(ctx)
	res, err := sess.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		r, err := tx.Run(ctx, `
			MATCH path=(m:Module {id:$id})-[:IMPORTS*2..10]->(m)
			RETURN [n IN nodes(path) | n.id] AS cycle
			LIMIT 10
		`, map[string]any{"id": moduleID})
		if err != nil {
			return nil, err
		}
		var cycles [][]string
		for r.Next(ctx) {
			rec := r.Record()
			if v, ok := rec.Get("cycle"); ok {
				raw := v.([]any)
				cycle := make([]string, len(raw))
				for i, x := range raw {
					cycle[i] = fmt.Sprintf("%v", x)
				}
				cycles = append(cycles, cycle)
			}
		}
		return cycles, r.Err()
	})
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	return res.([][]string), nil
}

const maxGraphDepth = 20

func (n *Neo4jGraph) GetModuleSubgraph(ctx context.Context, moduleID string, depth int) (*port.ModuleGraph, error) {
	if depth <= 0 {
		depth = 1
	}
	if depth > maxGraphDepth {
		depth = maxGraphDepth
	}
	sess := n.read(ctx)
	defer sess.Close(ctx)
	res, err := sess.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		r, err := tx.Run(ctx, fmt.Sprintf(`
			MATCH (root:Module {id:$id})
			OPTIONAL MATCH (root)-[:IMPORTS*1..%d]->(dep:Module)
			OPTIONAL MATCH (importer:Module)-[:IMPORTS]->(root)
			RETURN root.id AS rootID, root.name AS rootName,
			       collect(DISTINCT {id:dep.id, name:dep.name}) AS imports,
			       collect(DISTINCT {id:importer.id, name:importer.name}) AS importedBy
		`, depth), map[string]any{"id": moduleID})
		if err != nil {
			return nil, err
		}
		if !r.Next(ctx) {
			return &port.ModuleGraph{Root: port.ModuleNode{ModuleID: moduleID}}, nil
		}
		rec := r.Record()
		g := &port.ModuleGraph{
			Root: port.ModuleNode{
				ModuleID: neoStr(rec, "rootID"),
				Name:     neoStr(rec, "rootName"),
			},
		}
		if v, ok := rec.Get("imports"); ok {
			for _, x := range v.([]any) {
				m := x.(map[string]any)
				g.Imports = append(g.Imports, port.ModuleNode{
					ModuleID: fmt.Sprintf("%v", m["id"]),
					Name:     fmt.Sprintf("%v", m["name"]),
				})
			}
		}
		if v, ok := rec.Get("importedBy"); ok {
			for _, x := range v.([]any) {
				m := x.(map[string]any)
				g.ImportedBy = append(g.ImportedBy, port.ModuleNode{
					ModuleID: fmt.Sprintf("%v", m["id"]),
					Name:     fmt.Sprintf("%v", m["name"]),
				})
			}
		}
		return g, nil
	})
	if err != nil {
		return nil, err
	}
	return res.(*port.ModuleGraph), nil
}

// ─── Agent Scope ────────────────────────────────────────────────────────────

func (n *Neo4jGraph) UpsertAgentScope(ctx context.Context, scope port.AgentScope) error {
	sess := n.write(ctx)
	defer sess.Close(ctx)
	_, err := sess.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		_, err := tx.Run(ctx, `
			MERGE (a:Agent {id:$agentID})
			SET a.role=$role, a.owns=$owns, a.mustNotTouch=$mustNot
		`, map[string]any{
			"agentID": scope.AgentID, "role": scope.Role,
			"owns": scope.Owns, "mustNot": scope.MustNotTouch,
		})
		return nil, err
	})
	return err
}

func (n *Neo4jGraph) GetScopeConflicts(ctx context.Context, agentID string, paths []string) ([]port.ScopeConflict, error) {
	sess := n.read(ctx)
	defer sess.Close(ctx)
	res, err := sess.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		r, err := tx.Run(ctx, `
			MATCH (other:Agent)
			WHERE other.id <> $agentID
			  AND any(p IN $paths WHERE p IN other.owns)
			RETURN other.id AS conflictAgent, other.role AS conflictRole,
			       [p IN $paths WHERE p IN other.owns] AS conflictPaths
		`, map[string]any{"agentID": agentID, "paths": paths})
		if err != nil {
			return nil, err
		}
		var conflicts []port.ScopeConflict
		for r.Next(ctx) {
			rec := r.Record()
			if ps, ok := rec.Get("conflictPaths"); ok {
				for _, p := range ps.([]any) {
					conflicts = append(conflicts, port.ScopeConflict{
						Path:         fmt.Sprintf("%v", p),
						ConflictWith: neoStr(rec, "conflictAgent"),
						ConflictRole: neoStr(rec, "conflictRole"),
					})
				}
			}
		}
		return conflicts, r.Err()
	})
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	return res.([]port.ScopeConflict), nil
}

// ─── Skill ──────────────────────────────────────────────────────────────────

func (n *Neo4jGraph) RecordTaskOutcome(ctx context.Context, agentID, taskID, role string, success bool, costUSD float64) error {
	sess := n.write(ctx)
	defer sess.Close(ctx)
	succ := 0
	if success {
		succ = 1
	}
	_, err := sess.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		_, err := tx.Run(ctx, `
			MERGE (a:Agent {id:$agentID}) SET a.role=$role
			MERGE (s:Skill {agentID:$agentID})
			ON CREATE SET s.total=1, s.success=$succ, s.cost=$cost
			ON MATCH  SET s.total=s.total+1, s.success=s.success+$succ, s.cost=s.cost+$cost
			MERGE (a)-[:HAS_SKILL]->(s)
		`, map[string]any{"agentID": agentID, "role": role, "succ": succ, "cost": costUSD})
		return nil, err
	})
	return err
}

func (n *Neo4jGraph) GetAgentSkillSummary(ctx context.Context, agentID string) (*port.AgentSkill, error) {
	sess := n.read(ctx)
	defer sess.Close(ctx)
	res, err := sess.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		r, err := tx.Run(ctx, `
			MATCH (a:Agent {id:$id})-[:HAS_SKILL]->(s:Skill)
			RETURN a.role AS role, s.total AS total, s.success AS succ, s.cost AS cost
		`, map[string]any{"id": agentID})
		if err != nil {
			return nil, err
		}
		if !r.Next(ctx) {
			return nil, nil
		}
		rec := r.Record()
		total, _ := rec.Get("total")
		succ, _ := rec.Get("succ")
		cost, _ := rec.Get("cost")
		totalN := total.(int64)
		var sr float64
		if totalN > 0 {
			sr = float64(succ.(int64)) / float64(totalN)
		}
		return &port.AgentSkill{
			AgentID:     agentID,
			Role:        neoStr(rec, "role"),
			TotalTasks:  int(totalN),
			SuccessRate: sr,
			AvgCostUSD:  cost.(float64) / float64(max(1, totalN)),
		}, nil
	})
	if err != nil || res == nil {
		return nil, err
	}
	return res.(*port.AgentSkill), nil
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func collectTaskNodes(ctx context.Context, r neo4j.ResultWithContext) ([]port.TaskNode, error) {
	var nodes []port.TaskNode
	for r.Next(ctx) {
		rec := r.Record()
		nodes = append(nodes, port.TaskNode{
			TaskID:  neoStr(rec, "id"),
			Title:   neoStr(rec, "title"),
			Status:  neoStr(rec, "status"),
			Phase:   neoStr(rec, "phase"),
			AgentID: neoStr(rec, "agentID"),
			Role:    neoStr(rec, "role"),
		})
	}
	return nodes, r.Err()
}

func neoStr(rec *neo4j.Record, key string) string {
	v, ok := rec.Get(key)
	if !ok || v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}
