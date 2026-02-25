package lsp

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/acidsugarx/helm-lsp/pkg/engine"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"gopkg.in/yaml.v3"
	"helm.sh/helm/v3/pkg/chartutil"
)

func (s *Server) textDocumentDefinition(context *glsp.Context, params *protocol.DefinitionParams) (interface{}, error) {
	uri := params.TextDocument.URI
	content, ok := s.Store.Get(uri)
	if !ok {
		return nil, nil // Document not in store
	}

	lines := strings.Split(content, "\n")
	lineIdx := params.Position.Line
	charIdx := params.Position.Character

	if int(lineIdx) >= len(lines) {
		return nil, nil
	}
	lineText := lines[lineIdx]

	// 1. Check if it's a template include first
	if tmplName, ok := engine.TemplateAtPosition(lineText, int(charIdx)); ok {
		filePath := uriToPath(uri)
		chartRoot := findChartRoot(filepath.Dir(filePath))
		if chartRoot != "" {
			ch, err := engine.ParseChart(chartRoot)
			if err == nil {
				tmplFile, pos, err := engine.FindTemplatePosition(ch, tmplName)
				if err == nil && pos != nil {
					absTmplPath := filepath.Join(chartRoot, tmplFile)
					return []protocol.Location{
						{
							URI: pathToURI(absTmplPath),
							Range: protocol.Range{
								Start: protocol.Position{Line: protocol.UInteger(pos.Line), Character: protocol.UInteger(pos.Character + 4)}, // Approx start of `define`
								End:   protocol.Position{Line: protocol.UInteger(pos.Line), Character: protocol.UInteger(pos.Character + 4 + len("define"))},
							},
						},
					}, nil
				}
			}
		}
	}

	// 2. Fallback to Values checking
	word, err := engine.WordAtPosition(lineText, int(charIdx))
	if err != nil || word == "" {
		return nil, nil
	}

	path, isValues, _ := engine.ResolveValuesPath(lines, int(lineIdx), word)
	if !isValues || len(path) == 0 {
		return nil, nil
	}

	// It's a Values path, let's find values.yaml
	filePath := uriToPath(uri)
	chartRoot := findChartRoot(filepath.Dir(filePath))
	if chartRoot == "" {
		log.Printf("Could not find chart root for %s", filePath)
		return nil, nil
	}

	valuesYamlPath := filepath.Join(chartRoot, "values.yaml")
	valuesContent, err := os.ReadFile(valuesYamlPath)
	if err != nil {
		log.Printf("Could not read values.yaml: %v", err)
		return nil, nil
	}

	pos, err := engine.FindYamlPosition(valuesContent, path)
	if err != nil || pos == nil {
		log.Printf("Could not find path %v in values.yaml: %v", path, err)
		return nil, nil
	}

	// Construct location pointing to values.yaml
	valuesURI := pathToURI(valuesYamlPath)

	// Create a precise range (we just highlight the first character of the key)
	targetRange := protocol.Range{
		Start: protocol.Position{Line: protocol.UInteger(pos.Line), Character: protocol.UInteger(pos.Character)},
		End:   protocol.Position{Line: protocol.UInteger(pos.Line), Character: protocol.UInteger(pos.Character + len(path[len(path)-1]))},
	}

	return []protocol.Location{
		{
			URI:   valuesURI,
			Range: targetRange,
		},
	}, nil
}

