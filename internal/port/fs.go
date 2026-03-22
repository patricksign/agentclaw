package port

// FileWriter abstracts file system write operations.
// Implementations must validate that all paths stay within the project WorkDir.
type FileWriter interface {
	// WriteFile writes content to the given path (relative to WorkDir).
	// Creates parent directories as needed.
	WriteFile(relativePath, content string) error

	// MkdirAll creates a directory tree (relative to WorkDir).
	MkdirAll(relativePath string) error
}
