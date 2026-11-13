// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package common

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCatalogHealthCheckConfig(t *testing.T) {
	t.Run("unmarshals shared connection test and schedule settings", func(t *testing.T) {
		vp := viper.New()
		vp.Set("catalogHealthCheck", map[string]any{
			"workerEnabled": true,
			"timeout":       "30s",
			"cronExpr":      "0 * * * *",
		})
		setting := &AppSetting{}

		require.NoError(t, vp.Unmarshal(setting))
		assert.True(t, setting.CatalogHealthCheck.WorkerEnabled)
		assert.Equal(t, 30*time.Second, setting.CatalogHealthCheck.Timeout)
		assert.Equal(t, "0 * * * *", setting.CatalogHealthCheck.CronExpr)
	})
}

func TestSetServiceSettings(t *testing.T) {
	t.Run("sets service settings from dependency map", func(t *testing.T) {
		resetAppSetting(t, map[string]map[string]any{
			rdsServiceName: {
				"host":     "db",
				"port":     3306,
				"user":     "root",
				"password": "secret",
			},
			mqServiceName: {
				"mqtype": "kafka",
				"mqhost": "mq",
				"mqport": 9092,
				"tenant": "openbkn",
				"auth": map[string]any{
					"username":  "mq-user",
					"password":  "mq-pass",
					"mechanism": "PLAIN",
				},
			},
			opensearchServiceName: {
				"host":     "os",
				"port":     9200,
				"protocol": "http",
				"user":     "os-user",
				"password": "os-pass",
			},
			hydraAdminServiceName: {
				"protocol": "http",
				"host":     "hydra",
				"port":     4445,
			},
			kafkaConnectServiceName: {
				"protocol": "http",
				"host":     "connect",
				"port":     8083,
			},
			modelFactoryManagerServiceName: {
				"protocol": "http",
				"host":     "model-manager",
				"port":     8001,
			},
			modelFactoryAPIServiceName: {
				"protocol": "https",
				"host":     "model-api",
				"port":     8002,
			},
		})
		SetDBSetting()
		SetMQSetting()
		SetOpenSearchSetting()
		SetHydraAdminSetting()
		SetKafkaConnectSetting()
		SetModelFactoryManagerSetting()
		SetModelFactoryAPISetting()

		assert.Equal(t, "db", appSetting.DBSetting.Host)
		assert.Equal(t, 3306, appSetting.DBSetting.Port)
		assert.Equal(t, "root", appSetting.DBSetting.Username)
		assert.Equal(t, "secret", appSetting.DBSetting.Password)
		assert.Equal(t, DATA_BASE_NAME, appSetting.DBSetting.DBName)

		assert.Equal(t, "kafka", appSetting.MQSetting.MQType)
		assert.Equal(t, "mq", appSetting.MQSetting.MQHost)
		assert.Equal(t, 9092, appSetting.MQSetting.MQPort)
		assert.Equal(t, "openbkn", appSetting.MQSetting.Tenant)
		assert.Equal(t, "mq-user", appSetting.MQSetting.Auth.Username)
		assert.Equal(t, "mq-pass", appSetting.MQSetting.Auth.Password)
		assert.Equal(t, "PLAIN", appSetting.MQSetting.Auth.Mechanism)

		assert.Equal(t, "os", appSetting.OpenSearchSetting.Host)
		assert.Equal(t, 9200, appSetting.OpenSearchSetting.Port)
		assert.Equal(t, "http", appSetting.OpenSearchSetting.Protocol)
		assert.Equal(t, "os-user", appSetting.OpenSearchSetting.Username)
		assert.Equal(t, "os-pass", appSetting.OpenSearchSetting.Password)

		assert.Equal(t, "http", appSetting.HydraAdminSetting.HydraAdminProcotol)
		assert.Equal(t, "hydra", appSetting.HydraAdminSetting.HydraAdminHost)
		assert.Equal(t, 4445, appSetting.HydraAdminSetting.HydraAdminPort)

		assert.Equal(t, "connect", appSetting.KafkaConnectSetting.Host)
		assert.Equal(t, 8083, appSetting.KafkaConnectSetting.Port)
		assert.Equal(t, "http", appSetting.KafkaConnectSetting.Protocol)
		assert.Equal(t, "http://model-manager:8001/api/private/mf-model-manager/v1", appSetting.ModelFactoryManagerUrl)
		assert.Equal(t, "https://model-api:8002/api/private/mf-model-api/v1", appSetting.ModelFactoryAPIUrl)
	})
}

