# Helm Upgraded LSP

An enhanced Helm Language Server Protocol (LSP) providing blazing fast virtual rendering, Kubernetes schema validations, and advanced autocompletion for Helm templates.

## Acknowledgements

This project was built using the solid foundational structural architecture of the official [`helm-ls`](https://github.com/mrjosh/helm-ls), originally created by **Alireza Josheghani (mrjosh)** and contributors.

We adopted the core language server handlers (using `go.lsp.dev/protocol`), Yaml language server integration, and `tree-sitter` bindings developed by `helm-ls`, integrating them with our custom real-time rendering engine, K8s validator, and code action snippet generators to provide a hybrid, ultimate IDE experience for Helm.

Original portions of the codebase are distributed under the MIT License by Alireza Josheghani and others (see `LICENSE`). The modified and enhanced codebase maintains the open-source MIT spirit.
