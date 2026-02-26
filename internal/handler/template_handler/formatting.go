package templatehandler

import (
	"context"
	"strings"

	languagefeatures "github.com/acidsugarx/helm-lsp/internal/language_features"
	lsp "go.lsp.dev/protocol"
)

func (h *TemplateHandler) Formatting(ctx context.Context, params *lsp.DocumentFormattingParams) (result []lsp.TextEdit, err error) {
	doc, ok := h.documents.GetTemplateDoc(params.TextDocument.URI)
	if !ok {
		return nil, nil
	}

	content := string(doc.Content)
	formattedContent := languagefeatures.FormatHelmYAML(content, h.formatterConfig.Enabled)
	formattedContent = languagefeatures.TrimTrailingWhitespace(formattedContent)
	formattedContent = languagefeatures.EnsureNewlineAtEnd(formattedContent)

	if content == formattedContent {
		return nil, nil
	}

	lines := strings.Split(content, "\n")
	lastLine := uint32(len(lines) - 1)
	lastChar := uint32(len(lines[lastLine]))

	return []lsp.TextEdit{
		{
			Range: lsp.Range{
				Start: lsp.Position{Line: 0, Character: 0},
				End:   lsp.Position{Line: lastLine, Character: lastChar},
			},
			NewText: formattedContent,
		},
	}, nil
}
