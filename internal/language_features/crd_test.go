package languagefeatures

import (
	"testing"
)

func TestLoadCRDsFromChart(t *testing.T) {
	sm := NewSchemaManager()

	// Load the CRDs from the mes-chat-sfrum charter
	chartRoot := "/Users/acidsugarx/CODES/w/mo/mes-chat-sfrum/charts/helm-chart"
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
