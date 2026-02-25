package templatehandler

import (
	"context"

	"github.com/acidsugarx/helm-lsp/internal/adapter/yamlls"
	"github.com/acidsugarx/helm-lsp/internal/util"
)

func (h *TemplateHandler) Configure(ctx context.Context, helmlsConfig util.HelmlsConfiguration) {
	h.configureYamlls(ctx, helmlsConfig.YamllsConfiguration)
	h.helmlintConfig = helmlsConfig.HelmLintConfig
}

func (h *TemplateHandler) configureYamlls(ctx context.Context, config util.YamllsConfiguration) {
	if config.Enabled {
		h.setYamllsConnector(yamlls.NewConnector(ctx, config, h.client, h.documents, &yamlls.DefaultCustomHandler))
		err := h.yamllsConnector.CallInitialize(ctx, h.chartStore.RootURI)
		if err != nil {
			logger.Error("Error initializing yamlls", err)
		}

		h.yamllsConnector.InitiallySyncOpenTemplateDocuments(h.documents.GetAllTemplateDocs())
	}
}
