# Helm Upgraded LSP

An enhanced Helm Language Server Protocol (LSP) built on top of the official [`helm-ls`](https://github.com/mrjosh/helm-ls) architecture, adding Kubernetes-aware intelligence and advanced hover features.

## ✨ Features

### Unique (not in original helm-ls)

| Feature | Description |
|---------|-------------|
| **K8s Schema Hover** | Hover over any K8s YAML field (`spec`, `containers`, `replicas`) to see official Kubernetes API documentation |
| **K8s JSON Schema Validation** | Validates rendered templates against K8s schemas (fetched & cached from GitHub) |
| **Variable Hover** | Hover over `$host` in `range` loops → shows map keys; hover over `$ing` → shows nested values |
| **Keyword Hover** | Hover over `if` / `range` / `with` / `end` / `not` / `and` / `or` → syntax reference |
| **Template-aware YAML Formatting** | Formats YAML while preserving `{{ }}` template blocks |
| **Template apiVersion Resolution** | Automatically resolves `{{ include "helpers.capabilities..." }}` to the correct apiVersion using kind inference |

### Inherited from helm-ls

- `.Values.` autocompletion & hover with values from `values.yaml`
- `.Chart.` / `.Release.` built-in object hover
- Go-to-definition for `.Values` references
- `include` / `define` template navigation & hover
- Variable definitions & references
- Symbol outline
- `yamlls` proxy for YAML validation
- Tree-sitter based AST parsing
- Helm lint diagnostics
- Sprig function documentation

## Installation

### Build from source

```bash
git clone https://github.com/acidsugarx/helm-upgraded-lsp.git
cd helm-upgraded-lsp
go build -o helm-lsp ./cmd/helm-lsp
```

### Neovim Setup

```lua
-- Detect helm filetype
vim.api.nvim_create_autocmd({ 'BufRead', 'BufNewFile' }, {
  pattern = { '*/templates/*.yaml', '*/templates/*.tpl', '*/templates/**/*.yaml' },
  callback = function() vim.bo.filetype = 'helm' end,
})

-- Register LSP
local lspconfig = require('lspconfig')
local configs = require('lspconfig.configs')

configs.helm_upgraded = {
  default_config = {
    cmd = { '/path/to/helm-lsp', 'serve' },
    filetypes = { 'helm' },
    root_dir = lspconfig.util.root_pattern('Chart.yaml'),
  },
}
lspconfig.helm_upgraded.setup({})
```

## K8s Schema Support

Schemas are automatically fetched from [yannh/kubernetes-json-schema](https://github.com/yannh/kubernetes-json-schema) and cached locally in `/tmp/helm-lsp-schemas/`.

### Supported Resources

All standard Kubernetes resources are supported. When `apiVersion` is a template expression (e.g. `{{ include "helpers.capabilities.deployment.apiVersion" $ }}`), the LSP automatically infers the correct version from the resource `kind`.

## Configuration

The LSP uses the same configuration format as `helm-ls`. See the [helm-ls documentation](https://github.com/mrjosh/helm-ls#configuration) for details.

## Acknowledgements

Built on top of [`helm-ls`](https://github.com/mrjosh/helm-ls) by **Alireza Josheghani (mrjosh)** and contributors. Original code distributed under the MIT License (see [LICENSE](LICENSE)).

## License

MIT
