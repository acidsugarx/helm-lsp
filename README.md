# Helm Upgraded LSP

An enhanced Helm Language Server Protocol (LSP) built on top of the official [`helm-ls`](https://github.com/mrjosh/helm-ls) architecture, adding Kubernetes-aware intelligence, advanced hover features, code actions, and live chart preview.

## ✨ Features

### Unique (not in original helm-ls)

| Feature | Description |
|---------|-------------|
| **K8s Schema Hover** | Hover over any K8s YAML field (`spec`, `containers`, `replicas`) to see official Kubernetes API documentation |
| **K8s JSON Schema Validation** | Validates rendered templates against K8s schemas (fetched & cached from GitHub) |
| **Variable Hover** | Hover over `$host` in `range` loops → shows map keys; hover over `$ing` → shows nested values |
| **Keyword Hover** | Hover over `if` / `range` / `with` / `end` / `not` / `and` / `or` → syntax reference |
| **Template-aware YAML Formatting** | Formats YAML while preserving `{{ }}` template blocks (**BETA** - see below) |
| **Template apiVersion Resolution** | Automatically resolves `{{ include "helpers.capabilities..." }}` to the correct apiVersion using kind inference |
| **Code Actions (AST)** | Tree-sitter AST-based code actions: Extract to `values.yaml`, wrap with `\| quote`, `indent` → `nindent` |
| **Helm Render Preview** | Renders the current template (or full chart) and opens result in a split buffer |
| **Multi-Values Support** | Coalesces `values.yaml` + all `values*.yaml` files when rendering and linting |
| **Real-time Helm Lint** | Virtual in-memory render on every keystroke — shows Helm template errors as diagnostics |

### Code Actions

Intelligent code actions triggered on the current cursor position:

| Action | Trigger | Description |
|--------|---------|-------------|
| **Extract to Values** | On a `key: literal-value` YAML line | Moves the value to `values.yaml` and replaces it with a `{{ .Values.key }}` reference |
| **Add `\| quote`** | On a `{{ .Values.xxx }}` expression | Wraps the expression with `\| quote` for YAML string safety |
| **`indent` → `nindent`** | On a `\| indent N` pipeline | Converts to `\| nindent N` (adds leading newline) |
| **`toYaml` + `nindent`** | On a `\| toYaml` pipeline | Appends `\| nindent N` to the pipeline |

### Helm Render Preview

Two LSP commands available via `workspace/executeCommand`:

| Command | Description |
|---------|-------------|
| `helm.renderPreview` | Renders only the **current file** and shows YAML in a split buffer |
| `helm.renderFullPreview` | Renders the **entire chart** and shows all manifests in a split buffer |

If the chart fails to render (e.g. missing values, template syntax error), the error text is shown in the split buffer instead of YAML — making it easy to diagnose what went wrong without leaving Neovim.

Example Neovim keybindings:
```lua
vim.keymap.set('n', '<leader>hr', function()
  local uri = vim.uri_from_bufnr(0)
  vim.lsp.buf.execute_command({ command = 'helm.renderPreview', arguments = { uri } })
end, { desc = 'Helm Render Preview' })

vim.keymap.set('n', '<leader>hR', function()
  local uri = vim.uri_from_bufnr(0)
  vim.lsp.buf.execute_command({ command = 'helm.renderFullPreview', arguments = { uri } })
end, { desc = 'Helm Render Full Chart Preview' })
```

### Experimental YAML Formatter (Beta)

The original `helm-ls` explicitly disabled YAML formatting for templates because standard tools break `nindent/indent` usage. We have implemented an **experimental heuristic formatter** that intelligently adjusts YAML block indentations without breaking your `{{ }}` Go templates.

Disabled by default. **To enable it**:
```lua
require('lspconfig').helm_ls.setup {
  settings = {
    ['helm-ls'] = {
      yamlFormatter = {
        enabled = true
      }
    }
  }
}
```

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

### Custom Resource Definitions (CRDs)

1. **Auto-parsing from chart**: Place CRD YAML files in the `crds/` directory. The LSP will discover them and generate a local JSON schema.
2. **Global Custom Schemas**: Place custom JSON schema files in `~/.config/helm-lsp/schemas/`. These take priority over downloaded schemas.

## Configuration

The LSP uses the same configuration format as `helm-ls`. See the [helm-ls documentation](https://github.com/mrjosh/helm-ls#configuration) for details.

## Acknowledgements

Built on top of [`helm-ls`](https://github.com/mrjosh/helm-ls) by **Alireza Josheghani (mrjosh)** and contributors. Original code distributed under the MIT License (see [LICENSE](LICENSE)).

## License

MIT
