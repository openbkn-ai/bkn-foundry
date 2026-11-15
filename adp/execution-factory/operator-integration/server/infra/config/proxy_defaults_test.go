package config

import (
	"os"
	"testing"

	"github.com/creasty/defaults"
	"gopkg.in/yaml.v3"
)

// The shipped YAML, the struct defaults and the Helm values must agree on the proxy
// timeouts (#3191): they once said 30/120, 30/300 and 300/300.
func TestProxyTimeoutDefaultsAgreeAcrossYAMLStructAndHelm(t *testing.T) {
	var shipped struct {
		Proxy ProxyModuleConfig `yaml:"proxy_module"`
	}
	raw, err := os.ReadFile("agent-operator-integration.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(raw, &shipped); err != nil {
		t.Fatal(err)
	}
	var fromStruct ProxyModuleConfig
	if err := defaults.Set(&fromStruct); err != nil {
		t.Fatal(err)
	}
	var helm struct {
		Service struct {
			ProxyModule struct {
				DefaultTimeout int64 `yaml:"defaultTimeout"`
				MaxTimeout     int64 `yaml:"maxTimeout"`
			} `yaml:"proxyModule"`
		} `yaml:"service"`
	}
	raw, err = os.ReadFile("../../../helm/agent-operator-integration/values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(raw, &helm); err != nil {
		t.Fatal(err)
	}
	h := helm.Service.ProxyModule
	if shipped.Proxy.DefaultTimeout != fromStruct.DefaultTimeout || shipped.Proxy.DefaultTimeout != h.DefaultTimeout {
		t.Fatalf("default_timeout: yaml=%d struct=%d helm=%d", shipped.Proxy.DefaultTimeout, fromStruct.DefaultTimeout, h.DefaultTimeout)
	}
	if shipped.Proxy.MaxTimeout != fromStruct.MaxTimeout || shipped.Proxy.MaxTimeout != h.MaxTimeout {
		t.Fatalf("max_timeout: yaml=%d struct=%d helm=%d", shipped.Proxy.MaxTimeout, fromStruct.MaxTimeout, h.MaxTimeout)
	}
}
