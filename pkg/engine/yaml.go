package engine

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Position represents a 0-indexed line and character in a document.
type Position struct {
	Line      int
	Character int
}

// FindYamlPosition parses a YAML document and returns the position of the key corresponding to the path.
// For example, path []string{"image", "repository"} finds the "repository" key under "image".
func FindYamlPosition(content []byte, path []string) (*Position, error) {
	if len(path) == 0 {
		return nil, fmt.Errorf("empty path")
	}

	var root yaml.Node
	err := yaml.Unmarshal(content, &root)
	if err != nil {
		return nil, fmt.Errorf("failed to parse yaml: %w", err)
	}

	// Unmarshal usually returns a single DocumentNode at the root
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		node := findNode(root.Content[0], path, 0)
		if node != nil {
			// yaml.Node lines and columns are 1-indexed. We convert them to 0-indexed for LSP.
			return &Position{
				Line:      node.Line - 1,
				Character: node.Column - 1,
			}, nil
		}
	}

	return nil, nil // Not found
}

// findNode recursively searches for the key in a MappingNode.
func findNode(node *yaml.Node, path []string, index int) *yaml.Node {
	if node.Kind != yaml.MappingNode {
		return nil
	}

	targetKey := path[index]

	// Mapping nodes have pairs of (key, value) in Content
	for i := 0; i < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]

		if keyNode.Value == targetKey {
			// If this is the last element in the path, return the key node itself
			// (so the cursor jumps to the key, not the value)
			if index == len(path)-1 {
				return keyNode
			}

			// Otherwise, continue searching in the value node
			return findNode(valNode, path, index+1)
		}
	}

	return nil
}
