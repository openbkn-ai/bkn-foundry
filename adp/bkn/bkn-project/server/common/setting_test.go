// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package common

import (
	"testing"

	"github.com/spf13/viper"
)

func TestSetBknAgentSetting(t *testing.T) {
	previous := appSetting
	defer func() { appSetting = previous }()
	appSetting = &AppSetting{DepServices: map[string]map[string]any{
		bknAgentServiceName: {"protocol": "http", "host": "bkn-agent", "port": 30800},
	}}

	SetBknAgentSetting()

	if want := "http://bkn-agent:30800/api/bkn-agent/v1"; appSetting.BknAgentUrl != want {
		t.Fatalf("BknAgentUrl = %q, want %q", appSetting.BknAgentUrl, want)
	}
}

// The shipped configuration and the chart's values name every service the setting requires, so
// the service does not stop at start for a missing one.
func TestShippedConfigurationNamesEveryDependency(t *testing.T) {
	for _, file := range []struct{ path, key string }{
		{"../config/bkn-project-config.yaml", "depServices"},
		{"../../helm/bkn-project/values.yaml", "depServices"},
	} {
		vp := viper.New()
		vp.SetConfigFile(file.path)
		if err := vp.ReadInConfig(); err != nil {
			t.Fatalf("read %s: %v", file.path, err)
		}
		for _, name := range []string{bknBackendServiceName, agentOperatorServiceName, vegaBackendServiceName,
			bknAgentServiceName} {
			for _, field := range []string{"protocol", "host", "port"} {
				if !vp.IsSet(file.key + "." + name + "." + field) {
					t.Fatalf("%s has no %s.%s.%s", file.path, file.key, name, field)
				}
			}
		}
	}
}