func TestOverrideDBSettingFromEnv(t *testing.T) {
	t.Run("overrides all db fields from env", func(t *testing.T) {
		resetAppSetting(t, map[string]map[string]any{})
		appSetting.DBSetting.Host = "db"
		appSetting.DBSetting.Port = 3306
		appSetting.DBSetting.Username = "root"
		appSetting.DBSetting.Password = "secret"
		appSetting.DBSetting.DBName = "openbkn"
		t.Setenv("VEGA_DB_HOST", "127.0.0.1")
		t.Setenv("VEGA_DB_PORT", "15432")
		t.Setenv("VEGA_DB_USER", "tester")
		t.Setenv("VEGA_DB_PASSWORD", "test-pass")
		t.Setenv("VEGA_DB_NAME", "test_db")

		overrideDBSettingFromEnv()

		assert.Equal(t, "127.0.0.1", appSetting.DBSetting.Host)
		assert.Equal(t, 15432, appSetting.DBSetting.Port)
		assert.Equal(t, "tester", appSetting.DBSetting.Username)
		assert.Equal(t, "test-pass", appSetting.DBSetting.Password)
		assert.Equal(t, "test_db", appSetting.DBSetting.DBName)
	})

	t.Run("ignores invalid port and blank values", func(t *testing.T) {
		resetAppSetting(t, map[string]map[string]any{})
		appSetting.DBSetting.Host = "db"
		appSetting.DBSetting.Port = 3306
		appSetting.DBSetting.Username = "root"
		appSetting.DBSetting.Password = "secret"
		appSetting.DBSetting.DBName = "openbkn"
		t.Setenv("VEGA_DB_HOST", " ")
		t.Setenv("VEGA_DB_PORT", "invalid")
		t.Setenv("VEGA_DB_USER", " ")
		t.Setenv("VEGA_DB_NAME", " ")

		overrideDBSettingFromEnv()

		assert.Equal(t, "db", appSetting.DBSetting.Host)
		assert.Equal(t, 3306, appSetting.DBSetting.Port)
		assert.Equal(t, "root", appSetting.DBSetting.Username)
		assert.Equal(t, "secret", appSetting.DBSetting.Password)
		assert.Equal(t, "openbkn", appSetting.DBSetting.DBName)
	})
}

func TestNormalizeBknSafeURL(t *testing.T) {
	url, err := NormalizeBknSafeURL(" HTTPS://bkn-safe:3000/ ")
	require.NoError(t, err)
	assert.Equal(t, "https://bkn-safe:3000", url)

	for _, rawURL := range []string{"", "bkn-safe:3000", "ftp://bkn-safe", "http://bkn-safe?token=x"} {
		_, err := NormalizeBknSafeURL(rawURL)
		require.Error(t, err, rawURL)
	}
}

