---
description: Code quality and testing guidelines for Helm LSP development
---

# Code Quality and Testing Guidelines

As an AI assistant working on the Helm LSP project, follow these rules strictly:

1. **Clean Architecture**:
   - Separate LSP protocol handling from the parser and core Helm/K8s logic.
   - Use interfaces for external dependencies (e.g., file system, Helm engine) to enable easy testing.
   - Keep `cmd/helm-lsp` minimal. Put business logic into `pkg/` or `internal/`.
2. **Testing**:
   - Write unit tests for all core logic (e.g., merging values, parsing functions, AST traversal).
   - Use standard `go test` and embrace table-driven tests (`[]struct{name string...}`).
   - Ensure handlers and parsing logic have mock tests where real disk access isn't required.
3. **Error Handling**:
   - Use `fmt.Errorf("...: %w", err)` to wrap errors and provide context.
   - Never use `_` to suppress errors silently unless explicitly documented why.
   - Recover from panics at the top LSP handler level so a single bad request doesn't crash the entire server.
4. **Code Style**:
   - Follow standard Go formatting rules. Run `gofmt` (or let the LSP/editor do it).
   - Add doc comments (`// FunctionName does X...`) for exported structs, interfaces, and functions.
   - Keep variable names concise but descriptive (e.g. `doc` instead of `d`, but `err` is fine).
