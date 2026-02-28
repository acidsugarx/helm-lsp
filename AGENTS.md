# AGENTS.md

Production-ready agent instructions for `github.com/acidsugarx/helm-lsp`.
This project is a Go-based Helm language server. Agents must prioritize correctness, safety, clean code, and test-verified outcomes.

## Instruction Priority
Apply instructions in this strict order:
1. Active system/developer/user instructions.
2. This `AGENTS.md`.
3. Repository docs (`README.md`, code comments, tests).

Rules files audit:
- `.cursor/rules/`: not found
- `.cursorrules`: not found
- `.github/copilot-instructions.md`: not found
- `.agent/workflows/clean-code-and-tests.md`: present and incorporated

## Non-Negotiable Operating Principles
- Always run tests for the changed scope before finishing.
- Prefer minimal, reversible, well-scoped changes.
- Keep behavior deterministic and observable.
- Do not silently ignore errors.
- Do not edit generated code manually.
- If validation cannot be executed, explicitly state why and provide exact reproduction commands.

## Repository Topology
- `cmd/helm-lsp`: entrypoint only; keep thin.
- `internal/`: LSP handlers, document model, chart management, yaml-language-server adapter, JSON schema logic.
- `pkg/engine`: reusable Helm/YAML helpers.
- `mocks/`: generated mocks (`mockery`), no manual edits.
- `testdata/`: fixtures used by tests.

## Environment Baseline
- Module: `github.com/acidsugarx/helm-lsp`
- Go version: `1.25.1` (see `go.mod`)
- Binary: `helm-lsp`

## Required Workflow Per Change
Follow this sequence for every implementation task:
1. Read relevant files and understand existing patterns.
2. Implement minimal code changes in the correct layer.
3. Add or update tests for behavior changes.
4. Run formatting and targeted tests.
5. Run broader checks when risk is medium/high.
6. Report exactly what was run and what passed/failed.

## Build / Run Commands
```bash
go build -o helm-lsp ./cmd/helm-lsp
go build ./...
go run ./cmd/helm-lsp
```

## Lint / Format / Quality Commands
No Makefile/Taskfile/golangci config was found. Use standard Go tooling:

```bash
gofmt -w .
go vet ./...
go test ./...
```

Fast local verification:

```bash
go test ./pkg/engine ./internal/util
```

## Test Commands (Single Test Emphasis)
Run all tests:

```bash
go test ./...
```

Run package tests:

```bash
go test ./internal/util
go test ./pkg/engine
```

Run one test function:

```bash
go test ./pkg/engine -run '^TestResolveValuesPath$'
```

Run one subtest:

```bash
go test ./pkg/engine -run 'TestResolveValuesPath/variable_assignment'
```

Run one test with verbose output:

```bash
go test -v ./internal/handler/template_handler -run '^TestCompletionMain$'
```

Run integration tests:

```bash
go test -tags=integration ./internal/adapter/yamlls/...
```

Integration prerequisites:
- `yaml-language-server` on PATH
- optional env override: `YAMLLS_PATH` (string or JSON array)

Known test caveat:
- Some fixture trees under `testdata/` may be absent in partial checkouts.
- If `go test ./...` fails from missing fixtures, run affected packages and report missing paths exactly.

## Definition of Done (DoD)
A change is done only when all apply:
- Code compiles for touched packages.
- Changed behavior is covered by tests (new or existing).
- Relevant tests pass locally.
- Formatting is clean (`gofmt`).
- Error handling is explicit and contextual.
- Final report lists commands executed and outcomes.

## Code Style and Design Standards (Go)

### Architecture and Boundaries
- Keep transport/protocol logic separate from business logic.
- Keep `cmd/` wiring-only; put real logic in `internal/` and `pkg/`.
- Use constructor-based dependency injection.
- Use interfaces at external seams (filesystem, subprocess, remote clients).

### Imports
- Let `gofmt` organize imports.
- Keep standard library imports separated from third-party/internal imports.
- Alias only when needed for clarity/collisions (for example `lsp`).

### Formatting and Function Shape
- `gofmt` is authoritative.
- Keep functions focused and reasonably short.
- Prefer helper extraction over deep nesting.

### Types and APIs
- Prefer concrete types internally.
- Introduce interfaces for boundaries and tests, not by default everywhere.
- Keep exported API surface minimal.
- Add doc comments for exported symbols.
- Prefer typed structs to `map[string]any` unless payload is inherently dynamic.

### Naming
- Idiomatic Go naming: `CamelCase` exported, `camelCase` internal.
- Use short, intention-revealing names (`doc`, `chart`, `cfg`, `err`).
- Package names should be short and lowercase.
- Test names should describe behavior; subtests should describe scenarios.

### Error Handling
- Return errors; do not swallow failures.
- Wrap propagated errors with context using `%w`.
- Keep error strings lowercase and concise.
- Ignore errors only with explicit justification.
- Recover from panic only at safe boundaries and log context.

### Logging
- Use project logging patterns.
- Include request/path/chart context in logs.
- Keep high-volume logs at debug level.

### Concurrency and Context
- Thread `context.Context` through request-scoped work.
- Protect shared mutable state (`sync.Map`, mutexes, channels).
- For async tests, use time-bounded eventual assertions (`assert.Eventually`).

### Testing Style
- Use table-driven tests (`[]struct{...}` + `t.Run`) where practical.
- Use `testify/assert` consistently.
- Use `t.Parallel()` only when tests are isolated.
- Unit tests should be deterministic and offline.
- Use `//go:build integration` for external-process tests.

### Generated Code
- Do not hand-edit files in `mocks/`.
- Preserve generated headers (`DO NOT EDIT`).

## Safety and Change Discipline
- Do not perform destructive operations without explicit user instruction.
- Do not refactor unrelated areas while implementing scoped changes.
- Preserve existing public behavior unless change is requested.
- If you find unrelated defects, note them separately; do not bundle stealth fixes.

## Final Response Contract for Agents
When finishing work, always include:
1. What changed and why.
2. Files touched.
3. Commands run.
4. Test/build results.
5. Any blockers, risks, or follow-up actions.