func (s *Server) textDocumentHover(context *glsp.Context, params *protocol.HoverParams) (*protocol.Hover, error) {
	// Let's implement basic Hover to return the path being hovered.
	uri := params.TextDocument.URI
	log.Printf("Hover request received for URI: %s at Line: %d, Char: %d", uri, params.Position.Line, params.Position.Character)

	content, ok := s.Store.Get(uri)
	if !ok {
		log.Printf("Document %s not found in store!", uri)
		return nil, nil
	}

	lines := strings.Split(content, "\n")
	lineIdx := params.Position.Line
	charIdx := params.Position.Character

	if int(lineIdx) >= len(lines) {
		return nil, nil
	}
	lineText := lines[lineIdx]

	// 1. Check if hovering over a template inclusion
	if tmplName, ok := engine.TemplateAtPosition(lineText, int(charIdx)); ok {
		filePath := uriToPath(uri)
		chartRoot := findChartRoot(filepath.Dir(filePath))
		if chartRoot != "" {
			ch, err := engine.ParseChart(chartRoot)
			if err == nil {
				// We need to fetch the template contents
				// In Helm, ch.Templates contains all templates.
				// Find the one that has the `define "tmplName"`
				defineRegex := regexp.MustCompile(fmt.Sprintf(`(?m)^[ \t]*{{-?\s*define\s+"%s"\s*-?}}\n([\s\S]*?){{-?\s*end\s*-?}}`, regexp.QuoteMeta(tmplName)))
				for _, t := range ch.Templates {
					content := string(t.Data)
					matches := defineRegex.FindStringSubmatch(content)
					if len(matches) > 1 {
						tmplContent := strings.TrimSpace(matches[1])
						markdown := fmt.Sprintf("**Helm Template Definition**: `%s`\n\n```gotemplate\n%s\n```", tmplName, tmplContent)
						return &protocol.Hover{
							Contents: protocol.MarkupContent{
								Kind:  protocol.MarkupKindMarkdown,
								Value: markdown,
							},
						}, nil
					}
				}

				// Template definition not found
				markdown := fmt.Sprintf("**Helm Template**: `%s`\n\n_Definition not found in chart templates._", tmplName)
				return &protocol.Hover{
					Contents: protocol.MarkupContent{
						Kind:  protocol.MarkupKindMarkdown,
						Value: markdown,
					},
				}, nil
			}
		}
	}

	// 2. Check for Sprig/Helm built-in functions
	word, err := engine.WordAtPosition(lineText, int(charIdx))
	if err != nil || word == "" {
		return nil, nil
	}

	if doc, ok := engine.SprigFunctions[word]; ok {
		return &protocol.Hover{
			Contents: protocol.MarkupContent{
				Kind:  protocol.MarkupKindMarkdown,
				Value: doc,
			},
		}, nil
	}

	// 3. Fallback to Values checking

	path, isValues, isMapKey := engine.ResolveValuesPath(lines, int(lineIdx), word)
	if !isValues || len(path) == 0 {
		return nil, nil
	}

	markdown := ""
	if isMapKey {
		markdown = fmt.Sprintf("**Helm Map Key**\n\nThis variable represents the dictionary keys of `.Values.%s`.\n\n", strings.Join(path, "."))
	} else {
		markdown = fmt.Sprintf("**Helm Value**: `%s`\n\n", strings.Join(path, "."))
	}

	filePath := uriToPath(uri)
	chartRoot := findChartRoot(filepath.Dir(filePath))
	if chartRoot != "" {
		// Load the chart and get merged values
		ch, err := engine.ParseChart(chartRoot)
		if err == nil {
			additionalVals, _ := engine.LoadAdditionalValues(chartRoot)
			mergedMap, err := engine.MergeValues(ch, additionalVals)
			if err == nil {
				// Navigate the map using the path
				var current interface{} = mergedMap
				for _, p := range path {
					// Direct key lookup
					if m, ok := current.(map[string]interface{}); ok && inMapPathStr(m, p) {
						current = m[p]
					} else if m, ok := current.(chartutil.Values); ok && inMapPathValues(m, p) {
						current = m[p]
					} else if m, ok := current.(map[interface{}]interface{}); ok && inMapPathIntf(m, p) {
						current = m[p]
					} else {
						// For Map Keys (like $host), we don't dive into the object values,
						// because 'p' is just the target dictionary (e.g. ingresses).
						// Wait, if it's the target dictionary, we ALREADY found it on the previous iteration
						// Actually, `path` is the base path (e.g. ["ingresses"]).
						// So this will find `current["ingresses"]` and stop.

						// The key 'p' wasn't found directly.
						// It might be a wildcard/loop map (e.g. ingresses -> ms-kube -> annotations).
						// We grab the first map value that actually has 'p'.
						if !isMapKey {
							foundNested := false
							if m, ok := current.(map[string]interface{}); ok {
								for _, v := range m {
									if subM, subOk := v.(map[string]interface{}); subOk && inMapPathStr(subM, p) {
										current = subM[p]
										foundNested = true
										break
									}
									if subM, subOk := v.(chartutil.Values); subOk && inMapPathValues(subM, p) {
										current = subM[p]
										foundNested = true
										break
									}
									if subM, subOk := v.(map[interface{}]interface{}); subOk && inMapPathIntf(subM, p) {
										current = subM[p]
										foundNested = true
										break
									}
								}
							} else if m, ok := current.(chartutil.Values); ok {
								for _, v := range m {
									if subM, subOk := v.(map[string]interface{}); subOk && inMapPathStr(subM, p) {
										current = subM[p]
										foundNested = true
										break
									}
									if subM, subOk := v.(chartutil.Values); subOk && inMapPathValues(subM, p) {
										current = subM[p]
										foundNested = true
										break
									}
									if subM, subOk := v.(map[interface{}]interface{}); subOk && inMapPathIntf(subM, p) {
										current = subM[p]
										foundNested = true
										break
									}
								}
							}

							if !foundNested {
								current = nil
								break // Key completely missing
							}
						} else {
							current = nil
							break
						}
					}
				}

				if current != nil {
					if isMapKey {
						// Extract and list available keys
						keys := []string{}
						if m, ok := current.(map[string]interface{}); ok {
							for k := range m {
								keys = append(keys, k)
							}
						} else if m, ok := current.(chartutil.Values); ok {
							for k := range m {
								keys = append(keys, k)
							}
						} else if m, ok := current.(map[interface{}]interface{}); ok {
							for k := range m {
								keys = append(keys, fmt.Sprintf("%v", k))
							}
						}

						if len(keys) > 0 {
							markdown += "**Available Keys:**\n"
							for _, k := range keys {
								markdown += fmt.Sprintf("- `%s`\n", k)
							}
						} else {
							markdown += "_No keys found in values_"
						}
					} else {
						// We found the value, let's format it beautifully
						yamlBytes, err := yaml.Marshal(current)
						if err == nil {
							markdown += fmt.Sprintf("```yaml\n%s\n```", strings.TrimSpace(string(yamlBytes)))
						} else {
							markdown += fmt.Sprintf("```json\n%v\n```", current)
						}
					}
				} else {
					markdown += "_Value not found in merged values_"
				}
			}
		}
	}

	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.MarkupKindMarkdown,
			Value: markdown,
		},
	}, nil
}

