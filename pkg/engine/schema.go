package engine

import (
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v5"
	_ "github.com/santhosh-tekuri/jsonschema/v5/httploader"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"gopkg.in/yaml.v3"
)

var (
	schemaCache = make(map[string]*jsonschema.Schema)
	schemaMutex sync.Mutex
	compiler    = jsonschema.NewCompiler()
)

// ValidateSchematic takes the rendered multi-document YAML and validates each K8s resource.
// It skips documents that do not originate from the active templateName.
// It tries to map schema violations back to a line in the Go template.
func ValidateSchematic(manifests string, templateContent string, templateName string) []protocol.Diagnostic {
	var diagnostics []protocol.Diagnostic

	// Helm output can have multiple YAML documents
	docs := strings.Split(manifests, "---")
	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}

		// Filter out manifests not originating from our active editor file
		if templateName != "" {
			lines := strings.SplitN(doc, "\n", 2)
			if len(lines) > 0 && strings.HasPrefix(lines[0], "# Source:") {
				sourceLine := strings.TrimSpace(lines[0])
				if !strings.HasSuffix(sourceLine, templateName) {
					log.Printf("Schema validator: skipping doc (source=%q, want suffix=%q)", sourceLine, templateName)
					continue
				}
				log.Printf("Schema validator: matched doc (source=%q)", sourceLine)
			} else {
				log.Printf("Schema validator: doc has no # Source header, including it")
			}
		}

		var obj map[string]interface{}
		if err := yaml.Unmarshal([]byte(doc), &obj); err != nil {
			// The rendered YAML is malformed — this usually means a template bracket was broken
			// (e.g. `{ include` instead of `{{ include`), causing raw template syntax in the output.
			log.Printf("Schema validator: YAML unmarshal error in rendered output: %v", err)
			severity := protocol.DiagnosticSeverityError
			source := "helm-render"
			msg := fmt.Sprintf("Rendered YAML is invalid: %s (possible missing '{{' bracket in template)", err.Error())
			diagnostics = append(diagnostics, protocol.Diagnostic{
				Range: protocol.Range{
					Start: protocol.Position{Line: 0, Character: 0},
					End:   protocol.Position{Line: 0, Character: 500},
				},
				Severity: &severity,
				Source:   &source,
				Message:  msg,
			})
			continue
		}

		kindVal, okKind := obj["kind"].(string)
		apiVal, okApi := obj["apiVersion"].(string)

		if !okKind || !okApi {
			log.Printf("Schema validator: doc missing kind=%v or apiVersion=%v, skipping", okKind, okApi)
			continue // Not a valid K8s resource
		}

		log.Printf("Schema validator: validating %s/%s (apiVersion=%s)", kindVal, getMetadataName(obj), apiVal)
		schema, err := loadK8sSchema(kindVal, apiVal)
		if err != nil {
			log.Printf("Schema validator: could not load schema for %s/%s: %v", kindVal, apiVal, err)
			continue
		}

		if err := schema.Validate(obj); err != nil {
			log.Printf("Schema validator: validation errors for %s/%s: %v", kindVal, getMetadataName(obj), err)
			if validationErr, ok := err.(*jsonschema.ValidationError); ok {
				for _, cause := range validationErr.Causes {
					diag := buildDiagnostic(cause, kindVal, getMetadataName(obj), templateContent)
					diagnostics = append(diagnostics, diag)
				}
				if len(validationErr.Causes) == 0 {
					diag := buildDiagnostic(validationErr, kindVal, getMetadataName(obj), templateContent)
					diagnostics = append(diagnostics, diag)
				}
			}
		} else {
			log.Printf("Schema validator: %s/%s passed validation ✓", kindVal, getMetadataName(obj))
		}
	}

	return diagnostics
}

func getMetadataName(obj map[string]interface{}) string {
	if meta, ok := obj["metadata"].(map[string]interface{}); ok {
		if name, ok := meta["name"].(string); ok {
			return `"` + name + `"`
		}
	}
	return "unknown"
}

// loadK8sSchema fetches the JSON schema from yannh/kubernetes-json-schema for the given kind and apiVersion
func loadK8sSchema(kind, apiVersion string) (*jsonschema.Schema, error) {
	apiGroup := apiVersion
	if strings.Contains(apiVersion, "/") {
		parts := strings.Split(apiVersion, "/")
		if len(parts) == 2 {
			apiGroup = parts[0] + "-" + parts[1]
		}
	}
	fileName := fmt.Sprintf("%s-%s.json", strings.ToLower(kind), strings.ToLower(apiGroup))
	schemaURL := fmt.Sprintf("https://raw.githubusercontent.com/yannh/kubernetes-json-schema/master/v1.30.0-standalone-strict/%s", fileName)

	schemaMutex.Lock()
	defer schemaMutex.Unlock()

	if sch, exists := schemaCache[schemaURL]; exists {
		return sch, nil
	}

	// jsonschema compiler automatically fetches the URL
	sch, err := compiler.Compile(schemaURL)
	if err != nil {
		return nil, err
	}

	schemaCache[schemaURL] = sch
	return sch, nil
}

func buildDiagnostic(validationErr *jsonschema.ValidationError, kind, name, templateContent string) protocol.Diagnostic {
	msg := fmt.Sprintf("K8s Schema [%s %s]: %s (at %s)", kind, name, validationErr.Message, validationErr.InstanceLocation)

	// Try to find the field name in the template content to attach the Diagnostic
	// InstanceLocation is something like `/spec/template/spec/containers/0/ports`
	parts := strings.Split(validationErr.InstanceLocation, "/")
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
		lines := strings.Split(templateContent, "\n")
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
	return protocol.Diagnostic{
		Range: protocol.Range{
			Start: protocol.Position{Line: lineIdx, Character: 0},
			End:   protocol.Position{Line: lineIdx, Character: 500},
		},
		Severity: &severity,
		Source:   &source,
		Message:  msg,
	}
}
