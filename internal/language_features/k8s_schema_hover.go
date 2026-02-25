package languagefeatures

import (
	"fmt"
	"strings"

	"github.com/acidsugarx/helm-lsp/internal/tree-sitter/gotemplate"
	lsp "go.lsp.dev/protocol"
)

type K8sSchemaHoverFeature struct {
	*GenericDocumentUseCase
	textDocumentPosition *lsp.TextDocumentPositionParams
}

func NewK8sSchemaHoverFeature(genericDocumentUseCase *GenericDocumentUseCase, textDocumentPosition *lsp.TextDocumentPositionParams) *K8sSchemaHoverFeature {
	return &K8sSchemaHoverFeature{
		GenericDocumentUseCase: genericDocumentUseCase,
		textDocumentPosition:   textDocumentPosition,
	}
}

func (f *K8sSchemaHoverFeature) AppropriateForNode() bool {
	// K8s Schema Hover applies to raw YAML text nodes in the template
	return f.NodeType == gotemplate.NodeTypeText
}

func (f *K8sSchemaHoverFeature) Hover() (string, error) {
	docContent := string(f.Document.Content)
	lineIdx := int(f.textDocumentPosition.Position.Line)
	lines := strings.Split(docContent, "\n")

	if lineIdx < 0 || lineIdx >= len(lines) {
		return "", fmt.Errorf("invalid line index")
	}

	wordRange := f.Node
	if wordRange == nil {
		return "", fmt.Errorf("no AST node at position")
	}

	path := DetectYAMLPath(lines, lineIdx)
	if len(path) == 0 {
		return "", nil // Not a K8s field match
	}

	apiVersion, kind := FindK8sRoot(lines, lineIdx)
	if apiVersion != "" && kind != "" {
		desc, err := GlobalSchemaManager.GetFieldDescription(apiVersion, kind, path)
		if err == nil && desc != "" {
			markdown := fmt.Sprintf("### Kubernetes Field: `%s`\n**%s/%s**\n\n%s", strings.Join(path, "."), apiVersion, kind, desc)
			return markdown, nil
		}
	}

	return "", nil
}
