package languagefeatures

import (
	"fmt"
	"strings"

	"github.com/acidsugarx/helm-lsp/internal/lsp/symboltable"
	"github.com/acidsugarx/helm-lsp/internal/protocol"
	"github.com/acidsugarx/helm-lsp/internal/tree-sitter/gotemplate"
	"github.com/acidsugarx/helm-lsp/internal/util"
	lsp "go.lsp.dev/protocol"
)

type VariablesFeature struct {
	*GenericDocumentUseCase
}

func NewVariablesFeature(genericDocumentUseCase *GenericDocumentUseCase) *VariablesFeature {
	return &VariablesFeature{
		GenericDocumentUseCase: genericDocumentUseCase,
	}
}

func (f *VariablesFeature) AppropriateForNode() bool {
	return (f.NodeType == gotemplate.NodeTypeIdentifier || f.NodeType == gotemplate.NodeTypeDollar) &&
		f.ParentNodeType == gotemplate.NodeTypeVariable
}

func (f *VariablesFeature) Definition() (result []lsp.Location, err error) {
	variableDefinition, err := f.Document.SymbolTable.GetVariableDefinitionForNode(f.GenericDocumentUseCase.Node, []byte(f.Document.Content))
	if err != nil {
		return []lsp.Location{}, err
	}

	return []lsp.Location{util.RangeToLocation(f.Document.URI, variableDefinition.Range)}, nil
}

func (f *VariablesFeature) References() (result []lsp.Location, err error) {
	variableReferences, err := f.Document.SymbolTable.GetVariableReferencesForNode(f.GenericDocumentUseCase.Node, []byte(f.Document.Content))
	if err != nil {
		return []lsp.Location{}, err
	}

	result = append(result, util.RangesToLocations(f.Document.URI, variableReferences)...)
	return result, nil
}

func (f *VariablesFeature) Completion() (result *lsp.CompletionList, err error) {
	return protocol.CompletionResults{}.WithVariableDefinitions(f.Document.SymbolTable.GetAllVariableDefinitions()).ToList(), nil
}

func (f *VariablesFeature) Hover() (string, error) {
	varDef, err := f.Document.SymbolTable.GetVariableDefinitionForNode(f.Node, []byte(f.Document.Content))
	if err != nil {
		return "", err
	}

	// Parse the definition value (e.g. ".Values.ingresses") into a TemplateContext
	templateContext := symboltable.NewTemplateContext(varDef.Value)
	if len(templateContext) == 0 {
		return "", nil
	}

	// Add type information to the hover
	var typeInfo string
	switch varDef.VariableType {
	case symboltable.VariableTypeRangeKeyOrIndex:
		typeInfo = fmt.Sprintf("**key** of `%s`", varDef.Value)
	case symboltable.VariableTypeRangeValue:
		typeInfo = fmt.Sprintf("**value** of `%s`", varDef.Value)
	default:
		typeInfo = fmt.Sprintf("assigned from `%s`", varDef.Value)
	}

	// If it starts with "Values", try to resolve the values from values.yaml
	if templateContext[0] == "Values" && f.Chart != nil {
		selector := templateContext.Tail()

		if varDef.VariableType == symboltable.VariableTypeRangeKeyOrIndex {
			// For key variables, show the list of map keys
			keysResult := f.keysOnlyHover(selector)
			if keysResult != "" {
				return fmt.Sprintf("%s\n\n%s", typeInfo, keysResult), nil
			}
		} else {
			// For value variables, show the full nested structure
			genericTemplate := &GenericTemplateContextFeature{f.GenericDocumentUseCase}
			templateFeature := &TemplateContextFeature{GenericTemplateContextFeature: genericTemplate}

			valuesResult, err := templateFeature.valuesHover(selector)
			if err == nil && strings.TrimSpace(valuesResult) != "" {
				return fmt.Sprintf("%s\n\n%s", typeInfo, valuesResult), nil
			}
		}
	}

	return typeInfo, nil
}

// keysOnlyHover resolves the values at the given selector and returns only the map keys.
func (f *VariablesFeature) keysOnlyHover(selector symboltable.TemplateContext) string {
	for _, valuesFiles := range f.Chart.ResolveValueFiles(selector, f.ChartStore) {
		for _, valuesFile := range valuesFiles.ValuesFiles.AllValuesFiles() {
			subValues, err := util.GetSubValuesForSelector(valuesFile.Values, valuesFiles.Selector)
			if err != nil || len(subValues) == 0 {
				continue
			}

			var keys []string
			for k := range subValues {
				keys = append(keys, fmt.Sprintf("- `%s`", k))
			}
			if len(keys) > 0 {
				return fmt.Sprintf("**Possible keys:**\n%s", strings.Join(keys, "\n"))
			}
		}
	}
	return ""
}
