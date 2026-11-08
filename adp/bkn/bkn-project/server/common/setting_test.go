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

// The chart passes the database account from a Secret; the file then holds no credentials, and a
// variable that is set wins over the file even when it is empty.
func TestSetDBSettingTakesTheAccountFromTheEnvironment(t *testing.T) {
	previous := appSetting
	defer func() { appSetting = previous }()
	file := map[string]any{"host": "mariadb", "port": 3306, "user": "file-user", "password": "file-password"}

	appSetting = &AppSetting{DepServices: map[string]map[string]any{rdsServiceName: file}}
	SetDBSetting()
	if got := appSetting.DBSetting; got.Username != "file-user" || got.Password != "file-password" {
		t.Fatalf("without variables: %q / %q", got.Username, got.Password)
	}

	t.Setenv(DBUserEnv, "bkn_project")
	t.Setenv(DBPasswordEnv, "")
	appSetting = &AppSetting{DepServices: map[string]map[string]any{rdsServiceName: file}}
	SetDBSetting()
	if got := appSetting.DBSetting; got.Username != "bkn_project" || got.Password != "" {
		t.Fatalf("with variables: %q / %q", got.Username, got.Password)
	}

	appSetting = &AppSetting{DepServices: map[string]map[string]any{rdsServiceName: {"host": "mariadb", "port": 3306}}}
	SetDBSetting()
	if got := appSetting.DBSetting; got.Username != "bkn_project" || got.Host != "mariadb" {
		t.Fatalf("file without credentials: %+v", got)
	}
}
