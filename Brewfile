# Development toolchain for cuckoo.
# Install with: task setup, which also `go install`s nilaway (no formula).

brew "go"            # Go compiler and toolchain
brew "go-task"       # task runner (Taskfile.yml)
brew "golangci-lint" # the gate's linter, and the single definition of formatted
                     # this repo has: gofumpt and goimports run inside it
brew "bun"           # `bun fmt`, the same for Markdown, JSON and YAML: the gate's `task docs`
brew "govulncheck"   # the gate's known-vulnerability scan: `task vuln`
