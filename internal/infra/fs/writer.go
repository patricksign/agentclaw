package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/patricksign/AgentClaw/internal/port"
)

// Compile-time check: DiskWriter implements port.FileWriter.
var _ port.FileWriter = (*DiskWriter)(nil)

// DiskWriter writes files within a sandboxed root directory (WorkDir).
// All paths are validated to prevent traversal attacks.
type DiskWriter struct {
	rootDir string // absolute path to the project workspace
}

// NewDiskWriter creates a writer rooted at the given directory.
// The directory must already exist.
func NewDiskWriter(rootDir string) (*DiskWriter, error) {
	abs, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("fs: resolve root dir: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("fs: root dir %s: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("fs: %s is not a directory", abs)
	}
	return &DiskWriter{rootDir: abs}, nil
}

// WriteFile writes content to a file at relativePath within the root directory.
// Creates parent directories as needed. The path must be relative and must not
// escape the root directory (rejects "..", absolute paths, symlinks).
func (w *DiskWriter) WriteFile(relativePath, content string) error {
	fullPath, err := w.safePath(relativePath)
	if err != nil {
		return err
	}

	// Create parent directories.
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("fs: mkdir %s: %w", dir, err)
	}

	if err := os.WriteFile(fullPath, []byte(content), 0640); err != nil {
		return fmt.Errorf("fs: write %s: %w", fullPath, err)
	}
	return nil
}

// MkdirAll creates a directory tree at relativePath within the root directory.
func (w *DiskWriter) MkdirAll(relativePath string) error {
	fullPath, err := w.safePath(relativePath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(fullPath, 0750); err != nil {
		return fmt.Errorf("fs: mkdir %s: %w", fullPath, err)
	}
	return nil
}

// safePath validates and resolves a relative path within the root directory.
// Security checks:
//   - Rejects absolute paths
//   - Rejects ".." components (path traversal)
//   - Verifies resolved path stays under rootDir (symlink protection)
func (w *DiskWriter) safePath(relativePath string) (string, error) {
	if relativePath == "" {
		return "", fmt.Errorf("fs: empty path")
	}

	// Reject absolute paths.
	if filepath.IsAbs(relativePath) {
		return "", fmt.Errorf("fs: absolute path not allowed: %s", relativePath)
	}

	// Reject path traversal components.
	for _, part := range strings.Split(filepath.ToSlash(relativePath), "/") {
		if part == ".." {
			return "", fmt.Errorf("fs: path traversal not allowed: %s", relativePath)
		}
	}

	// Resolve to absolute path and verify it stays under rootDir.
	fullPath := filepath.Join(w.rootDir, relativePath)
	resolved, err := filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("fs: resolve path: %w", err)
	}

	// Ensure the resolved path is under rootDir (protects against symlink escapes).
	if !strings.HasPrefix(resolved, w.rootDir+string(filepath.Separator)) && resolved != w.rootDir {
		return "", fmt.Errorf("fs: path escapes root directory: %s", relativePath)
	}

	return resolved, nil
}