func inMapPathStr(m map[string]interface{}, key string) bool {
	_, ok := m[key]
	return ok
}

func (s *Server) validateDocument(context *glsp.Context, uri string, content string) {
	filePath := uriToPath(uri)
	chartRoot := findChartRoot(filepath.Dir(filePath))
	if chartRoot == "" {
		return
	}

	diagnostics, err := engine.ValidateChart(chartRoot, uri, content)
	if err != nil {
		log.Printf("Virtual render failed for validation: %v", err)
		return
	}

	if diagnostics == nil {
		diagnostics = make([]protocol.Diagnostic, 0)
	}

	// Publish diagnostics
	context.Notify("textDocument/publishDiagnostics", protocol.PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diagnostics,
	})
}

func inMapPathValues(m chartutil.Values, key string) bool {
	_, ok := m[key]
	return ok
}

func inMapPathIntf(m map[interface{}]interface{}, key string) bool {
	_, ok := m[key]
	return ok
}

// textDocumentFormatting handles document formatting requests.
// It trims trailing whitespace, ensures a final newline, and formats YAML structure
// while preserving Go template blocks.
func (s *Server) textDocumentFormatting(context *glsp.Context, params *protocol.DocumentFormattingParams) ([]protocol.TextEdit, error) {
	content, ok := s.Store.Get(params.TextDocument.URI)
	if !ok {
		return nil, nil
	}

	// Apply formatting pipeline
	formatted := engine.TrimTrailingWhitespace(content)
	formatted = engine.EnsureNewlineAtEnd(formatted)

	if formatted == content {
		return nil, nil // No changes
	}

	// Return a single edit replacing the entire document
	lines := strings.Split(content, "\n")
	return []protocol.TextEdit{
		{
			Range: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: uint32(len(lines)), Character: 0},
			},
			NewText: formatted,
		},
	}, nil
}

// textDocumentCodeAction returns applicable code actions for the given range.
func (s *Server) textDocumentCodeAction(context *glsp.Context, params *protocol.CodeActionParams) (interface{}, error) {
	content, ok := s.Store.Get(params.TextDocument.URI)
	if !ok {
		return nil, nil
	}

	actions := engine.GetCodeActions(content, params.Range, params.TextDocument.URI)
	if len(actions) == 0 {
		return nil, nil
	}

	return actions, nil
}

// executeCommand handles custom LSP commands.
// Currently supports: "helm.renderPreview" — renders the current template and returns YAML.
func (s *Server) executeCommand(context *glsp.Context, params *protocol.ExecuteCommandParams) (interface{}, error) {
	switch params.Command {
	case "helm.renderPreview":
		// Expect args[0] to be the document URI
		if len(params.Arguments) == 0 {
			return nil, fmt.Errorf("helm.renderPreview requires a document URI argument")
		}

		uri, ok := params.Arguments[0].(string)
		if !ok {
			return nil, fmt.Errorf("helm.renderPreview: argument must be a string URI")
		}

		content, ok := s.Store.Get(uri)
		if !ok {
			// Try reading from disk
			filePath := uriToPath(uri)
			data, err := os.ReadFile(filePath)
			if err != nil {
				return nil, fmt.Errorf("document not found: %s", uri)
			}
			content = string(data)
		}

		filePath := uriToPath(uri)
		chartRoot := findChartRoot(filepath.Dir(filePath))
		if chartRoot == "" {
			return nil, fmt.Errorf("no Chart.yaml found for %s", uri)
		}

		rendered, err := engine.RenderPreview(chartRoot, uri, content)
		if err != nil {
			return map[string]interface{}{
				"error":   err.Error(),
				"content": "",
			}, nil
		}

		return map[string]interface{}{
			"content": rendered,
			"error":   "",
		}, nil

	case "helm.renderFullPreview":
		// Expect args[0] to be the document URI (to find the chart root)
		if len(params.Arguments) == 0 {
			return nil, fmt.Errorf("helm.renderFullPreview requires a document URI argument")
		}

		uri, ok := params.Arguments[0].(string)
		if !ok {
			return nil, fmt.Errorf("helm.renderFullPreview: argument must be a string URI")
		}

		filePath := uriToPath(uri)
		chartRoot := findChartRoot(filepath.Dir(filePath))
		if chartRoot == "" {
			return nil, fmt.Errorf("no Chart.yaml found for %s", uri)
		}

		rendered, err := engine.RenderFullChart(chartRoot)
		if err != nil {
			return map[string]interface{}{
				"error":   err.Error(),
				"content": "",
			}, nil
		}

		return map[string]interface{}{
			"content": rendered,
			"error":   "",
		}, nil

	default:
		return nil, fmt.Errorf("unknown command: %s", params.Command)
	}
}
