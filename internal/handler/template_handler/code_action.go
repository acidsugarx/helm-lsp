package templatehandler

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	languagefeatures "github.com/acidsugarx/helm-lsp/internal/language_features"
	templateast "github.com/acidsugarx/helm-lsp/internal/lsp/template_ast"
	"github.com/acidsugarx/helm-lsp/internal/tree-sitter/gotemplate"
	sitter "github.com/smacker/go-tree-sitter"
	lsp "go.lsp.dev/protocol"
)

// CodeActionKinds supported by this handler
var CodeActionKinds = []lsp.CodeActionKind{
	lsp.QuickFix,
	lsp.RefactorExtract,
	lsp.Source,
}

var (
	yamlKVRegex      = regexp.MustCompile(`^(\s*(?:-\s+)?)([\w\-\.\/]+):\s+(.+)$`)
	yamlKeyOnlyRegex = regexp.MustCompile(`^(\s*)([\w\-\.\/]+):\s*$`)
)

func (h *TemplateHandler) CodeAction(ctx context.Context, params *lsp.CodeActionParams) (result []lsp.CodeAction, err error) {
	posParams := lsp.TextDocumentPositionParams{
		TextDocument: params.TextDocument,
		Position:     params.Range.Start,
	}
	genericDocumentUseCase, err := h.NewGenericDocumentUseCase(posParams, templateast.NodeAtPosition)
	if err != nil {
		return nil, nil // Gracefully fallback if no node found
	}

	doc, ok := h.documents.GetTemplateDoc(params.TextDocument.URI)
	if !ok {
		return nil, nil
	}

	content := string(doc.Content)
	return getCodeActions(content, params.Range, string(params.TextDocument.URI), genericDocumentUseCase), nil
}

func getCodeActions(content string, rng lsp.Range, uri string, usecase *languagefeatures.GenericDocumentUseCase) []lsp.CodeAction {
	var actions []lsp.CodeAction
	lines := strings.Split(content, "\n")
	lineIdx := int(rng.Start.Line)

	if lineIdx >= len(lines) {
		return actions
	}

	line := lines[lineIdx]
	trimmed := strings.TrimSpace(line)

	scope := detectScopeAST(usecase)

	actions = append(actions, extractToValuesActions(lines, line, trimmed, lineIdx, uri, scope, usecase)...)
	actions = append(actions, quoteWrapActions(line, lineIdx, uri, usecase)...)
	actions = append(actions, nindentActions(line, lineIdx, uri, usecase)...)
	actions = append(actions, toYamlNindentActions(line, lineIdx, uri, usecase)...)

	return actions
}

type TemplateScope struct {
	InRange     bool
	InWith      bool
	InDefine    bool
	Depth       int
	RootRef     string
	RangeKeyVar string
	RangeValVar string
	WithVar     string
}

func detectScopeAST(usecase *languagefeatures.GenericDocumentUseCase) TemplateScope {
	scope := TemplateScope{RootRef: "."}
	if usecase == nil || usecase.Node == nil {
		return scope
	}

	var actionNode *sitter.Node
	curr := usecase.Node
	for curr != nil {
		if curr.Type() == gotemplate.NodeTypeRangeAction ||
			curr.Type() == gotemplate.NodeTypeWithAction ||
			curr.Type() == gotemplate.NodeTypeDefineAction {
			actionNode = curr
			break
		}
		curr = curr.Parent()
	}

	if actionNode == nil {
		return scope
	}

	if actionNode.Type() == gotemplate.NodeTypeRangeAction {
		scope.InRange = true
		scope.RootRef = "$"

		for i := 0; i < int(actionNode.ChildCount()); i++ {
			child := actionNode.Child(i)
			if child.Type() == gotemplate.NodeTypeRangeVariableDefinition {
				var vars []string
				for j := 0; j < int(child.ChildCount()); j++ {
					vChild := child.Child(j)
					if vChild.Type() == gotemplate.NodeTypeVariable {
						vars = append(vars, vChild.Content([]byte(usecase.Document.Content)))
					}
				}
				if len(vars) >= 2 {
					scope.RangeKeyVar = vars[0]
					scope.RangeValVar = vars[1]
				} else if len(vars) == 1 {
					scope.RangeValVar = vars[0]
				}
				break
			}
		}
	} else if actionNode.Type() == gotemplate.NodeTypeWithAction {
		scope.InWith = true
		scope.RootRef = "$"
	} else if actionNode.Type() == gotemplate.NodeTypeDefineAction {
		scope.InDefine = true
		scope.RootRef = "$"
	}

	return scope
}

