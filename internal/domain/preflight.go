package domain

// ToolRequirement defines a tool that must be present before pipeline execution.
type ToolRequirement struct {
	Name       string `json:"name"`        // human-readable: "Flutter SDK"
	Binary     string `json:"binary"`      // executable name: "flutter"
	CheckCmd   string `json:"check_cmd"`   // version check: "flutter --version"
	CheckArgs  []string `json:"check_args"` // args for check: ["--version"]
	InstallHint string `json:"install_hint"` // human-readable install instruction
	Required   bool   `json:"required"`    // if true, pipeline cannot proceed without it
}

// PreflightResult captures the outcome of a single tool check.
type PreflightResult struct {
	Tool     ToolRequirement `json:"tool"`
	Installed bool           `json:"installed"`
	Version   string         `json:"version"`  // parsed from check output
	Error     string         `json:"error"`    // why the check failed
}

// ToolsByStack maps tech stack identifiers to their required tools.
// Install commands are NOT here — they are install hints shown to the human.
// Agents NEVER run arbitrary install commands from LLM output.
var ToolsByStack = map[string][]ToolRequirement{
	"flutter": {
		{Name: "Flutter SDK", Binary: "flutter", CheckCmd: "flutter", CheckArgs: []string{"--version"}, InstallHint: "https://docs.flutter.dev/get-started/install", Required: true},
		{Name: "Dart SDK", Binary: "dart", CheckCmd: "dart", CheckArgs: []string{"--version"}, InstallHint: "Included with Flutter SDK", Required: true},
	},
	"go": {
		{Name: "Go", Binary: "go", CheckCmd: "go", CheckArgs: []string{"version"}, InstallHint: "https://go.dev/dl/", Required: true},
	},
	"node": {
		{Name: "Node.js", Binary: "node", CheckCmd: "node", CheckArgs: []string{"--version"}, InstallHint: "https://nodejs.org/", Required: true},
		{Name: "npm", Binary: "npm", CheckCmd: "npm", CheckArgs: []string{"--version"}, InstallHint: "Included with Node.js", Required: true},
	},
	"python": {
		{Name: "Python", Binary: "python3", CheckCmd: "python3", CheckArgs: []string{"--version"}, InstallHint: "https://python.org/downloads/", Required: true},
	},
	"common": {
		{Name: "Git", Binary: "git", CheckCmd: "git", CheckArgs: []string{"--version"}, InstallHint: "https://git-scm.com/downloads", Required: true},
		{Name: "Docker", Binary: "docker", CheckCmd: "docker", CheckArgs: []string{"--version"}, InstallHint: "https://docs.docker.com/get-docker/", Required: false},
	},
}

// ToolsForProject returns all tools required for a given ProjectConfig.
// Always includes "common" tools (git, docker).
func ToolsForProject(cfg ProjectConfig) []ToolRequirement {
	var tools []ToolRequirement

	// Always check common tools.
	tools = append(tools, ToolsByStack["common"]...)

	// Frontend.
	if stack, ok := ToolsByStack[cfg.FrontendFramework]; ok {
		tools = append(tools, stack...)
	}

	// Backend.
	if stack, ok := ToolsByStack[cfg.BackendLanguage]; ok {
		tools = append(tools, stack...)
	}

	return tools
}
