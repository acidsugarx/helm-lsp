package handler

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/acidsugarx/helm-lsp/internal/charts"
	templatehandler "github.com/acidsugarx/helm-lsp/internal/handler/template_handler"
	yamlhandler "github.com/acidsugarx/helm-lsp/internal/handler/yaml_handler"
	helmlint "github.com/acidsugarx/helm-lsp/internal/helm_lint"
	"github.com/acidsugarx/helm-lsp/internal/lsp/document"
	"github.com/acidsugarx/helm-lsp/internal/util"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	lsp "go.lsp.dev/protocol"
	"go.lsp.dev/uri"
	"go.uber.org/zap"
	"helm.sh/helm/v3/pkg/chartutil"

	"github.com/acidsugarx/helm-lsp/internal/log"
)

var logger = log.GetLogger()

type ServerHandler struct {
	client       protocol.Client
	connPool     jsonrpc2.Conn
	linterName   string
	documents    *document.DocumentStore
	chartStore   *charts.ChartStore
	helmlsConfig util.HelmlsConfiguration
	langHandlers map[document.DocumentType]LangHandler
}

func StartHandler(stream io.ReadWriteCloser) {
	logger, _ := zap.NewProduction()

	server := newHandler(nil, nil)
	_, conn, client := protocol.NewServer(context.Background(),
		server,
		jsonrpc2.NewStream(stream),
		logger,
	)
	server.connPool = conn
	server.setClient(client)

	<-conn.Done()
}

func newHandler(connPool jsonrpc2.Conn, client protocol.Client) *ServerHandler {
	documents := document.NewDocumentStore()
	handler := &ServerHandler{
		client:       client,
		linterName:   "helm-lint",
		connPool:     connPool,
		documents:    documents,
		helmlsConfig: util.DefaultConfig,
	}

	currentDir, err := os.Getwd()
	if err != nil {
		logger.Error("Error getting current directory", err)
	}
	chartStore := charts.NewChartStore(uri.File(currentDir), charts.NewChart, handler.AddChartCallback)
	handler.chartStore = chartStore
	handler.langHandlers = map[document.DocumentType]LangHandler{
		document.TemplateDocumentType: templatehandler.NewTemplateHandler(client, documents, chartStore, handler.helmlsConfig.HelmLintConfig),
		document.YamlDocumentType:     yamlhandler.NewYamlHandler(client, documents, chartStore),
	}

	logger.Printf("helm-ls: connections opened")
	return handler
}

func (h *ServerHandler) setClient(client protocol.Client) {
	h.client = client

	for _, handler := range h.langHandlers {
		handler.SetClient(client)
	}
}

// CodeAction implements protocol.Server.
func (h *ServerHandler) CodeAction(ctx context.Context, params *lsp.CodeActionParams) (result []lsp.CodeAction, err error) {
	logger.Debug("Running CodeAction with params", params)

	handler, err := h.selectLangHandler(ctx, params.TextDocument.URI)
	if err != nil {
		logger.Error("Error selecting lang handler for code action", err)
		return nil, err
	}
	return handler.CodeAction(ctx, params)
}

// CodeLens implements protocol.Server.
func (h *ServerHandler) CodeLens(ctx context.Context, params *lsp.CodeLensParams) (result []lsp.CodeLens, err error) {
	logger.Error("Code lens unimplemented")
	return nil, nil
}

// CodeLensRefresh implements protocol.Server.
func (h *ServerHandler) CodeLensRefresh(ctx context.Context) (err error) {
	logger.Error("Code lens refresh unimplemented")
	return nil
}

// CodeLensResolve implements protocol.Server.
func (h *ServerHandler) CodeLensResolve(ctx context.Context, params *lsp.CodeLens) (result *lsp.CodeLens, err error) {
	logger.Error("Code lens resolve unimplemented")
	return nil, nil
}

// ColorPresentation implements protocol.Server.
func (h *ServerHandler) ColorPresentation(ctx context.Context, params *lsp.ColorPresentationParams) (result []lsp.ColorPresentation, err error) {
	logger.Error("Color presentation unimplemented")
	return nil, nil
}

