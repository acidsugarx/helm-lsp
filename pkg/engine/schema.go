package engine

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"gopkg.in/yaml.v3"
)

var GlobalSchemaManager = NewSchemaManager()

// ValidateSchematic takes a rendered YAML manifest and the original template content,
// runs JSON schema validation on the manifest, and maps the errors back to the template.
func ValidateSchematic(manifest string, templateContent string, templateName string) []protocol.Diagnostic {
	violations := GlobalSchemaManager.ValidateYAML(manifest)
	var diagnostics []protocol.Diagnostic

	lines := strings.Split(templateContent, "\n")

	for _, v := range violations {
		// Try to find the field name in the template content to attach the Diagnostic
		// Path is something like `/spec/template/spec/containers/0/ports`
		parts := strings.Split(v.Path, "/")
		fieldName := ""
		for i := len(parts) - 1; i >= 0; i-- {
			// skip array indices
			if !regexp.MustCompile(`^\d+$`).MatchString(parts[i]) && parts[i] != "" {
				fieldName = parts[i]
				break
			}
		}

		lineIdx := uint32(0)
		if fieldName != "" {
			// Find the first occurrence (best effort)
			for i, line := range lines {
				if strings.Contains(line, fieldName+":") {
					lineIdx = uint32(i)
					break
				}
			}
		}

		severity := protocol.DiagnosticSeverityWarning
		source := "k8s-schema"
		msg := fmt.Sprintf("K8s Schema: %s (at %s)", v.Message, v.Path)

		diagnostics = append(diagnostics, protocol.Diagnostic{
			Range: protocol.Range{
				Start: protocol.Position{Line: lineIdx, Character: 0},
				End:   protocol.Position{Line: lineIdx, Character: 500},
			},
			Severity: &severity,
			Source:   &source,
			Message:  msg,
		})
	}

	return diagnostics
}

// SchemaManager handles caching and validating against Kubernetes JSON schemas.
type SchemaManager struct {
	cacheDir string
	compiler *jsonschema.Compiler
	schemas  map[string]*jsonschema.Schema
	mu       sync.Mutex
}

// NewSchemaManager initializes a schema manager with a temporary cache directory.
func NewSchemaManager() *SchemaManager {
	cacheDir := filepath.Join(os.TempDir(), "helm-lsp-schemas")
	_ = os.MkdirAll(cacheDir, 0755)

	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft7

	return &SchemaManager{
		cacheDir: cacheDir,
		compiler: c,
		schemas:  make(map[string]*jsonschema.Schema),
	}
}

// SchemaViolation represents a validation error with an approximate line number.
type SchemaViolation struct {
	Path    string
	Message string
	Line    int
}

// ValidateYAML validates a multi-document YAML string against K8s schemas.
func (sm *SchemaManager) ValidateYAML(yamlContent string) []SchemaViolation {
	var violations []SchemaViolation

	decoder := yaml.NewDecoder(strings.NewReader(yamlContent))
	for {
		var rootNode yaml.Node
		err := decoder.Decode(&rootNode)
		if err == io.EOF {
			break
		}
		if err != nil {
			continue // Skip unparseable documents
		}

		if len(rootNode.Content) == 0 {
			continue
		}

		docNode := rootNode.Content[0]
		if docNode.Kind != yaml.MappingNode {
			continue
		}

		// Extract apiVersion and kind
		apiVersion, kind := extractKindAndVersion(docNode)
		if apiVersion == "" || kind == "" {
			continue
		}

		// Convert YAML AST to map for validation
		var data interface{}
		_ = docNode.Decode(&data)

		// Convert map[interface{}]interface{} to map[string]interface{} for jsonschema
		data = normalizeYAMLMap(data)

		schema, err := sm.getSchema(apiVersion, kind)
		if err != nil {
			log.Printf("Could not get schema for %s/%s: %v", apiVersion, kind, err)
			continue
		}

		if err := schema.Validate(data); err != nil {
			if validationErr, ok := err.(*jsonschema.ValidationError); ok {
				// We can have multiple errors
				for _, cause := range flattenValidationErrors(validationErr) {
					line := findLineForPath(docNode, cause.InstanceLocation)
					violations = append(violations, SchemaViolation{
						Path:    cause.InstanceLocation,
						Message: cause.Message,
						Line:    line,
					})
				}
			} else {
				violations = append(violations, SchemaViolation{
					Path:    "/",
					Message: err.Error(),
					Line:    docNode.Line,
				})
			}
		}
	}

	return violations
}

// flattenValidationErrors processes nested jsonschema errors.
func flattenValidationErrors(err *jsonschema.ValidationError) []*jsonschema.ValidationError {
	if len(err.Causes) == 0 {
		return []*jsonschema.ValidationError{err}
	}
	var flat []*jsonschema.ValidationError
	for _, cause := range err.Causes {
		flat = append(flat, flattenValidationErrors(cause)...)
	}
	return flat
}

