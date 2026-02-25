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

// Pre-compiled regexes for code actions
var (
	yamlKVRegex      = regexp.MustCompile(`^(\s*)([\w\-]+):\s+(.+)$`)
	quoteRegex       = regexp.MustCompile(`(\{\{-?\s*(?:\$\.|\.)[^\}]+?)(\s*-?\}\})`)
	indentPipeRegex  = regexp.MustCompile(`(\{\{-?\s*(?:include|template)\s+"[^"]+"\s+[^\}]+)\|\s*indent\s+(\d+)\s*(-?\}\})`)
	toYamlRegex      = regexp.MustCompile(`(\{\{-?\s*(?:include|template)\s+"[^"]+"\s+[^\}]+)\|\s*toYaml\s*(-?\}\})`)
	rangeBlockRegex  = regexp.MustCompile(`\{\{-?\s*range\b`)
	withBlockRegex   = regexp.MustCompile(`\{\{-?\s*with\b`)
	endBlockRegex    = regexp.MustCompile(`\{\{-?\s*end\s*-?\}\}`)
	defineBlockRegex = regexp.MustCompile(`\{\{-?\s*define\b`)
)

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

	// Detect template scope context (range, with, define)
	scope := detectScope(lines, lineIdx)

	// 1. Extract hardcoded value to Values (scope-aware)
	actions = append(actions, extractToValuesActions(line, trimmed, lineIdx, uri, scope)...)

	// 2. Quote/toYaml helpers
	actions = append(actions, quoteWrapActions(line, trimmed, lineIdx, uri)...)

	// 3. indent → nindent conversion
	actions = append(actions, nindentActions(line, trimmed, lineIdx, uri)...)

	// 4. Add toYaml | nindent to map/list values
	actions = append(actions, toYamlActions(line, trimmed, lineIdx, uri)...)

	// 5. Wrap with `with` block
	actions = append(actions, wrapWithActions(line, trimmed, lineIdx, uri, scope)...)

	return actions
}

// TemplateScope describes the enclosing template scope context for a given line.
type TemplateScope struct {
	InRange  bool   // inside a {{- range ... }} block
	InWith   bool   // inside a {{- with ... }} block
	InDefine bool   // inside a {{- define ... }} block
	Depth    int    // nesting depth (range count + with count)
	RootRef  string // "$" if inside range/with, "." if at top level
}

// detectScope scans upward from lineIdx to determine the enclosing template scope.
func detectScope(lines []string, lineIdx int) TemplateScope {
	scope := TemplateScope{RootRef: "."}
	depth := 0

	for i := lineIdx - 1; i >= 0; i-- {
		line := lines[i]

		// Count end blocks (they close a scope above us)
		if endBlockRegex.MatchString(line) {
			depth++
			continue
		}

		// Range/with/define open a scope
		if rangeBlockRegex.MatchString(line) {
			if depth > 0 {
				depth--
			} else {
				scope.InRange = true
				scope.Depth++
				scope.RootRef = "$"
			}
			continue
		}
		if withBlockRegex.MatchString(line) {
			if depth > 0 {
				depth--
			} else {
				scope.InWith = true
				scope.Depth++
				scope.RootRef = "$"
			}
			continue
		}
		if defineBlockRegex.MatchString(line) {
			if depth > 0 {
				depth--
			} else {
				scope.InDefine = true
				scope.RootRef = "$"
			}
			continue
		}
	}

	return scope
}

// extractToValuesActions suggests extracting a hardcoded value to values.yaml.
// It's scope-aware: inside range/with blocks it uses $.Values instead of .Values.
func extractToValuesActions(line, trimmed string, lineIdx int, uri string, scope TemplateScope) []protocol.CodeAction {
	var actions []protocol.CodeAction

	// Don't suggest on lines that already use {{ }}
	if strings.Contains(trimmed, "{{") || strings.Contains(trimmed, "}}") {
		return nil
	}

	matches := yamlKVRegex.FindStringSubmatch(line)
	if len(matches) != 4 {
		return nil
	}

	indent := matches[1]
	key := matches[2]
	value := strings.TrimSpace(matches[3])

	// Skip nested maps, comments, and structural fields
	if strings.HasSuffix(value, ":") || strings.HasPrefix(value, "#") {
		return nil
	}
	skipFields := map[string]bool{
		"apiVersion": true, "kind": true, "metadata": true, "spec": true,
		"status": true, "template": true, "data": true, "type": true,
	}
	if skipFields[key] {
		return nil
	}

	// Use $ prefix when inside range/with/define blocks
	valuesRef := fmt.Sprintf("%s.Values.%s", scope.RootRef, key)

	newLine := fmt.Sprintf("%s%s: {{ %s | default %s }}", indent, key, valuesRef, value)

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

	title := fmt.Sprintf("Extract '%s: %s' → %s", key, value, valuesRef)
	if scope.InRange {
		title += " (inside range)"
	} else if scope.InWith {
		title += " (inside with)"
	}

	actions = append(actions, protocol.CodeAction{
		Title: title,
		Kind:  &kind,
		Edit: &protocol.WorkspaceEdit{
			DocumentChanges: []interface{}{editChange},
		},
	})

	return actions
}