// CompletionResolve implements protocol.Server.
func (h *ServerHandler) CompletionResolve(ctx context.Context, params *lsp.CompletionItem) (result *lsp.CompletionItem, err error) {
	logger.Error("Completion resolve unimplemented")
	return nil, nil
}

// Declaration implements protocol.Server.
func (h *ServerHandler) Declaration(ctx context.Context, params *lsp.DeclarationParams) (result []lsp.Location, err error) {
	logger.Error("Declaration unimplemented")
	return nil, nil
}

// DidChangeWorkspaceFolders implements protocol.Server.
func (h *ServerHandler) DidChangeWorkspaceFolders(ctx context.Context, params *lsp.DidChangeWorkspaceFoldersParams) (err error) {
	logger.Error("DidChangeWorkspaceFolders unimplemented")
	return nil
}

// DocumentColor implements protocol.Server.
func (h *ServerHandler) DocumentColor(ctx context.Context, params *lsp.DocumentColorParams) (result []lsp.ColorInformation, err error) {
	logger.Error("Document color unimplemented")
	return nil, nil
}

// DocumentHighlight implements protocol.Server.
func (h *ServerHandler) DocumentHighlight(ctx context.Context, params *lsp.DocumentHighlightParams) (result []lsp.DocumentHighlight, err error) {
	logger.Error("Document highlight unimplemented")
	return nil, nil
}

// DocumentLink implements protocol.Server.
func (h *ServerHandler) DocumentLink(ctx context.Context, params *lsp.DocumentLinkParams) (result []lsp.DocumentLink, err error) {
	logger.Error("Document link unimplemented")
	return nil, nil
}

// DocumentLinkResolve implements protocol.Server.
func (h *ServerHandler) DocumentLinkResolve(ctx context.Context, params *lsp.DocumentLink) (result *lsp.DocumentLink, err error) {
	logger.Error("Document link resolve unimplemented")
	return nil, nil
}

// ExecuteCommand implements protocol.Server.
func (h *ServerHandler) ExecuteCommand(ctx context.Context, params *lsp.ExecuteCommandParams) (result interface{}, err error) {
	if params.Command == "helm.renderPreview" || params.Command == "helm.renderFullPreview" {
		if len(params.Arguments) == 0 {
			logger.Error("ExecuteCommand requires a document URI as argument")
			return nil, fmt.Errorf("ExecuteCommand requires a document URI as argument")
		}

		uriStr, ok := params.Arguments[0].(string)
		if !ok {
			return nil, fmt.Errorf("ExecuteCommand argument must be a string URI")
		}
		documentURI, err := uri.Parse(uriStr)
		if err != nil {
			return nil, err
		}

		doc, ok := h.documents.GetTemplateDoc(documentURI)
		if !ok {
			return nil, fmt.Errorf("document not found in store")
		}

		ch, err := h.chartStore.GetChartForDoc(documentURI)
		if err != nil {
			return nil, err
		}

		vals := ch.ValuesFiles.MainValuesFile.Values
		for _, additionalFile := range ch.ValuesFiles.AdditionalValuesFiles {
			vals = chartutil.CoalesceTables(additionalFile.Values, vals)
		}
		if ch.ValuesFiles.OverlayValuesFile != nil {
			vals = chartutil.CoalesceTables(ch.ValuesFiles.OverlayValuesFile.Values, vals)
		}

		manifest, err := helmlint.VirtualRenderString(ch, doc, vals)
		if err != nil {
			logger.Error("VirtualRenderString failed", err)
			return err.Error(), nil // Print the Helm template error directly into the split!
		}

		if params.Command == "helm.renderPreview" {
			single := extractSingleFilePreview(manifest, filepath.Base(documentURI.Filename()))
			return single, nil
		}
		return manifest, nil
	}
	logger.Error("Execute command unimplemented for " + params.Command)
	return nil, nil
}

func extractSingleFilePreview(manifest string, baseFilename string) string {
	parts := strings.Split(manifest, "---")
	for _, part := range parts {
		if strings.Contains(part, "# Source: ") && strings.Contains(part, baseFilename) {
			return strings.TrimSpace(part)
		}
	}
	return "No rendered output found for " + baseFilename
}

