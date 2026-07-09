package common

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetAuthEnabled(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want bool
	}{
		{name: "defaults enabled", want: true},
		{name: "false disables", env: "false", want: false},
		{name: "zero disables", env: "0", want: false},
		{name: "true enables", env: "true", want: true},
		{name: "other value enables", env: "off", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AUTH_ENABLED", tt.env)

			assert.Equal(t, tt.want, GetAuthEnabled())
		})
	}
}

func TestGetDebugMode(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want bool
	}{
		{name: "defaults disabled", want: false},
		{name: "true enables", env: "true", want: true},
		{name: "one enables", env: "1", want: true},
		{name: "mixed case true enables", env: " TrUe ", want: true},
		{name: "false disables", env: "false", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DEBUG_MODE", tt.env)

			assert.Equal(t, tt.want, GetDebugMode())
		})
	}
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
			redisServiceName: {
				"connecttype": "sentinel",
				"connectinfo": map[string]any{
					"username":         "redis-user",
					"password":         "redis-pass",
					"sentinelhost":     "sentinel",
					"sentinelport":     26379,
					"sentinelusername": "sentinel-user",
					"sentinelpassword": "sentinel-pass",
					"mastergroupname":  "mymaster",
					"host":             "redis",
					"port":             6379,
					"masterhost":       "master",
					"masterport":       6379,
					"slavehost":        "slave",
					"slaveport":        6380,
				},
			},
			hydraAdminServiceName: {
				"protocol": "http",
				"host":     "hydra",
				"port":     4445,
			},
			permissionServiceName: {
				"protocol": "http",
				"host":     "permission",
				"port":     8080,
			},
			userMgmtServiceName: {
				"protocol": "https",
				"host":     "users",
				"port":     8443,
			},
			kafkaConnectServiceName: {
				"protocol": "http",
				"host":     "connect",
				"port":     8083,
			},
			mfModelManagerServiceName: {
				"protocol": "http",
				"host":     "model-manager",
				"port":     8001,
			},
			mfModelApiServiceName: {
				"protocol": "https",
				"host":     "model-api",
				"port":     8002,
			},
		})
		t.Setenv("AUTH_ENABLED", "true")

		SetDBSetting()
		SetMQSetting()
		SetOpenSearchSetting()
		SetHydraAdminSetting()
		SetPermissionSetting()
		SetUserMgmtSetting()
		SetRedisSetting()
		SetKafkaConnectSetting()
		SetMfModelManagerSetting()
		SetMfModelApiSetting()

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
		assert.Equal(t, "http://permission:8080/api/authorization/v1", appSetting.PermissionUrl)
		assert.Equal(t, "https://users:8443", appSetting.UserMgmtUrl)

		assert.Equal(t, "sentinel", appSetting.RedisSetting.ConnectType)
		assert.Equal(t, "redis-user", appSetting.RedisSetting.Username)
		assert.Equal(t, "sentinel", appSetting.RedisSetting.SentinelHost)
		assert.Equal(t, "mymaster", appSetting.RedisSetting.MasterGroupName)
		assert.Equal(t, "redis", appSetting.RedisSetting.Host)
		assert.Equal(t, 6380, appSetting.RedisSetting.SlavePort)

		assert.Equal(t, "connect", appSetting.KafkaConnectSetting.Host)
		assert.Equal(t, 8083, appSetting.KafkaConnectSetting.Port)
		assert.Equal(t, "http", appSetting.KafkaConnectSetting.Protocol)
		assert.Equal(t, "http://model-manager:8001", appSetting.MfModelManagerUrl)
		assert.Equal(t, "https://model-api:8002", appSetting.MfModelApiUrl)
	})
}

func TestAuthDisabledSkipsAuthDependentSettings(t *testing.T) {
	t.Run("skips auth dependent services", func(t *testing.T) {
		resetAppSetting(t, map[string]map[string]any{})
		t.Setenv("AUTH_ENABLED", "false")

		require.NotPanics(t, SetHydraAdminSetting)
		require.NotPanics(t, SetPermissionSetting)
		require.NotPanics(t, SetUserMgmtSetting)

		assert.Empty(t, appSetting.HydraAdminSetting)
		assert.Empty(t, appSetting.PermissionUrl)
		assert.Empty(t, appSetting.UserMgmtUrl)
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

func TestLoadCryptoKeys(t *testing.T) {
	t.Run("skips when disabled", func(t *testing.T) {
		resetAppSetting(t, map[string]map[string]any{})
		appSetting.CryptoSetting.Enabled = false

		require.NoError(t, loadCryptoKeys())
	})

	t.Run("requires paths when enabled", func(t *testing.T) {
		resetAppSetting(t, map[string]map[string]any{})
		appSetting.CryptoSetting.Enabled = true

		require.ErrorContains(t, loadCryptoKeys(), "privateKeyPath is required")
	})

	t.Run("requires public key path when enabled", func(t *testing.T) {
		resetAppSetting(t, map[string]map[string]any{})
		appSetting.CryptoSetting = CryptoSetting{
			Enabled:        true,
			PrivateKeyPath: "private.pem",
		}

		require.ErrorContains(t, loadCryptoKeys(), "publicKeyPath is required")
	})

	t.Run("returns private key read error", func(t *testing.T) {
		resetAppSetting(t, map[string]map[string]any{})
		appSetting.CryptoSetting = CryptoSetting{
			Enabled:        true,
			PrivateKeyPath: filepath.Join(t.TempDir(), "missing-private.pem"),
			PublicKeyPath:  "public.pem",
		}

		require.ErrorContains(t, loadCryptoKeys(), "failed to read private key file")
	})

	t.Run("returns public key read error", func(t *testing.T) {
		resetAppSetting(t, map[string]map[string]any{})
		dir := t.TempDir()
		privateKeyPath := filepath.Join(dir, "private.pem")
		require.NoError(t, os.WriteFile(privateKeyPath, []byte("private-key"), 0600))
		appSetting.CryptoSetting = CryptoSetting{
			Enabled:        true,
			PrivateKeyPath: privateKeyPath,
			PublicKeyPath:  filepath.Join(dir, "missing-public.pem"),
		}

		require.ErrorContains(t, loadCryptoKeys(), "failed to read public key file")
	})

	t.Run("loads private and public keys", func(t *testing.T) {
		resetAppSetting(t, map[string]map[string]any{})
		dir := t.TempDir()
		privateKeyPath := filepath.Join(dir, "private.pem")
		publicKeyPath := filepath.Join(dir, "public.pem")
		require.NoError(t, os.WriteFile(privateKeyPath, []byte("private-key"), 0600))
		require.NoError(t, os.WriteFile(publicKeyPath, []byte("public-key"), 0600))
		appSetting.CryptoSetting = CryptoSetting{
			Enabled:        true,
			PrivateKeyPath: privateKeyPath,
			PublicKeyPath:  publicKeyPath,
		}

		require.NoError(t, loadCryptoKeys())
		assert.Equal(t, "private-key", appSetting.CryptoSetting.PrivateKey)
		assert.Equal(t, "public-key", appSetting.CryptoSetting.PublicKey)
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