// quoteWrapActions suggests adding | quote to template expressions.
// Also suggests | squote for single-quote variant.
func quoteWrapActions(line, trimmed string, lineIdx int, uri string) []protocol.CodeAction {
	var actions []protocol.CodeAction

	matches := quoteRegex.FindStringSubmatchIndex(line)
	if matches == nil {
		return nil
	}

	expr := line[matches[2]:matches[3]]
	// Skip if already has quote/squote/toYaml
	if strings.Contains(expr, "| quote") || strings.Contains(expr, "| squote") || strings.Contains(expr, "| toYaml") {
		return nil
	}

	kind := protocol.CodeActionKindQuickFix
	insertPos := matches[4] // start of closing }}

	// Suggest | quote
	quoteText := line[:insertPos] + " | quote" + line[insertPos:]
	actions = append(actions, protocol.CodeAction{
		Title: "Add | quote to template expression",
		Kind:  &kind,
		Edit: &protocol.WorkspaceEdit{
			DocumentChanges: []interface{}{
				protocol.TextDocumentEdit{
					TextDocument: protocol.OptionalVersionedTextDocumentIdentifier{
						TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri},
					},
					Edits: []interface{}{
						protocol.TextEdit{
							Range: protocol.Range{
								Start: protocol.Position{Line: uint32(lineIdx), Character: 0},
								End:   protocol.Position{Line: uint32(lineIdx), Character: uint32(len(line))},
							},
							NewText: quoteText,
						},
					},
				},
			},
		},
	})

	return actions
}

// nindentActions suggests converting `| indent N` to `| nindent N` in include/template calls.
func nindentActions(line, trimmed string, lineIdx int, uri string) []protocol.CodeAction {
	var actions []protocol.CodeAction

	matches := indentPipeRegex.FindStringSubmatchIndex(line)
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
			DocumentChanges: []interface{}{
				protocol.TextDocumentEdit{
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
				},
			},
		},
	})

	return actions
}

// toYamlActions suggests adding `| toYaml | nindent N` to expressions that pass
// map/list values to include/template without toYaml.
func toYamlActions(line, trimmed string, lineIdx int, uri string) []protocol.CodeAction {
	var actions []protocol.CodeAction

	// Look for toYaml without nindent: `{{ toYaml .Values.x }}` → `{{ toYaml .Values.x | nindent N }}`
	toYamlNoIndent := regexp.MustCompile(`(\{\{-?\s*toYaml\s+[^\}|]+?)(\s*-?\}\})`)
	matches := toYamlNoIndent.FindStringSubmatchIndex(line)
	if matches == nil {
		return nil
	}

	expr := line[matches[2]:matches[3]]
	if strings.Contains(expr, "| nindent") || strings.Contains(expr, "| indent") {
		return nil
	}

	// Calculate current indentation level
	indentLevel := len(line) - len(strings.TrimLeft(line, " "))

	kind := protocol.CodeActionKindQuickFix
	insertPos := matches[4]
	newText := line[:insertPos] + fmt.Sprintf(" | nindent %d", indentLevel) + line[insertPos:]

	actions = append(actions, protocol.CodeAction{
		Title: fmt.Sprintf("Add | nindent %d to toYaml expression", indentLevel),
		Kind:  &kind,
		Edit: &protocol.WorkspaceEdit{
			DocumentChanges: []interface{}{
				protocol.TextDocumentEdit{
					TextDocument: protocol.OptionalVersionedTextDocumentIdentifier{
						TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri},
					},
					Edits: []interface{}{
						protocol.TextEdit{
							Range: protocol.Range{
								Start: protocol.Position{Line: uint32(lineIdx), Character: 0},
								End:   protocol.Position{Line: uint32(lineIdx), Character: uint32(len(line))},
							},
							NewText: newText,
						},
					},
				},
			},
		},
	})

	return actions
}

// wrapWithActions suggests wrapping a `.Values.x` expression with a `with` block
// for cleaner nested access.
func wrapWithActions(line, trimmed string, lineIdx int, uri string, scope TemplateScope) []protocol.CodeAction {
	var actions []protocol.CodeAction

	// Look for repeated deep access like .Values.something.nested.deep
	// Suggest wrapping with {{- with .Values.something.nested }}
	deepAccessRegex := regexp.MustCompile(`(?:\.Values|` + regexp.QuoteMeta(scope.RootRef) + `\.Values)((?:\.\w+){3,})`)
	matches := deepAccessRegex.FindStringSubmatch(trimmed)
	if matches == nil {
		return nil
	}

	fullPath := matches[1] // e.g. ".something.nested.deep"
	parts := strings.Split(strings.TrimPrefix(fullPath, "."), ".")
	if len(parts) < 3 {
		return nil
	}

	// Suggest wrapping with the first N-1 parts
	withPath := scope.RootRef + ".Values." + strings.Join(parts[:len(parts)-1], ".")
	leafField := parts[len(parts)-1]

	kind := protocol.CodeActionKindRefactorExtract
	actions = append(actions, protocol.CodeAction{
		Title: fmt.Sprintf("Wrap with '{{- with %s }}' (access .%s directly)", withPath, leafField),
		Kind:  &kind,
		// No automatic edit — this is a hint, user applies manually
		// because it requires wrapping multiple lines with end block
	})

	return actions
}