func countIndent(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

func DetectYAMLPath(lines []string, lineIdx int) []string {
	if lineIdx >= len(lines) {
		return nil
	}
	currentIndent := countIndent(lines[lineIdx])
	var path []string
	targetIndent := currentIndent

	for i := lineIdx - 1; i >= 0; i-- {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == "---" || strings.HasPrefix(trimmed, "{{") || strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
			continue
		}
		lineIndent := countIndent(line)
		if lineIndent < targetIndent {
			m := yamlKeyOnlyRegex.FindStringSubmatch(line)
			if m != nil {
				path = append([]string{m[2]}, path...)
				targetIndent = lineIndent
			}
			if lineIndent == 0 {
				break
			}
		}
	}
	return path
}

func sanitizeKeyForValues(key string) string {
	if !strings.ContainsAny(key, "./-") {
		return key
	}
	normalized := strings.NewReplacer("/", ".", "-", ".").Replace(key)
	parts := strings.Split(normalized, ".")
	prefixes := map[string]bool{"kubernetes": true, "io": true, "cert": true, "manager": true, "k8s": true, "nginx": true, "app": true}
	startIdx := 0
	for startIdx < len(parts)-1 && prefixes[parts[startIdx]] {
		startIdx++
	}
	if startIdx >= len(parts) {
		startIdx = len(parts) - 1
	}
	result := parts[startIdx]
	for i := startIdx + 1; i < len(parts); i++ {
		p := parts[i]
		if len(p) > 0 {
			result += strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return result
}

func quoteDefault(value string) string {
	isNumeric := regexp.MustCompile(`^\d+(\.\d+)?$`).MatchString(value)
	if isNumeric {
		return value
	}
	lower := strings.ToLower(value)
	if lower == "true" || lower == "false" {
		return value
	}
	if (strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"")) || (strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")) {
		return value
	}
	return fmt.Sprintf("%q", value)
}

func findSiblingName(lines []string, lineIdx int) string {
	if lineIdx >= len(lines) {
		return ""
	}
	startIdx, endIdx, listItemIndent := lineIdx, lineIdx, -1
	for i := lineIdx; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "-") {
			startIdx = i
			listItemIndent = countIndent(lines[i])
			break
		}
	}
	if listItemIndent == -1 {
		return ""
	}
	for i := startIdx + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if countIndent(lines[i]) <= listItemIndent {
			endIdx = i
			break
		}
		endIdx = i
	}
	if endIdx == startIdx && endIdx < len(lines)-1 {
		endIdx = len(lines)
	}
	nameRegex := regexp.MustCompile(`^\s*-?\s*name:\s+(.+)$`)
	for i := startIdx; i < endIdx && i < len(lines); i++ {
		if m := nameRegex.FindStringSubmatch(lines[i]); m != nil {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}

func envNameToValuesKey(name string) string {
	if strings.HasPrefix(strings.TrimSpace(name), "{{") {
		m := regexp.MustCompile(`default\s+"([^"]+)"`).FindStringSubmatch(name)
		if len(m) > 1 {
			name = m[1]
		} else {
			m = regexp.MustCompile(`\.Values\.([a-zA-Z0-9_]+)`).FindStringSubmatch(name)
			if len(m) > 1 {
				name = m[1]
			} else {
				name = regexp.MustCompile(`[\{\}\|"'\s]`).ReplaceAllString(name, "")
			}
		}
	}
	name = strings.Trim(name, `"'`)
	normalized := strings.NewReplacer("_", " ", "-", " ").Replace(name)
	words := strings.Fields(normalized)
	if len(words) == 0 {
		return name
	}
	result := strings.ToLower(words[0])
	for _, w := range words[1:] {
		if len(w) > 0 {
			result += strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
		}
	}
	return result
}

func containsPath(path []string, target string) bool {
	for _, p := range path {
		if p == target {
			return true
		}
	}
	return false
}

func findContainerName(lines []string, lineIdx int) string {
	if lineIdx >= len(lines) {
		return ""
	}
	currentIndent := countIndent(lines[lineIdx])
	nameWithDashRegex := regexp.MustCompile(`^\s*-\s+name:\s+(.+)$`)
	lastSeenName := ""
	lastSeenIndent := 999
	for i := lineIdx - 1; i >= 0 && i >= lineIdx-50; i-- {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		lineIndent := countIndent(line)
		if strings.TrimSpace(line) == "containers:" {
			return lastSeenName
		}
		if lineIndent < currentIndent && nameWithDashRegex.MatchString(line) {
			if lineIndent < lastSeenIndent {
				lastSeenName = strings.TrimSpace(nameWithDashRegex.FindStringSubmatch(line)[1])
				lastSeenIndent = lineIndent
			}
		}
	}
	return ""
}

func makeEdit(uri string, lineIdx int, oldLineLen int, newText string) *lsp.WorkspaceEdit {
	return &lsp.WorkspaceEdit{
		Changes: map[lsp.DocumentURI][]lsp.TextEdit{
			lsp.DocumentURI(uri): {
				{
					Range: lsp.Range{
						Start: lsp.Position{Line: uint32(lineIdx), Character: 0},
						End:   lsp.Position{Line: uint32(lineIdx), Character: uint32(oldLineLen)},
					},
					NewText: newText,
				},
			},
		},
	}
}

func extractToValuesActions(lines []string, line, trimmed string, lineIdx int, uri string, scope TemplateScope, usecase *languagefeatures.GenericDocumentUseCase) []lsp.CodeAction {
	var actions []lsp.CodeAction
	if strings.Contains(trimmed, "{{") || strings.Contains(trimmed, "}}") {
		return nil
	}
	matches := yamlKVRegex.FindStringSubmatch(line)
	if len(matches) != 4 {
		return nil
	}
	indent, key, value := matches[1], matches[2], strings.TrimSpace(matches[3])
	if strings.HasSuffix(value, ":") || strings.HasPrefix(value, "#") || value == "" {
		return nil
	}
	skipFields := map[string]bool{"apiVersion": true, "kind": true, "metadata": true, "spec": true, "status": true, "template": true, "data": true, "type": true, "name": true}
	if skipFields[key] {
		return nil
	}

	parentPath := DetectYAMLPath(lines, lineIdx)
	sanitizedKey := sanitizeKeyForValues(key)
	if key == "value" {
		if siblingName := findSiblingName(lines, lineIdx); siblingName != "" {
			sanitizedKey = envNameToValuesKey(siblingName)
		}
	}
	containerPrefix := ""
	if containsPath(parentPath, "containers") {
		if cName := findContainerName(lines, lineIdx); cName != "" {
			containerPrefix = envNameToValuesKey(cName)
		}
	}

	structuralParents := map[string]bool{"metadata": true, "spec": true, "template": true, "containers": true, "selector": true, "matchLabels": true, "data": true, "env": true, "ports": true}
	var cleanPath []string
	if containerPrefix != "" {
		cleanPath = append(cleanPath, containerPrefix)
	}
	for _, p := range parentPath {
		if !structuralParents[p] {
			cleanPath = append(cleanPath, p)
		}
	}
	cleanPath = append(cleanPath, sanitizedKey)
	valuesPath := strings.Join(cleanPath, ".")

	valuesRef := fmt.Sprintf(".Values.%s", valuesPath)
	if scope.RootRef == "$" {
		valuesRef = fmt.Sprintf("$.Values.%s", valuesPath)
	}
	newLine := fmt.Sprintf("%s%s: {{ %s | default %s }}", indent, key, valuesRef, quoteDefault(value))

	kind := lsp.RefactorExtract
	actions = append(actions, lsp.CodeAction{
		Title: fmt.Sprintf("Extract → %s (global, same for all)", valuesRef),
		Kind:  kind,
		Edit:  makeEdit(uri, lineIdx, len(line), newLine),
	})

	if scope.InRange && scope.RangeValVar != "" {
		perKeyRef := fmt.Sprintf("%s.%s", scope.RangeValVar, sanitizedKey)
		perKeyLine := fmt.Sprintf("%s%s: {{ %s }}", indent, key, perKeyRef)
		actions = append(actions, lsp.CodeAction{
			Title: fmt.Sprintf("Extract → %s (per-item from loop)", perKeyRef),
			Kind:  kind,
			Edit:  makeEdit(uri, lineIdx, len(line), perKeyLine),
		})
	}

	if scope.InWith && !scope.InRange {
		withRef := fmt.Sprintf(".%s", sanitizedKey)
		withLine := fmt.Sprintf("%s%s: {{ %s }}", indent, key, withRef)
		actions = append(actions, lsp.CodeAction{
			Title: fmt.Sprintf("Extract → %s (from with context)", withRef),
			Kind:  kind,
			Edit:  makeEdit(uri, lineIdx, len(line), withLine),
		})
	}
	return actions
}

func findAncestor(node *sitter.Node, nodeType string) *sitter.Node {
	for node != nil {
		if node.Type() == nodeType {
			return node
		}
		node = node.Parent()
	}
	return nil
}

func quoteWrapActions(line string, lineIdx int, uri string, usecase *languagefeatures.GenericDocumentUseCase) []lsp.CodeAction {
	var actions []lsp.CodeAction
	if usecase == nil || usecase.Node == nil {
		return actions
	}

	templateNode := findAncestor(usecase.Node, gotemplate.NodeTypeTemplate)
	if templateNode == nil {
		return actions
	}

	content := templateNode.Content([]byte(usecase.Document.Content))
	if strings.Contains(content, "| quote") || strings.Contains(content, "| squote") || strings.Contains(content, "| toYaml") {
		return actions
	}

	matches := regexp.MustCompile(`(\s*-?\}\})$`).FindStringSubmatchIndex(content)
	if matches == nil {
		return actions
	}

	insertPos := matches[2]
	newText := content[:insertPos] + " | quote" + content[insertPos:]
	rng := templateast.GetLspRangeForNode(templateNode)

	kind := lsp.QuickFix
	actions = append(actions, lsp.CodeAction{
		Title: "Add | quote to template expression",
		Kind:  kind,
		Edit: &lsp.WorkspaceEdit{
			Changes: map[lsp.DocumentURI][]lsp.TextEdit{
				lsp.DocumentURI(uri): {
					{
						Range:   rng,
						NewText: newText,
					},
				},
			},
		},
	})
	return actions
}

func nindentActions(line string, lineIdx int, uri string, usecase *languagefeatures.GenericDocumentUseCase) []lsp.CodeAction {
	var actions []lsp.CodeAction
	if usecase == nil || usecase.Node == nil {
		return actions
	}

	funcCall := findAncestor(usecase.Node, gotemplate.NodeTypeFunctionCall)
	if funcCall == nil {
		return actions
	}

	content := funcCall.Content([]byte(usecase.Document.Content))
	if !strings.HasPrefix(strings.TrimSpace(content), "indent ") {
		return actions
	}

	newText := strings.Replace(content, "indent", "nindent", 1)
	rng := templateast.GetLspRangeForNode(funcCall)

	kind := lsp.QuickFix
	actions = append(actions, lsp.CodeAction{
		Title: "Use nindent instead of indent",
		Kind:  kind,
		Edit: &lsp.WorkspaceEdit{
			Changes: map[lsp.DocumentURI][]lsp.TextEdit{
				lsp.DocumentURI(uri): {
					{
						Range:   rng,
						NewText: newText,
					},
				},
			},
		},
	})
	return actions
}

func toYamlNindentActions(line string, lineIdx int, uri string, usecase *languagefeatures.GenericDocumentUseCase) []lsp.CodeAction {
	var actions []lsp.CodeAction
	if usecase == nil || usecase.Node == nil {
		return actions
	}

	funcCall := findAncestor(usecase.Node, gotemplate.NodeTypeFunctionCall)
	if funcCall == nil {
		return actions
	}

	content := funcCall.Content([]byte(usecase.Document.Content))
	if !strings.HasPrefix(strings.TrimSpace(content), "toYaml ") {
		return actions
	}

	templateNode := findAncestor(usecase.Node, gotemplate.NodeTypeTemplate)
	if templateNode == nil {
		return actions
	}

	templateContent := templateNode.Content([]byte(usecase.Document.Content))
	if strings.Contains(templateContent, "| nindent") || strings.Contains(templateContent, "| indent") {
		return actions
	}

	matches := regexp.MustCompile(`(\s*-?\}\})$`).FindStringSubmatchIndex(templateContent)
	if matches == nil {
		return actions
	}

	indentLevel := countIndent(line)
	insertPos := matches[2]
	newText := templateContent[:insertPos] + fmt.Sprintf(" | nindent %d", indentLevel) + templateContent[insertPos:]
	rng := templateast.GetLspRangeForNode(templateNode)

	kind := lsp.QuickFix
	actions = append(actions, lsp.CodeAction{
		Title: fmt.Sprintf("Add | nindent %d to toYaml", indentLevel),
		Kind:  kind,
		Edit: &lsp.WorkspaceEdit{
			Changes: map[lsp.DocumentURI][]lsp.TextEdit{
				lsp.DocumentURI(uri): {
					{
						Range:   rng,
						NewText: newText,
					},
				},
			},
		},
	})
	return actions
}
