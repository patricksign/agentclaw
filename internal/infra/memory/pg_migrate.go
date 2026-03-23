package memory

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

const bootstrapSQL = `CREATE TABLE IF NOT EXISTS schema_migrations (
    version    TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
)`

// MigratePostgres scans the embedded migrations/ folder, sorts by filename,
// skips already-applied migrations, and runs new ones in order.
// Each migration runs in its own transaction for atomicity.
func MigratePostgres(ctx context.Context, pool *pgxpool.Pool) error {
	// 1. Ensure schema_migrations table exists.
	if _, err := pool.Exec(ctx, bootstrapSQL); err != nil {
		return fmt.Errorf("pg migrate: create schema_migrations: %w", err)
	}

	// 2. Read all .sql files from embedded folder.
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("pg migrate: read migrations dir: %w", err)
	}

	// 3. Collect and sort migration files.
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		files = append(files, e.Name())
	}
	sort.Strings(files) // 001_xxx.sql < 002_xxx.sql < ...

	if len(files) == 0 {
		slog.Info("pg migrate: no migration files found")
		return nil
	}

	// 4. Get already-applied versions.
	applied, err := getAppliedVersions(ctx, pool)
	if err != nil {
		return fmt.Errorf("pg migrate: get applied versions: %w", err)
	}

	// 5. Run pending migrations in order.
	var ran int
	for _, filename := range files {
		version := strings.TrimSuffix(filename, ".sql")
		if applied[version] {
			continue
		}

		if err := runMigration(ctx, pool, filename, version); err != nil {
			return fmt.Errorf("pg migrate: %s: %w", filename, err)
		}
		ran++
	}

	if ran > 0 {
		slog.Info("pg migration completed", "applied", ran, "total", len(files))
	} else {
		slog.Info("pg migration: schema up to date", "total", len(files))
	}
	return nil
}

// runMigration executes a single migration file inside a transaction,
// then records it in schema_migrations.
func runMigration(ctx context.Context, pool *pgxpool.Pool, filename, version string) error {
	content, err := migrationsFS.ReadFile("migrations/" + filename)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	// Execute each statement individually.
	stmts := splitStatements(string(content))
	for i, stmt := range stmts {
		if _, err = tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("statement %d: %w", i+1, err)
		}
	}

	// Record migration as applied.
	if _, err = tx.Exec(ctx,
		`INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT (version) DO NOTHING`,
		version,
	); err != nil {
		return fmt.Errorf("record version: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	slog.Info("pg migration applied", "version", version, "statements", len(stmts))
	return nil
}

// getAppliedVersions returns a set of already-applied migration versions.
func getAppliedVersions(ctx context.Context, pool *pgxpool.Pool) (map[string]bool, error) {
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

// splitStatements splits a SQL script into individual statements by semicolon.
// Skips empty statements and comment-only blocks.
func splitStatements(sql string) []string {
	raw := strings.Split(sql, ";")
	stmts := make([]string, 0, len(raw))
	for _, s := range raw {
		trimmed := strings.TrimSpace(s)
		if trimmed == "" {
			continue
		}
		lines := strings.Split(trimmed, "\n")
		hasCode := false
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "--") {
				hasCode = true
				break
			}
		}
		if hasCode {
			stmts = append(stmts, trimmed)
		}
	}
	return stmts
}

// MigrationStatus returns which migrations are applied and which are pending.
func MigrationStatus(ctx context.Context, pool *pgxpool.Pool) (applied []string, pending []string, err error) {
	// Ensure table exists for fresh DBs.
	if _, err = pool.Exec(ctx, bootstrapSQL); err != nil {
		return nil, nil, err
	}

	appliedMap, err := getAppliedVersions(ctx, pool)
	if err != nil {
		return nil, nil, err
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, nil, err
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, f := range files {
		version := strings.TrimSuffix(f, ".sql")
		if appliedMap[version] {
			applied = append(applied, version)
		} else {
			pending = append(pending, version)
		}
	}
	return applied, pending, nil
}
