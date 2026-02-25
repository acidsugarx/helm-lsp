package engine

import (
	"fmt"
	"regexp"
	"strings"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// CodeActionKinds supported by this LSP
var CodeActionKinds = []protocol.CodeActionKind{
	protocol.CodeActionKindQuickFix,
	protocol.CodeActionKindRefactorExtract,
	protocol.CodeActionKindSource,
}

// GetCodeActions analyzes the document at the given range and returns applicable code actions.
func GetCodeActions(content string, rng protocol.Range, uri string) []protocol.CodeAction {
	var actions []protocol.CodeAction
	lines := strings.Split(content, "\n")
	lineIdx := int(rng.Start.Line)

	if lineIdx >= len(lines) {
		return actions
	}

	line := lines[lineIdx]
	trimmed := strings.TrimSpace(line)

	// 1. Extract hardcoded value to Values
	actions = append(actions, extractToValuesActions(line, trimmed, lineIdx, uri)...)

	// 2. Quote wrap: suggest adding quotes around unquoted values
	actions = append(actions, quoteWrapActions(line, trimmed, lineIdx)...)

	// 3. Add nindent: suggest adding nindent to include/template calls
	actions = append(actions, nindentActions(line, trimmed, lineIdx)...)

	return actions
}

// extractToValuesActions suggests extracting a hardcoded value to values.yaml
func extractToValuesActions(line, trimmed string, lineIdx int, uri string) []protocol.CodeAction {
	var actions []protocol.CodeAction

	// Match lines like `  replicas: 3` or `  image: nginx:latest`
	// but NOT lines that already use {{ }}
	if strings.Contains(trimmed, "{{") || strings.Contains(trimmed, "}}") {
		return nil
	}

	yamlKVRegex := regexp.MustCompile(`^(\s*)([\w\-]+):\s+(.+)$`)
	matches := yamlKVRegex.FindStringSubmatch(line)
	if len(matches) != 4 {
		return nil
	}

	indent := matches[1]
	key := matches[2]
	value := strings.TrimSpace(matches[3])

	// Skip if value is another YAML key (nested map) or template comment
	if strings.HasSuffix(value, ":") || strings.HasPrefix(value, "#") {
		return nil
	}

	// Skip common K8s structural fields that shouldn't be extracted
	skipFields := map[string]bool{
		"apiVersion": true, "kind": true, "metadata": true, "spec": true,
		"status": true, "template": true, "data": true,
	}
	if skipFields[key] {
		return nil
	}

	// Build the replacement: `replicas: 3` -> `replicas: {{ .Values.replicas | default 3 }}`
	newLine := fmt.Sprintf("%s%s: {{ .Values.%s | default %s }}", indent, key, key, value)

	kind := protocol.CodeActionKindRefactorExtract
	editChange := protocol.TextDocumentEdit{
		TextDocument: protocol.OptionalVersionedTextDocumentIdentifier{
			TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri},
		},
		Edits: []interface{}{
			protocol.TextEdit{
				Range: protocol.Range{
					Start: protocol.Position{Line: uint32(lineIdx), Character: 0},
					End:   protocol.Position{Line: uint32(lineIdx), Character: uint32(len(line))},
				},
				NewText: newLine,
			},
		},
	}

	actions = append(actions, protocol.CodeAction{
		Title: fmt.Sprintf("Extract '%s: %s' to .Values.%s", key, value, key),
		Kind:  &kind,
		Edit: &protocol.WorkspaceEdit{
			DocumentChanges: []interface{}{editChange},
		},
	})

	return actions
}

// quoteWrapActions suggests wrapping unquoted template expressions with quote
func quoteWrapActions(line, trimmed string, lineIdx int) []protocol.CodeAction {
	var actions []protocol.CodeAction

	// Look for: `key: {{ .Values.something }}`  (no quote function)
	// Suggest: `key: {{ .Values.something | quote }}`
	quoteRegex := regexp.MustCompile(`(\{\{-?\s*\.Values\.[^\}]+?)(\s*-?\}\})`)
	matches := quoteRegex.FindStringSubmatchIndex(line)
	if matches == nil {
		return nil
	}

	// Check it doesn't already have | quote
	expr := line[matches[2]:matches[3]]
	if strings.Contains(expr, "| quote") || strings.Contains(expr, "|quote") {
		return nil
	}

	kind := protocol.CodeActionKindQuickFix
	// Insert ` | quote` before the closing }}
	insertPos := matches[4] // start of closing }}
	newText := line[:insertPos] + " | quote" + line[insertPos:]

	actions = append(actions, protocol.CodeAction{
		Title:       "Add | quote to template expression",
		Kind:        &kind,
		Diagnostics: []protocol.Diagnostic{},
		Edit: &protocol.WorkspaceEdit{
			Changes: map[string][]protocol.TextEdit{
				"": {
					{
						Range: protocol.Range{
							Start: protocol.Position{Line: uint32(lineIdx), Character: 0},
							End:   protocol.Position{Line: uint32(lineIdx), Character: uint32(len(line))},
						},
						NewText: newText,
					},
				},
			},
		},
	})

	return actions
}

// nindentActions suggests adding nindent to include/template calls that use indent
func nindentActions(line, trimmed string, lineIdx int) []protocol.CodeAction {
	var actions []protocol.CodeAction

	// Look for: `{{ include "name" . | indent N }}` -> suggest `{{ include "name" . | nindent N }}`
	indentRegex := regexp.MustCompile(`(\{\{-?\s*(?:include|template)\s+"[^"]+"\s+[^\}]+)\|\s*indent\s+(\d+)\s*(-?\}\})`)
	matches := indentRegex.FindStringSubmatchIndex(line)
	if matches == nil {
		return nil
	}

	// Replace `indent` with `nindent`
	newLine := line[:matches[0]] +
		line[matches[2]:matches[3]] + "| nindent " + line[matches[4]:matches[5]] + " " + line[matches[6]:matches[7]] +
		line[matches[7]:]

	kind := protocol.CodeActionKindQuickFix
	actions = append(actions, protocol.CodeAction{
		Title: "Use nindent instead of indent (adds newline before content)",
		Kind:  &kind,
		Edit: &protocol.WorkspaceEdit{
			Changes: map[string][]protocol.TextEdit{
				"": {
					{
						Range: protocol.Range{
							Start: protocol.Position{Line: uint32(lineIdx), Character: 0},
							End:   protocol.Position{Line: uint32(lineIdx), Character: uint32(len(line))},
						},
						NewText: newLine,
					},
				},
			},
		},
	})

	return actions
}