// GetFieldDescription traverses the schema to find the description of a specific property path.
func (sm *SchemaManager) GetFieldDescription(apiVersion, kind string, path []string) (string, error) {
	schema, err := sm.getSchema(apiVersion, kind)
	if err != nil {
		return "", err
	}

	current := schema
	for _, p := range path {
		// Resolve refs
		for current.Ref != nil {
			current = current.Ref
		}

		// If current is an array/list, step into its definition
		if current.Items != nil {
			if s, ok := current.Items.(*jsonschema.Schema); ok {
				current = s
			} else if sArr, ok := current.Items.([]*jsonschema.Schema); ok && len(sArr) > 0 {
				current = sArr[0]
			}
		}

		// Resolve refs again after stepping into Items
		for current.Ref != nil {
			current = current.Ref
		}

		if current.Properties != nil {
			if next, ok := current.Properties[p]; ok {
				current = next
			} else {
				return "", fmt.Errorf("property %s not found", p)
			}
		} else {
			return "", fmt.Errorf("no properties at %s", p)
		}
	}

	for current.Ref != nil {
		current = current.Ref
	}

	return current.Description, nil
}

// getSchema fetches and caches the K8s schema for a given apiVersion and kind.
func (sm *SchemaManager) getSchema(apiVersion, kind string) (*jsonschema.Schema, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Normalize names: e.g. apps/v1 -> apps-v1
	apiPrefix := strings.ReplaceAll(apiVersion, "/", "-")
	schemaID := strings.ToLower(fmt.Sprintf("%s-%s.json", kind, apiPrefix))

	if schema, ok := sm.schemas[schemaID]; ok {
		return schema, nil
	}

	// Try loading from cache
	cachePath := filepath.Join(sm.cacheDir, schemaID)
	if _, err := os.Stat(cachePath); err == nil {
		sch, err := sm.compiler.Compile("file://" + cachePath)
		if err == nil {
			sm.schemas[schemaID] = sch
			return sch, nil
		}
	}

	// Github yannh/kubernetes-json-schema URL structure:
	// Deployment: https://raw.githubusercontent.com/yannh/kubernetes-json-schema/master/v1.28.0-standalone-strict/deployment-apps-v1.json
	// ConfigMap: https://raw.githubusercontent.com/yannh/kubernetes-json-schema/master/v1.28.0-standalone-strict/configmap-v1.json

	if !strings.Contains(apiVersion, "/") {
		schemaID = strings.ToLower(fmt.Sprintf("%s-%s.json", kind, apiVersion))
	}
	url := fmt.Sprintf("https://raw.githubusercontent.com/yannh/kubernetes-json-schema/master/v1.28.0-standalone-strict/%s", schemaID)

	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("schema not found at %s. status: %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Save to cache
	_ = os.WriteFile(cachePath, body, 0644)

	sch, err := sm.compiler.Compile("file://" + cachePath)
	if err != nil {
		return nil, err
	}

	sm.schemas[schemaID] = sch
	return sch, nil
}

// extractKindAndVersion extracts apiVersion and kind from a mapping node.
func extractKindAndVersion(node *yaml.Node) (apiVersion, kind string) {
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i].Value
		val := node.Content[i+1].Value
		if key == "apiVersion" {
			apiVersion = val
		} else if key == "kind" {
			kind = val
		}
	}
	return
}

// findLineForPath takes a JSONPointer path (e.g. /spec/replicas) and finds the corresponding
// line number in the yaml AST.
func findLineForPath(node *yaml.Node, path string) int {
	if path == "" || path == "/" {
		return node.Line
	}

	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	current := node

	for _, part := range parts {
		if part == "" {
			continue
		}

		if current.Kind == yaml.MappingNode {
			found := false
			for i := 0; i < len(current.Content); i += 2 {
				keyNode := current.Content[i]
				valNode := current.Content[i+1]

				// Handle ~1 (/) and ~0 (~) in JSONPointer
				partDecoded := strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")

				if keyNode.Value == partDecoded {
					current = valNode
					found = true
					break
				}
			}
			if !found {
				return current.Line
			}
		} else if current.Kind == yaml.SequenceNode {
			// part should be an index
			var idx int
			_, err := fmt.Sscanf(part, "%d", &idx)
			if err == nil && idx >= 0 && idx < len(current.Content) {
				current = current.Content[idx]
			} else {
				return current.Line
			}
		} else {
			return current.Line
		}
	}

	return current.Line // Or current.Content[0].Line for more precision if it's a scalar
}

// normalizeYAMLMap converts map[interface{}]interface{} generated by go-yaml
// into map[string]interface{} expected by json-schema.
func normalizeYAMLMap(in interface{}) interface{} {
	switch x := in.(type) {
	case map[interface{}]interface{}:
		m2 := map[string]interface{}{}
		for k, v := range x {
			m2[fmt.Sprint(k)] = normalizeYAMLMap(v)
		}
		return m2
	case []interface{}:
		for i, v := range x {
			x[i] = normalizeYAMLMap(v)
		}
	}
	return in
}