// Exit implements protocol.Server.
func (h *ServerHandler) Exit(ctx context.Context) (err error) {
	return nil
}

// FoldingRanges implements protocol.Server.
func (h *ServerHandler) FoldingRanges(ctx context.Context, params *lsp.FoldingRangeParams) (result []lsp.FoldingRange, err error) {
	logger.Error("Folding ranges unimplemented")
	return nil, nil
}

// Formatting implements protocol.Server.
func (h *ServerHandler) Formatting(ctx context.Context, params *lsp.DocumentFormattingParams) (result []lsp.TextEdit, err error) {
	logger.Debug("Running formatting with params", params)

	handler, err := h.selectLangHandler(ctx, params.TextDocument.URI)
	if err != nil {
		logger.Error("Error selecting lang handler for formatting", err)
		return nil, err
	}
	return handler.Formatting(ctx, params)
}

// Implementation implements protocol.Server.
func (h *ServerHandler) Implementation(ctx context.Context, params *lsp.ImplementationParams) (result []lsp.Location, err error) {
	logger.Error("Implementation unimplemented")
	return nil, nil
}

// IncomingCalls implements protocol.Server.
func (h *ServerHandler) IncomingCalls(ctx context.Context, params *lsp.CallHierarchyIncomingCallsParams) (result []lsp.CallHierarchyIncomingCall, err error) {
	logger.Error("Incoming calls unimplemented")
	return nil, nil
}

// LinkedEditingRange implements protocol.Server.
func (h *ServerHandler) LinkedEditingRange(ctx context.Context, params *lsp.LinkedEditingRangeParams) (result *lsp.LinkedEditingRanges, err error) {
	logger.Error("Linked editing range unimplemented")
	return nil, nil
}

// LogTrace implements protocol.Server.
func (h *ServerHandler) LogTrace(ctx context.Context, params *lsp.LogTraceParams) (err error) {
	logger.Error("Log trace unimplemented")
	return nil
}

// Moniker implements protocol.Server.
func (h *ServerHandler) Moniker(ctx context.Context, params *lsp.MonikerParams) (result []lsp.Moniker, err error) {
	logger.Error("Moniker unimplemented")
	return nil, nil
}

// OnTypeFormatting implements protocol.Server.
func (h *ServerHandler) OnTypeFormatting(ctx context.Context, params *lsp.DocumentOnTypeFormattingParams) (result []lsp.TextEdit, err error) {
	logger.Error("On type formatting unimplemented")
	return nil, nil
}

// OutgoingCalls implements protocol.Server.
func (h *ServerHandler) OutgoingCalls(ctx context.Context, params *lsp.CallHierarchyOutgoingCallsParams) (result []lsp.CallHierarchyOutgoingCall, err error) {
	logger.Error("Outgoing calls unimplemented")
	return nil, nil
}

// PrepareCallHierarchy implements protocol.Server.
func (h *ServerHandler) PrepareCallHierarchy(ctx context.Context, params *lsp.CallHierarchyPrepareParams) (result []lsp.CallHierarchyItem, err error) {
	logger.Error("Prepare call hierarchy unimplemented")
	return nil, nil
}

// PrepareRename implements protocol.Server.
func (h *ServerHandler) PrepareRename(ctx context.Context, params *lsp.PrepareRenameParams) (result *lsp.Range, err error) {
	logger.Error("Prepare rename unimplemented")
	return nil, nil
}

// RangeFormatting implements protocol.Server.
func (h *ServerHandler) RangeFormatting(ctx context.Context, params *lsp.DocumentRangeFormattingParams) (result []lsp.TextEdit, err error) {
	logger.Error("Range formatting unimplemented")
	return nil, nil
}

// Rename implements protocol.Server.
func (h *ServerHandler) Rename(ctx context.Context, params *lsp.RenameParams) (result *lsp.WorkspaceEdit, err error) {
	logger.Error("Rename unimplemented")
	return nil, nil
}

// Request implements protocol.Server.
func (h *ServerHandler) Request(ctx context.Context, method string, params interface{}) (result interface{}, err error) {
	logger.Error("Request unimplemented")
	return nil, nil
}

