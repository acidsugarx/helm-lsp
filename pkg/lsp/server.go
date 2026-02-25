package lsp

import (
	"sync"
	"time"

	"github.com/acidsugarx/helm-lsp/pkg/engine"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/tliron/glsp/server"
)

type Server struct {
	Handler        *protocol.Handler
	Server         *server.Server
	Store          *DocumentStore
	debounceMu     sync.Mutex
	debounceTimers map[string]*time.Timer
}

func NewServer() *Server {
	s := &Server{
		Store:          NewDocumentStore(),
		debounceTimers: make(map[string]*time.Timer),
	}

	handler := &protocol.Handler{
		Initialize:              s.initialize,
		Initialized:             s.initialized,
		Shutdown:                s.shutdown,
		SetTrace:                s.setTrace,
		TextDocumentDidOpen:     s.textDocumentDidOpen,
		TextDocumentDidChange:   s.textDocumentDidChange,
		TextDocumentDidClose:    s.textDocumentDidClose,
		TextDocumentDidSave:     s.textDocumentDidSave,
		TextDocumentCompletion:  s.textDocumentCompletion,
		TextDocumentDefinition:  s.textDocumentDefinition,
		TextDocumentHover:       s.textDocumentHover,
		TextDocumentFormatting:  s.textDocumentFormatting,
		TextDocumentCodeAction:  s.textDocumentCodeAction,
		WorkspaceExecuteCommand: s.executeCommand,
	}
	s.Handler = handler

	// Create the glsp server instance
	s.Server = server.NewServer(handler, "helm-lsp", false)

	return s
}

func (s *Server) Run() error {
	// Run Stdio blocks until the server exits
	return s.Server.RunStdio()
}

func (s *Server) initialize(context *glsp.Context, params *protocol.InitializeParams) (any, error) {
	capabilities := s.Handler.CreateServerCapabilities()

	// Text document synchronization (full is easier for now)
	capabilities.TextDocumentSync = protocol.TextDocumentSyncKindFull

	// Completion support
	capabilities.CompletionProvider = &protocol.CompletionOptions{
		TriggerCharacters: []string{".", "{{", " "},
	}

	// Register handlers capabilities
	capabilities.DefinitionProvider = true
	capabilities.HoverProvider = true
	capabilities.DocumentFormattingProvider = true
	capabilities.CodeActionProvider = &protocol.CodeActionOptions{
		CodeActionKinds: engine.CodeActionKinds,
	}

	// Custom commands
	capabilities.ExecuteCommandProvider = &protocol.ExecuteCommandOptions{
		Commands: []string{"helm.renderPreview", "helm.renderFullPreview"},
	}

	version := "0.1.0"
	return protocol.InitializeResult{
		Capabilities: capabilities,
		ServerInfo: &protocol.InitializeResultServerInfo{
			Name:    "helm-lsp",
			Version: &version,
		},
	}, nil
}

func (s *Server) initialized(context *glsp.Context, params *protocol.InitializedParams) error {
	return nil
}

func (s *Server) shutdown(context *glsp.Context) error {
	protocol.SetTraceValue(protocol.TraceValueOff)
	return nil
}

func (s *Server) setTrace(context *glsp.Context, params *protocol.SetTraceParams) error {
	protocol.SetTraceValue(params.Value)
	return nil
}

func (s *Server) textDocumentDidOpen(context *glsp.Context, params *protocol.DidOpenTextDocumentParams) error {
	s.Store.Set(params.TextDocument.URI, params.TextDocument.Text)
	s.scheduleValidation(context, params.TextDocument.URI, params.TextDocument.Text)
	return nil
}

func (s *Server) textDocumentDidChange(context *glsp.Context, params *protocol.DidChangeTextDocumentParams) error {
	var latestContent string
	for _, change := range params.ContentChanges {
		switch c := change.(type) {
		case protocol.TextDocumentContentChangeEvent:
			s.Store.Set(params.TextDocument.URI, c.Text)
			latestContent = c.Text
		case protocol.TextDocumentContentChangeEventWhole:
			s.Store.Set(params.TextDocument.URI, c.Text)
			latestContent = c.Text
		}
	}

	if latestContent != "" {
		s.scheduleValidation(context, params.TextDocument.URI, latestContent)
	}
	return nil
}

// scheduleValidation debounces validation calls. Resets the timer on every keystroke.
// Validation only fires 400ms after the user stops typing.
func (s *Server) scheduleValidation(context *glsp.Context, uri string, content string) {
	s.debounceMu.Lock()
	defer s.debounceMu.Unlock()

	if timer, exists := s.debounceTimers[uri]; exists {
		timer.Stop()
	}

	s.debounceTimers[uri] = time.AfterFunc(400*time.Millisecond, func() {
		s.validateDocument(context, uri, content)
	})
}

func (s *Server) textDocumentDidClose(context *glsp.Context, params *protocol.DidCloseTextDocumentParams) error {
	s.Store.Delete(params.TextDocument.URI)
	return nil
}

func (s *Server) textDocumentDidSave(context *glsp.Context, params *protocol.DidSaveTextDocumentParams) error {
	if content, ok := s.Store.Get(params.TextDocument.URI); ok {
		go s.validateDocument(context, params.TextDocument.URI, content)
	}
	return nil
}

func (s *Server) textDocumentCompletion(context *glsp.Context, params *protocol.CompletionParams) (any, error) {
	// A dummy item to test completion
	kind := protocol.CompletionItemKindVariable
	detail := "Helm Values"

	return []protocol.CompletionItem{
		{
			Label:  "Values",
			Kind:   &kind,
			Detail: &detail,
		},
	}, nil
}
