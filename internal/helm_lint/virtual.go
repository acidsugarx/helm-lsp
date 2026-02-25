package helmlint

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/acidsugarx/helm-lsp/internal/charts"
	"github.com/acidsugarx/helm-lsp/internal/lsp/document"
	lsp "go.lsp.dev/protocol"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
)

var (
	singleBraceVarRegex = regexp.MustCompile(`(?:^|[^{])\{-?\s+\$`)
	singleBraceDotRegex = regexp.MustCompile(`(?:^|[^{])\{-?\s+\.`)
	undefVarRegex       = regexp.MustCompile(`undefined variable "(\$.*?)"`)
)

func VirtualRenderDiagnostics(ch *charts.Chart, doc *document.TemplateDocument, vals chartutil.Values) []lsp.Diagnostic {
	if ch.HelmChart == nil {
		return nil
	}
	// Create an isolated copy of the chart struct to prevent concurrent map/slice mutation
	chObj := *ch.HelmChart
	templatesCopy := make([]*chart.File, len(ch.HelmChart.Templates))
	for i, t := range ch.HelmChart.Templates {
		tCopy := *t
		if strings.HasSuffix(doc.URI.Filename(), t.Name) {
			tCopy.Data = doc.Content
		}
		templatesCopy[i] = &tCopy
	}
	chObj.Templates = templatesCopy

	renderEngine := action.NewInstall(&action.Configuration{})
	renderEngine.ClientOnly = true
	renderEngine.DryRun = true
	renderEngine.ReleaseName = "lsp-preview"
	renderEngine.Namespace = "default"

	// Actually run the render
	_, err := renderEngine.Run(&chObj, vals)
	content := string(doc.Content)

	var diagnostics []lsp.Diagnostic

	// Inject our syntax checker
	diagnostics = append(diagnostics, checkBrokenBrackets(content)...)

	if err != nil {
		diags := parseHelmError(err.Error(), doc.URI.Filename(), content)
		diagnostics = append(diagnostics, diags...)
	}

	return diagnostics
}

func parseHelmError(errMsg string, uri string, content string) []lsp.Diagnostic {
	targetPath := uri
	fileName := ""
	parts := strings.Split(targetPath, "/")
	if len(parts) > 0 {
		fileName = parts[len(parts)-1]
	}

	if fileName == "" {
		return nil
	}

	errRegex := regexp.MustCompile(fmt.Sprintf(`\((.*?%s):(\d+)(?::(\d+))?\):\s*(.*)`, regexp.QuoteMeta(fileName)))
	matches := errRegex.FindStringSubmatch(errMsg)

	yamlRegex := regexp.MustCompile(fmt.Sprintf(`YAML parse error on .*?%s:\s*.*?[yY]aml:\s*line\s*(\d+):\s*(.*)`, regexp.QuoteMeta(fileName)))
	yamlMatches := yamlRegex.FindStringSubmatch(errMsg)

	if len(yamlMatches) > 0 {
		lineNum, _ := strconv.Atoi(yamlMatches[1])
		if lineNum < 1 {
			lineNum = 1
		}
		lineIdx := uint32(lineNum - 1)
		return []lsp.Diagnostic{{
			Range:    lsp.Range{Start: lsp.Position{Line: lineIdx}, End: lsp.Position{Line: lineIdx, Character: 500}},
			Severity: lsp.DiagnosticSeverityError,
			Source:   "helm-yaml",
			Message:  yamlMatches[2],
		}}
	} else if len(matches) > 0 {
		lineNum, _ := strconv.Atoi(matches[2])
		if lineNum < 1 {
			lineNum = 1
		}
		lineIdx := uint32(lineNum - 1)
		msg := matches[4]

		lines := strings.Split(content, "\n")
		if (strings.Contains(msg, "unclosed action") || strings.Contains(msg, "unexpected EOF")) && int(lineIdx) >= len(lines)-2 {
			openTags := 0
			for i := 0; i < len(lines); i++ {
				openTags += strings.Count(lines[i], "{{") - strings.Count(lines[i], "}}")
				if openTags > 0 {
					lineIdx = uint32(i)
					break
				}
			}
		}

		var diagnostics []lsp.Diagnostic
		if strings.Contains(msg, "undefined variable") {
			if m := undefVarRegex.FindStringSubmatch(msg); len(m) > 1 {
				varName := m[1]
				for i, line := range lines {
					if strings.Contains(line, varName) && strings.Contains(line, ":=") && !strings.Contains(line, "{{") {
						diagnostics = append(diagnostics, lsp.Diagnostic{
							Range:    lsp.Range{Start: lsp.Position{Line: uint32(i)}, End: lsp.Position{Line: uint32(i), Character: 500}},
							Severity: lsp.DiagnosticSeverityWarning,
							Source:   "helm-lsp-hint",
							Message:  fmt.Sprintf("Possible typo: variable %s is assigned here but missing '{{' brackets", varName),
						})
					}
				}
			}
		}

		diagnostics = append(diagnostics, lsp.Diagnostic{
			Range:    lsp.Range{Start: lsp.Position{Line: lineIdx}, End: lsp.Position{Line: lineIdx, Character: 500}},
			Severity: lsp.DiagnosticSeverityError,
			Source:   "helm-template",
			Message:  msg,
		})
		return diagnostics
	}
	return nil
}

func checkBrokenBrackets(content string) []lsp.Diagnostic {
	var diagnostics []lsp.Diagnostic
	lines := strings.Split(content, "\n")
	templateKeywords := []string{"if", "else", "end", "range", "with", "define", "template", "include", "block"}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		for _, kw := range templateKeywords {
			patterns := []string{"{- " + kw, "{ " + kw}
			for _, pat := range patterns {
				idx := strings.Index(trimmed, pat)
				if idx >= 0 && (idx == 0 || trimmed[idx-1] != '{') {
					diagnostics = append(diagnostics, lsp.Diagnostic{
						Range:    lsp.Range{Start: lsp.Position{Line: uint32(i)}, End: lsp.Position{Line: uint32(i), Character: 500}},
						Severity: lsp.DiagnosticSeverityWarning,
						Source:   "helm-syntax",
						Message:  fmt.Sprintf("Possible broken template bracket: found '%s' — did you mean '{{- %s'?", pat, kw),
					})
				}
			}
		}

		if singleBraceVarRegex.MatchString(trimmed) && !strings.Contains(trimmed, "{{") {
			diagnostics = append(diagnostics, lsp.Diagnostic{
				Range:    lsp.Range{Start: lsp.Position{Line: uint32(i)}, End: lsp.Position{Line: uint32(i), Character: 500}},
				Severity: lsp.DiagnosticSeverityWarning,
				Source:   "helm-syntax",
				Message:  "Possible broken template bracket: variable assignment with single '{' — did you mean '{{'?",
			})
		}
		if singleBraceDotRegex.MatchString(trimmed) && (!strings.Contains(trimmed, "{{") || strings.Count(trimmed, "{")-strings.Count(trimmed, "{{") > 0) {
			diagnostics = append(diagnostics, lsp.Diagnostic{
				Range:    lsp.Range{Start: lsp.Position{Line: uint32(i)}, End: lsp.Position{Line: uint32(i), Character: 500}},
				Severity: lsp.DiagnosticSeverityWarning,
				Source:   "helm-syntax",
				Message:  "Possible broken template bracket: found '{ .expr' — did you mean '{{ .expr'?",
			})
		}
	}
	return diagnostics
}
