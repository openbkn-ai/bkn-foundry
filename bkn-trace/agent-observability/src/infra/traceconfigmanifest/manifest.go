package traceconfigmanifest

import (
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v2"
)

type Manifest struct {
	Version  int       `yaml:"version"`
	Services []Service `yaml:"services"`
}

type Service struct {
	Name           string   `yaml:"name"`
	Chart          string   `yaml:"chart"`
	Release        string   `yaml:"release"`
	Critical       bool     `yaml:"critical"`
	TraceValue     string   `yaml:"traceValue"`
	EvidenceValues []string `yaml:"evidenceValues"`
}

func Load(reader io.Reader) (Manifest, error) {
	var manifest Manifest
	decoder := yaml.NewDecoder(reader)
	decoder.SetStrict(true)
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode trace evidence manifest: %w", err)
	}
	if manifest.Version != 1 {
		return Manifest{}, fmt.Errorf("unsupported trace evidence manifest version %d", manifest.Version)
	}
	if len(manifest.Services) == 0 {
		return Manifest{}, fmt.Errorf("trace evidence manifest has no services")
	}
	seen := make(map[string]struct{}, len(manifest.Services))
	for _, service := range manifest.Services {
		if err := service.validate(); err != nil {
			return Manifest{}, err
		}
		if _, ok := seen[service.Name]; ok {
			return Manifest{}, fmt.Errorf("duplicate trace evidence service %q", service.Name)
		}
		seen[service.Name] = struct{}{}
	}
	return manifest, nil
}

func (service Service) Overrides(enabled bool) (map[string]bool, error) {
	if err := service.validate(); err != nil {
		return nil, err
	}
	overrides := map[string]bool{service.TraceValue: enabled}
	for _, valuePath := range service.EvidenceValues {
		overrides[valuePath] = enabled
	}
	return overrides, nil
}

func (service Service) validate() error {
	if strings.TrimSpace(service.Name) == "" {
		return fmt.Errorf("trace evidence service name is required")
	}
	if strings.TrimSpace(service.Chart) == "" {
		return fmt.Errorf("trace evidence service %q chart is required", service.Name)
	}
	if strings.TrimSpace(service.Release) == "" {
		return fmt.Errorf("trace evidence service %q release is required", service.Name)
	}
	if strings.TrimSpace(service.TraceValue) == "" {
		return fmt.Errorf("trace evidence service %q trace value is required when evidence is configured", service.Name)
	}
	for _, valuePath := range service.EvidenceValues {
		if strings.TrimSpace(valuePath) == "" {
			return fmt.Errorf("trace evidence service %q has an empty evidence value", service.Name)
		}
	}
	return nil
}
