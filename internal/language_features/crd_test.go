package languagefeatures

import (
	"path/filepath"
	"testing"
)

func TestLoadCRDsFromChart(t *testing.T) {
	sm := NewSchemaManager()

	// Path to the testdata directory relative to internal/language_features
	chartRoot := filepath.Join("..", "..", "testdata", "test-chart")

	// Test the loading functionality
	sm.LoadCRDsFromChart(chartRoot)

	desc, err := sm.GetFieldDescription("stable.example.com/v1", "CronTab", []string{"spec", "cronSpec"})
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	expected := "The cron schedule, e.g. * * * * *"
	if desc != expected {
		t.Errorf("Expected description %q, got %q", expected, desc)
	}
}

func TestCRDHoverIntegration(t *testing.T) {
	sm := NewSchemaManager()
	chartRoot := filepath.Join("..", "..", "testdata", "test-chart")
	sm.LoadCRDsFromChart(chartRoot)

	// Simulate hovering over the "cronSpec:" field in crontab.yaml
	// Line index 5 corresponds to "  cronSpec: "* * * * *""
	lines := []string{
		"apiVersion: stable.example.com/v1",
		"kind: CronTab",
		"metadata:",
		"  name: my-new-cron-object",
		"spec:",
		`  cronSpec: "* * * * *"`,
		`  image: "ubuntu:latest"`,
	}

	lineIdx := 5

	path := DetectYAMLPath(lines, lineIdx)
	if len(path) == 0 {
		t.Fatalf("DetectYAMLPath failed, got empty path")
	}

	apiVersion, kind := FindK8sRoot(lines, lineIdx)
	if apiVersion != "stable.example.com/v1" || kind != "CronTab" {
		t.Fatalf("FindK8sRoot failed: got %s/%s", apiVersion, kind)
	}

	desc, err := sm.GetFieldDescription(apiVersion, kind, path)
	if err != nil {
		t.Fatalf("GetFieldDescription failed: %v", err)
	}

	expected := "The cron schedule, e.g. * * * * *"
	if desc != expected {
		t.Errorf("Expected hover description %q, got %q", expected, desc)
	}
}
