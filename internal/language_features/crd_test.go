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