// SemanticTokensFull implements protocol.Server.
func (h *ServerHandler) SemanticTokensFull(ctx context.Context, params *lsp.SemanticTokensParams) (result *lsp.SemanticTokens, err error) {
	logger.Error("Semantic tokens full unimplemented")
	return nil, nil
}

// SemanticTokensFullDelta implements protocol.Server.
func (h *ServerHandler) SemanticTokensFullDelta(ctx context.Context, params *lsp.SemanticTokensDeltaParams) (result interface{}, err error) {
	logger.Error("Semantic tokens full delta unimplemented")
	return nil, nil
}

// SemanticTokensRange implements protocol.Server.
func (h *ServerHandler) SemanticTokensRange(ctx context.Context, params *lsp.SemanticTokensRangeParams) (result *lsp.SemanticTokens, err error) {
	logger.Error("Semantic tokens range unimplemented")
	return nil, nil
}

// SemanticTokensRefresh implements protocol.Server.
func (h *ServerHandler) SemanticTokensRefresh(ctx context.Context) (err error) {
	logger.Error("Semantic tokens refresh unimplemented")
	return nil
}

// SetTrace implements protocol.Server.
func (h *ServerHandler) SetTrace(ctx context.Context, params *lsp.SetTraceParams) (err error) {
	logger.Error("Set trace unimplemented")
	return nil
}

// ShowDocument implements protocol.Server.
func (h *ServerHandler) ShowDocument(ctx context.Context, params *lsp.ShowDocumentParams) (result *lsp.ShowDocumentResult, err error) {
	logger.Error("Show document unimplemented")
	return nil, nil
}

// Shutdown implements protocol.Server.
func (h *ServerHandler) Shutdown(ctx context.Context) (err error) {
	return h.connPool.Close()
}

// SignatureHelp implements protocol.Server.
func (h *ServerHandler) SignatureHelp(ctx context.Context, params *lsp.SignatureHelpParams) (result *lsp.SignatureHelp, err error) {
	logger.Error("Signature help unimplemented")
	return nil, nil
}

// Symbols implements protocol.Server.
func (h *ServerHandler) Symbols(ctx context.Context, params *lsp.WorkspaceSymbolParams) (result []lsp.SymbolInformation, err error) {
	logger.Error("Symbols unimplemented")
	return nil, nil
}

// TypeDefinition implements protocol.Server.
func (h *ServerHandler) TypeDefinition(ctx context.Context, params *lsp.TypeDefinitionParams) (result []lsp.Location, err error) {
	logger.Error("Type definition unimplemented")
	return nil, nil
}

// WillCreateFiles implements protocol.Server.
func (h *ServerHandler) WillCreateFiles(ctx context.Context, params *lsp.CreateFilesParams) (result *lsp.WorkspaceEdit, err error) {
	logger.Error("Will create files unimplemented")
	return nil, nil
}

// WillDeleteFiles implements protocol.Server.
func (h *ServerHandler) WillDeleteFiles(ctx context.Context, params *lsp.DeleteFilesParams) (result *lsp.WorkspaceEdit, err error) {
	logger.Error("Will delete files unimplemented")
	return nil, nil
}

// WillRenameFiles implements protocol.Server.
func (h *ServerHandler) WillRenameFiles(ctx context.Context, params *lsp.RenameFilesParams) (result *lsp.WorkspaceEdit, err error) {
	logger.Error("Will rename files unimplemented")
	return nil, nil
}

// WillSave implements protocol.Server.
func (h *ServerHandler) WillSave(ctx context.Context, params *lsp.WillSaveTextDocumentParams) (err error) {
	logger.Error("Will save unimplemented")
	return nil
}

// WillSaveWaitUntil implements protocol.Server.
func (h *ServerHandler) WillSaveWaitUntil(ctx context.Context, params *lsp.WillSaveTextDocumentParams) (result []lsp.TextEdit, err error) {
	logger.Error("Will save wait until unimplemented")
	return nil, nil
}

// WorkDoneProgressCancel implements protocol.Server.
func (h *ServerHandler) WorkDoneProgressCancel(ctx context.Context, params *lsp.WorkDoneProgressCancelParams) (err error) {
	logger.Error("Work done progress cancel unimplemented")
	return nil
}