func TestLoadCryptoKeys(t *testing.T) {
	t.Run("requires a mounted key when enabled", func(t *testing.T) {
		resetAppSetting(t, nil)
		appSetting.CryptoSetting.Enabled = true
		require.ErrorContains(t, loadCryptoKeys(), "aesKeyPath is required")
		appSetting.CryptoSetting.AESKeyPath = filepath.Join(t.TempDir(), "missing-key")
		require.ErrorContains(t, loadCryptoKeys(), "failed to read AES key file")
	})
	t.Run("disabled writes allow an absent key and clear stale in-memory bytes", func(t *testing.T) {
		for _, path := range []string{"", filepath.Join(t.TempDir(), "unmounted-key")} {
			resetAppSetting(t, nil)
			appSetting.CryptoSetting = CryptoSetting{AESKeyPath: path, AESKey: []byte("previous-key")}
			require.NoError(t, loadCryptoKeys())
			assert.Empty(t, appSetting.CryptoSetting.AESKey)
		}
	})
	t.Run("loads decoded AES key independently of the write switch", func(t *testing.T) {
		resetAppSetting(t, nil)
		key := []byte(strings.Repeat("k", 32))
		path := filepath.Join(t.TempDir(), "aes-key")
		require.NoError(t, os.WriteFile(path, []byte("\n"+base64.StdEncoding.EncodeToString(key)+"\n"), 0600))
		appSetting.CryptoSetting = CryptoSetting{Enabled: true, AESKeyPath: path}
		require.NoError(t, loadCryptoKeys())
		assert.Equal(t, key, appSetting.CryptoSetting.AESKey)
		appSetting.CryptoSetting.Enabled = false
		appSetting.CryptoSetting.AESKey = nil
		require.NoError(t, loadCryptoKeys())
		assert.Equal(t, key, appSetting.CryptoSetting.AESKey)
	})
	t.Run("rejects malformed keys and retains the last successfully decoded key", func(t *testing.T) {
		for _, enabled := range []bool{false, true} {
			for _, size := range []int{-1, 0, 16, 24, 31, 33} {
				t.Run(fmt.Sprintf("enabled=%t/size=%d", enabled, size), func(t *testing.T) {
					resetAppSetting(t, nil)
					previous := []byte(strings.Repeat("p", 32))
					path := filepath.Join(t.TempDir(), "aes-key")
					content := "invalid-base64!"
					if size >= 0 {
						content = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", size)))
					}
					require.NoError(t, os.WriteFile(path, []byte(content), 0600))
					appSetting.CryptoSetting = CryptoSetting{Enabled: enabled, AESKeyPath: path, AESKey: previous}
					err := loadCryptoKeys()
					require.Error(t, err)
					if content != "" {
						assert.NotContains(t, err.Error(), content)
					}
					assert.Equal(t, previous, appSetting.CryptoSetting.AESKey)
				})
			}
		}
	})
	t.Run("disabled writes do not ignore file read errors", func(t *testing.T) {
		resetAppSetting(t, nil)
		appSetting.CryptoSetting.AESKeyPath = t.TempDir()
		require.ErrorContains(t, loadCryptoKeys(), "failed to read AES key file")
	})
	t.Run("key bytes cannot be configured or serialized", func(t *testing.T) {
		vp := viper.New()
		vp.SetConfigType("yaml")
		require.NoError(t, vp.ReadConfig(strings.NewReader("crypto:\n  enabled: false\n  aesKeyPath: /mounted/aes-key\n  aesKey: [1, 2, 3]\n")))
		var setting AppSetting
		require.NoError(t, vp.Unmarshal(&setting))
		assert.Empty(t, setting.CryptoSetting.AESKey)
		assert.Equal(t, "/mounted/aes-key", setting.CryptoSetting.AESKeyPath)
		setting.CryptoSetting.AESKey = []byte(strings.Repeat("k", 32))
		encoded, err := json.Marshal(setting.CryptoSetting)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), "AESKey\"")
		var decoded CryptoSetting
		require.NoError(t, json.Unmarshal([]byte(`{"AESKey":[1,2,3]}`), &decoded))
		assert.Empty(t, decoded.AESKey)
	})
}

func resetAppSetting(t *testing.T, depServices map[string]map[string]any) {
	t.Helper()

	old := appSetting
	appSetting = &AppSetting{
		DepServices: depServices,
	}
	t.Cleanup(func() {
		appSetting = old
	})
}

