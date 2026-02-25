package templatehandler

import (
	"context"

	languagefeatures "github.com/acidsugarx/helm-lsp/internal/language_features"
	templateast "github.com/acidsugarx/helm-lsp/internal/lsp/template_ast"
	"github.com/acidsugarx/helm-lsp/internal/protocol"
	"github.com/acidsugarx/helm-lsp/internal/tree-sitter/gotemplate"

	lsp "go.lsp.dev/protocol"
)

func (h *TemplateHandler) Hover(ctx context.Context, params *lsp.HoverParams) (result *lsp.Hover, err error) {
	genericDocumentUseCase, err := h.NewGenericDocumentUseCase(params.TextDocumentPositionParams, templateast.NodeAtPosition)
	if err != nil {
		return nil, err
	}

	wordRange := templateast.GetLspRangeForNode(genericDocumentUseCase.Node)

	usecases := []languagefeatures.HoverUseCase{
		languagefeatures.NewKeywordFeature(genericDocumentUseCase, params.Position),
		languagefeatures.NewVariablesFeature(genericDocumentUseCase),
		languagefeatures.NewBuiltInObjectsFeature(genericDocumentUseCase), // has to be before template context
		languagefeatures.NewTemplateContextFeature(genericDocumentUseCase),
		languagefeatures.NewIncludesCallFeature(genericDocumentUseCase),
		languagefeatures.NewFunctionCallFeature(genericDocumentUseCase),
	}

	for _, usecase := range usecases {
		if usecase.AppropriateForNode() {
			result, err := usecase.Hover()
			return protocol.BuildHoverResponse(result, wordRange), err
		}
	}

	if genericDocumentUseCase.NodeType == gotemplate.NodeTypeText {
		// Try K8s Schema Hover first (our custom feature)
		k8sFeature := languagefeatures.NewK8sSchemaHoverFeature(genericDocumentUseCase, &params.TextDocumentPositionParams)
		if k8sFeature.AppropriateForNode() {
			k8sResult, k8sErr := k8sFeature.Hover()
			if k8sErr == nil && k8sResult != "" {
				return protocol.BuildHoverResponse(k8sResult, wordRange), nil
			}
		}

		// Fall back to yamlls hover
		word := genericDocumentUseCase.Document.WordAt(params.Position)
		response, err := h.yamllsConnector.CallHoverOrComplete(ctx, *params, word)
		return response, err
	}

	return nil, err
}
