package templatehandler

import (
	"context"
	"testing"

	"github.com/acidsugarx/helm-lsp/internal/charts"
	"github.com/acidsugarx/helm-lsp/internal/lsp/document"
	"github.com/acidsugarx/helm-lsp/internal/util"
	"github.com/stretchr/testify/assert"
)

func TestConfigureYamllsDisabled(t *testing.T) {
	ctx := context.Background()
	h := &TemplateHandler{}
	h.Configure(ctx, util.HelmlsConfiguration{
		YamllsConfiguration: util.YamllsConfiguration{Enabled: false},
	})

	assert.Nil(t, h.yamllsConnector)
}

func TestConfigureYamllsEnabled(t *testing.T) {
	ctx := context.Background()
	addChartCallback := func(chart *charts.Chart) {}
	chartStore := charts.NewChartStore(rootUri, charts.NewChart, addChartCallback)
	documents := document.NewDocumentStore()
	h := &TemplateHandler{
		chartStore: chartStore,
		documents:  documents,
	}
	h.Configure(ctx, util.HelmlsConfiguration{
		YamllsConfiguration: util.YamllsConfiguration{Enabled: true},
	})

	assert.NotNil(t, h.yamllsConnector)
}
