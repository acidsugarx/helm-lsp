package languagefeatures

import (
	"fmt"

	helmdocs "github.com/acidsugarx/helm-lsp/internal/documentation/helm"
	"github.com/acidsugarx/helm-lsp/internal/tree-sitter/gotemplate"
	lsp "go.lsp.dev/protocol"
)

type KeywordFeature struct {
	*GenericDocumentUseCase
	position lsp.Position
	word     string
}

func NewKeywordFeature(genericDocumentUseCase *GenericDocumentUseCase, pos lsp.Position) *KeywordFeature {
	return &KeywordFeature{
		GenericDocumentUseCase: genericDocumentUseCase,
		position:               pos,
		word:                   genericDocumentUseCase.Document.WordAt(pos),
	}
}

func (f *KeywordFeature) AppropriateForNode() bool {
	if f.NodeType == gotemplate.NodeTypeText {
		return false
	}
	w := f.word
	return w == "if" || w == "range" || w == "with" || w == "end" || w == "define" || w == "template" || w == "not" || w == "and" || w == "or" || w == "default" || w == "empty"
}

func (f *KeywordFeature) Hover() (string, error) {
	doc, ok := helmdocs.GetFunctionByName(f.word)
	if ok {
		return fmt.Sprintf("%s\n\n%s", doc.Detail, doc.Doc), nil
	}

	switch f.word {
	case "if":
		return "if $condition\n\nConditionally execute a block", nil
	case "range":
		return "range $index, $element := $pipeline\n\nIterate over a collection", nil
	case "with":
		return "with $pipeline\n\nSet the current context (.) to the given value", nil
	case "end":
		return "end\n\nCloses a control structure (if, range, with, define)", nil
	}

	return "", fmt.Errorf("no docs for %s", f.word)
}
