package templatehandler

import (
	"github.com/acidsugarx/helm-lsp/internal/adapter/yamlls"
	"github.com/acidsugarx/helm-lsp/internal/charts"
	"github.com/acidsugarx/helm-lsp/internal/log"
	"github.com/acidsugarx/helm-lsp/internal/lsp/document"
	"github.com/acidsugarx/helm-lsp/internal/util"
	"go.lsp.dev/protocol"
)

var logger = log.GetLogger()

type TemplateHandler struct {
	client          protocol.Client
	documents       *document.DocumentStore
	chartStore      *charts.ChartStore
	yamllsConnector *yamlls.Connector
	helmlintConfig  util.HelmLintConfig
	formatterConfig util.YamlFormatterConfig
}

func NewTemplateHandler(client protocol.Client, documents *document.DocumentStore, chartStore *charts.ChartStore, helmlintConfig util.HelmLintConfig) *TemplateHandler {
	return &TemplateHandler{
		client:          client,
		documents:       documents,
		chartStore:      chartStore,
		yamllsConnector: &yamlls.Connector{},
		helmlintConfig:  helmlintConfig,
	}
}

func (h *TemplateHandler) SetClient(client protocol.Client) {
	h.client = client
}

func (h *TemplateHandler) setYamllsConnector(yamllsConnector *yamlls.Connector) {
	h.yamllsConnector = yamllsConnector
}