func TestLoadServiceCIDRPrefixes(t *testing.T) {
	t.Run("preserves strings and normalizes IPv4 and IPv6 prefixes", func(t *testing.T) {
		resetAppSetting(t, nil)
		raw := []string{"10.96.1.7/12", "fd00:1234::abcd/108"}
		appSetting.HTTPAPI.ServiceCIDRs = raw
		require.NoError(t, loadServiceCIDRPrefixes())
		assert.Equal(t, raw, appSetting.HTTPAPI.ServiceCIDRs)
		assert.Equal(t, []netip.Prefix{
			netip.MustParsePrefix("10.96.0.0/12"),
			netip.MustParsePrefix("fd00:1234::/108"),
		}, appSetting.HTTPAPI.ServiceCIDRPrefixes)
	})
	t.Run("reload replaces prefixes only after validation succeeds", func(t *testing.T) {
		resetAppSetting(t, nil)
		appSetting.HTTPAPI.ServiceCIDRs = []string{"10.96.1.7/12"}
		require.NoError(t, loadServiceCIDRPrefixes())
		appSetting.HTTPAPI.ServiceCIDRs = []string{"fd00:1234::abcd/108"}
		require.NoError(t, loadServiceCIDRPrefixes())
		previous := []netip.Prefix{netip.MustParsePrefix("fd00:1234::/108")}
		assert.Equal(t, previous, appSetting.HTTPAPI.ServiceCIDRPrefixes)
		appSetting.HTTPAPI.ServiceCIDRs = []string{"10.96.0.0/12", "invalid"}
		require.ErrorContains(t, loadServiceCIDRPrefixes(), "serviceCIDRs[1]")
		assert.Equal(t, previous, appSetting.HTTPAPI.ServiceCIDRPrefixes)
		appSetting.HTTPAPI.ServiceCIDRs = nil
		require.NoError(t, loadServiceCIDRPrefixes())
		assert.Empty(t, appSetting.HTTPAPI.ServiceCIDRPrefixes)
	})
	t.Run("defaults to no Service CIDRs", func(t *testing.T) {
		resetAppSetting(t, nil)
		require.NoError(t, loadServiceCIDRPrefixes())
		assert.Empty(t, appSetting.HTTPAPI.ServiceCIDRPrefixes)
	})
	for _, value := range []string{"not-a-cidr", "", "10.96.0.1", "10.96.0.0/33", "fd00:1234::/129"} {
		t.Run("rejects invalid CIDR "+value, func(t *testing.T) {
			resetAppSetting(t, nil)
			appSetting.HTTPAPI.ServiceCIDRs = []string{value}
			require.ErrorContains(t, loadServiceCIDRPrefixes(), "serviceCIDRs[0]")
			assert.Empty(t, appSetting.HTTPAPI.ServiceCIDRPrefixes)
		})
	}
}

func TestHTTPAPIConfig(t *testing.T) {
	t.Run("uses standard Viper string slice compatibility", func(t *testing.T) {
		for _, input := range []string{"10.96.1.7/12", "10.96.1.7/12,fd00:1234::abcd/108"} {
			vp := viper.New()
			vp.Set("httpAPI", map[string]any{"serviceCIDRs": input})
			setting := &AppSetting{}
			require.NoError(t, vp.Unmarshal(setting))
			assert.Equal(t, strings.Split(input, ","), setting.HTTPAPI.ServiceCIDRs)
			assert.Empty(t, setting.HTTPAPI.ServiceCIDRPrefixes)
		}
	})
	t.Run("runtime prefixes are not decoded or serialized", func(t *testing.T) {
		vp := viper.New()
		vp.SetConfigType("yaml")
		require.NoError(t, vp.ReadConfig(strings.NewReader("httpAPI:\n  serviceCIDRs: [10.96.1.7/12]\n  serviceCIDRPrefixes: [0.0.0.0/0]\n")))
		setting := &AppSetting{}
		require.NoError(t, vp.Unmarshal(setting))
		assert.Equal(t, []string{"10.96.1.7/12"}, setting.HTTPAPI.ServiceCIDRs)
		assert.Empty(t, setting.HTTPAPI.ServiceCIDRPrefixes)
		setting.HTTPAPI.ServiceCIDRPrefixes = []netip.Prefix{netip.MustParsePrefix("10.96.0.0/12")}
		encoded, err := json.Marshal(setting.HTTPAPI)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), "ServiceCIDRPrefixes")
		assert.NotContains(t, string(encoded), "serviceCIDRPrefixes")
		var decoded HTTPAPIConfig
		require.NoError(t, json.Unmarshal([]byte(`{"serviceCIDRs":["10.96.1.7/12"],"serviceCIDRPrefixes":["0.0.0.0/0"]}`), &decoded))
		assert.Empty(t, decoded.ServiceCIDRPrefixes)
	})
}
