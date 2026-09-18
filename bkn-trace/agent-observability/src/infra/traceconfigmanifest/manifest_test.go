package traceconfigmanifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaultManifestMapsOneSwitchToTraceAndEvidence(t *testing.T) {
	manifestPath := filepath.Join("..", "..", "..", "..", "..", "deploy", "trace-evidence-switch", "services.yaml")
	content, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read default manifest: %v", err)
	}

	manifest, err := Load(strings.NewReader(string(content)))
	if err != nil {
		t.Fatalf("load default manifest: %v", err)
	}

	if len(manifest.Services) != 4 {
		t.Fatalf("service count = %d, want 4", len(manifest.Services))
	}

	for _, service := range manifest.Services {
		if !service.Critical {
			t.Errorf("%s must be critical", service.Name)
		}

		enabled, err := service.Overrides(true)
		if err != nil {
			t.Errorf("%s enabled overrides: %v", service.Name, err)
			continue
		}
		if enabled[service.TraceValue] != true {
			t.Errorf("%s trace override = %v, want true", service.Name, enabled[service.TraceValue])
		}
		for _, evidenceValue := range service.EvidenceValues {
			if enabled[evidenceValue] != true {
				t.Errorf("%s evidence override %s = %v, want true", service.Name, evidenceValue, enabled[evidenceValue])
			}
		}

		disabled, err := service.Overrides(false)
		if err != nil {
			t.Errorf("%s disabled overrides: %v", service.Name, err)
			continue
		}
		for valuePath, value := range disabled {
			if value {
				t.Errorf("%s disabled override %s = true, want false", service.Name, valuePath)
			}
		}
	}
}

func TestLoadRejectsEvidenceWithoutTrace(t *testing.T) {
	_, err := Load(strings.NewReader(`
    version: 1
    services:
      - name: bkn-backend
        chart: adp/bkn/bkn-backend/helm/bkn-backend
        release: bkn-backend
        evidenceValues:
          - bknTrace.producerOutbox.enabled
`))
	if err == nil {
		t.Fatal("Load() error = nil, want invalid evidence mapping error")
	}
}
