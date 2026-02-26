package languagefeatures

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"gopkg.in/yaml.v3"
)

var GlobalSchemaManager = NewSchemaManager()

// SchemaManager handles caching and validating against Kubernetes JSON schemas.
type SchemaManager struct {
	cacheDir         string
	customSchemasDir string
	compiler         *jsonschema.Compiler
	schemas          map[string]*jsonschema.Schema
	mu               sync.Mutex
}

// NewSchemaManager initializes a schema manager with a temporary cache directory.
func NewSchemaManager() *SchemaManager {
	cacheDir := filepath.Join(os.TempDir(), "helm-lsp-schemas")
	_ = os.MkdirAll(cacheDir, 0755)

	homeDir, _ := os.UserHomeDir()
	customSchemasDir := ""
	if homeDir != "" {
		customSchemasDir = filepath.Join(homeDir, ".config", "helm-lsp", "schemas")
		_ = os.MkdirAll(customSchemasDir, 0755)
	}

	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft7

	return &SchemaManager{
		cacheDir:         cacheDir,
		customSchemasDir: customSchemasDir,
		compiler:         c,
		schemas:          make(map[string]*jsonschema.Schema),
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

// knownKindToApiVersion maps common K8s kinds to their apiVersion when the template
// uses expressions like {{ include "helpers.capabilities.deployment.apiVersion" $ }}.
var knownKindToApiVersion = map[string]string{
	"Deployment":              "apps/v1",
	"StatefulSet":             "apps/v1",
	"DaemonSet":               "apps/v1",
	"ReplicaSet":              "apps/v1",
	"Job":                     "batch/v1",
	"CronJob":                 "batch/v1",
	"Ingress":                 "networking.k8s.io/v1",
	"NetworkPolicy":           "networking.k8s.io/v1",
	"Service":                 "v1",
	"ConfigMap":               "v1",
	"Secret":                  "v1",
	"ServiceAccount":          "v1",
	"PersistentVolumeClaim":   "v1",
	"Pod":                     "v1",
	"Namespace":               "v1",
	"HorizontalPodAutoscaler": "autoscaling/v2",
	"Role":                    "rbac.authorization.k8s.io/v1",
	"RoleBinding":             "rbac.authorization.k8s.io/v1",
	"ClusterRole":             "rbac.authorization.k8s.io/v1",
	"ClusterRoleBinding":      "rbac.authorization.k8s.io/v1",
}

// ResolveApiVersion returns a valid apiVersion, falling back to the knownKindToApiVersion map
// if the parsed version contains template expressions.
func ResolveApiVersion(apiVersion, kind string) string {
	if apiVersion != "" && !strings.Contains(apiVersion, "{{") {
		return apiVersion
	}
	if fallback, ok := knownKindToApiVersion[kind]; ok {
		return fallback
	}
	return apiVersion
}

// GetFieldDescription traverses the raw cached JSON schema to find the description
// of a specific property path. Uses raw JSON instead of the compiled jsonschema.Schema
// because the compiler loses Description fields on schemas with type arrays like
// ["object", "null"] used in standalone-strict schemas.
func (sm *SchemaManager) GetFieldDescription(apiVersion, kind string, path []string) (string, error) {
	// Resolve template apiVersions to real ones
	apiVersion = ResolveApiVersion(apiVersion, kind)

	// Get the schema ID and cache path
	apiPrefix := strings.ReplaceAll(apiVersion, "/", "-")
	schemaID := strings.ToLower(fmt.Sprintf("%s-%s.json", kind, apiPrefix))
	if !strings.Contains(apiVersion, "/") {
		schemaID = strings.ToLower(fmt.Sprintf("%s-%s.json", kind, apiVersion))
	}

	// Ensure the schema file is cached (or exists in custom dir)
	cachePath := filepath.Join(sm.cacheDir, schemaID)
	customPath := filepath.Join(sm.customSchemasDir, schemaID)

	activePath := ""
	if sm.customSchemasDir != "" && fileExists(customPath) {
		activePath = customPath
	} else if fileExists(cachePath) {
		activePath = cachePath
	} else {
		// Trigger download via getSchema (which also caches the file)
		_, schemaErr := sm.getSchema(apiVersion, kind)
		if schemaErr != nil {
			return "", schemaErr
		}
		activePath = cachePath
	}

	// Read the raw JSON from activePath
	data, err := os.ReadFile(activePath)
	if err != nil {
		return "", fmt.Errorf("failed to read schema cache: %w", err)
	}

	var schemaMap map[string]interface{}
	if err := json.Unmarshal(data, &schemaMap); err != nil {
		return "", fmt.Errorf("failed to parse schema JSON: %w", err)
	}

	// Traverse the path through properties
	current := schemaMap
	for _, p := range path {
		props, ok := current["properties"].(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("no properties at %s", p)
		}
		next, ok := props[p].(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("property %s not found", p)
		}
		current = next
	}

	// Extract description
	if desc, ok := current["description"].(string); ok {
		return desc, nil
	}

	return "", nil
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

	// Try loading from custom schemas dir first
	if sm.customSchemasDir != "" {
		customPath := filepath.Join(sm.customSchemasDir, schemaID)
		if fileExists(customPath) {
			sch, err := sm.compiler.Compile("file://" + customPath)
			if err == nil {
				sm.schemas[schemaID] = sch
				return sch, nil
			}
		}
	}

	// Try loading from cache
	cachePath := filepath.Join(sm.cacheDir, schemaID)
	if fileExists(cachePath) {
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
		switch key {
		case "apiVersion":
			apiVersion = val
		case "kind":
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

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// LoadCRDsFromChart scans the crds/ directory in the chart root, parses any found
// CustomResourceDefinitions, extracts their OpenAPI v3 schema, and saves them
// as standalone JSON schema files in the cache directory.
func (sm *SchemaManager) LoadCRDsFromChart(chartRoot string) {
	crdsDir := filepath.Join(chartRoot, "crds")
	if !fileExists(crdsDir) {
		return
	}

	files, err := os.ReadDir(crdsDir)
	if err != nil {
		log.Printf("Failed to read crds dir: %v", err)
		return
	}

	for _, file := range files {
		if file.IsDir() || (!strings.HasSuffix(file.Name(), ".yaml") && !strings.HasSuffix(file.Name(), ".yml")) {
			continue
		}

		path := filepath.Join(crdsDir, file.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		sm.parseAndCacheCRD(content)
	}
}

func (sm *SchemaManager) parseAndCacheCRD(content []byte) {
	// Parse multi-doc YAML
	decoder := yaml.NewDecoder(strings.NewReader(string(content)))
	for {
		var doc map[string]interface{}
		err := decoder.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil || doc == nil {
			continue
		}

		kind, _ := doc["kind"].(string)
		if kind != "CustomResourceDefinition" {
			continue
		}

		spec, ok := doc["spec"].(map[string]interface{})
		if !ok {
			continue
		}

		group, _ := spec["group"].(string)
		names, ok := spec["names"].(map[string]interface{})
		if !ok {
			continue
		}

		crdKind, _ := names["kind"].(string)
		if group == "" || crdKind == "" {
			continue
		}

		versions, ok := spec["versions"].([]interface{})
		if !ok {
			continue
		}

		for _, v := range versions {
			versionCfg, ok := v.(map[string]interface{})
			if !ok {
				continue
			}

			versionName, _ := versionCfg["name"].(string)
			if versionName == "" {
				continue
			}

			schema, ok := versionCfg["schema"].(map[string]interface{})
			if !ok {
				continue
			}

			openAPIV3, ok := schema["openAPIV3Schema"].(map[string]interface{})
			if !ok {
				continue
			}

			// Clean up openAPIV3Schema into acceptable JSON schema
			schemaBytes, err := json.Marshal(normalizeYAMLMap(openAPIV3))
			if err != nil {
				continue
			}

			// Construct API version and schema ID
			apiVersion := fmt.Sprintf("%s/%s", group, versionName)
			apiPrefix := strings.ReplaceAll(apiVersion, "/", "-")
			schemaID := strings.ToLower(fmt.Sprintf("%s-%s.json", crdKind, apiPrefix))

			cachePath := filepath.Join(sm.cacheDir, schemaID)
			_ = os.WriteFile(cachePath, schemaBytes, 0644)
			log.Printf("Cached CRD schema for %s/%s at %s", apiVersion, crdKind, cachePath)
		}
	}
}
