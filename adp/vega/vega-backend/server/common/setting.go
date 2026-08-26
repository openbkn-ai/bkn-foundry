// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/fsnotify/fsnotify"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	libmq "github.com/openbkn-ai/bkn-foundry/comm-go/mq"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/spf13/viper"

	"vega-backend/version"
)

// ServerSetting server configuration item
type ServerSetting struct {
	RunMode      string        `mapstructure:"runMode"`
	HttpPort     int           `mapstructure:"httpPort"`
	Language     string        `mapstructure:"language"`
	ReadTimeOut  time.Duration `mapstructure:"readTimeOut"`
	WriteTimeout time.Duration `mapstructure:"writeTimeOut"`
}

// CryptoSetting RSA key configuration item
type CryptoSetting struct {
	Enabled        bool   `mapstructure:"enabled"`
	PrivateKey     string `mapstructure:"-"`              // RSA private key (PEM format) - Read from file
	PublicKey      string `mapstructure:"-"`              // RSA Public Key (PEM format) - Read from file
	PrivateKeyPath string `mapstructure:"privateKeyPath"` // The path of the RSA private key file
	PublicKeyPath  string `mapstructure:"publicKeyPath"`  // RSA public key file path
}

// KafkaConnectSetting KafkaConnect configuration item
type KafkaConnectSetting struct {
	Host     string
	Port     int
	Protocol string
}

// RateLimitingConfig rate limiting configuration item
type RateLimitingConfig struct {
	Concurrency ConcurrencyConfig `mapstructure:"concurrency"`
}

// ConcurrencyConfig concurrency control configuration item
type ConcurrencyConfig struct {
	Enabled bool                    `mapstructure:"enabled"`
	Global  GlobalConcurrencyConfig `mapstructure:"global"`
}

// GlobalConcurrencyConfig global concurrent configuration
type GlobalConcurrencyConfig struct {
	MaxConcurrentQueries int `mapstructure:"max_concurrent_queries"`
}

// QueryConfig query service configuration item
type QueryConfig struct {
	CursorMaxSessions int `mapstructure:"cursorMaxSessions"`
}

// CatalogHealthCheckConfig configures the periodic physical Catalog health-check worker.
type CatalogHealthCheckConfig struct {
	// WorkerEnabled only controls periodic checks; it does not disable create/update/manual connection tests.
	WorkerEnabled bool `mapstructure:"workerEnabled"`
	// Timeout applies to every connector TestConnection invocation. Durations use Go duration strings.
	Timeout time.Duration `mapstructure:"timeout"`
	// CronExpr is the platform default Cron used by inherit-mode Schedules.
	CronExpr string `mapstructure:"cronExpr"`
}

// IndexCleanupConfig configures the periodic cleanup of managed local indexes.
type IndexCleanupConfig struct {
	WorkerEnabled    bool          `mapstructure:"workerEnabled"`
	Interval         time.Duration `mapstructure:"interval"`
	ProtectionPeriod time.Duration `mapstructure:"protectionPeriod"`
	MaxDeletesPerRun int           `mapstructure:"maxDeletesPerRun"`
	DryRun           bool          `mapstructure:"dryRun"`
}

// TaskWorkerConfig configures local worker-pool concurrency for database-backed tasks.
type TaskWorkerConfig struct {
	SemanticWorkerCount  int `mapstructure:"semanticWorkerCount"`
	DiscoverWorkerCount  int `mapstructure:"discoverWorkerCount"`
	BatchWorkerCount     int `mapstructure:"batchWorkerCount"`
	StreamingWorkerCount int `mapstructure:"streamingWorkerCount"`
}

// AppSetting contains application configuration.
type AppSetting struct {
	ServerSetting       ServerSetting             `mapstructure:"server"`
	LogSetting          logger.LogSetting         `mapstructure:"log"`
	OtelSetting         otel.OtelConfig           `mapstructure:"otel"`
	CryptoSetting       CryptoSetting             `mapstructure:"crypto"`
	DepServices         map[string]map[string]any `mapstructure:"depServices"`
	RateLimitingSetting RateLimitingConfig        `mapstructure:"rateLimiting"`
	QuerySetting        QueryConfig               `mapstructure:"query"`
	CatalogHealthCheck  CatalogHealthCheckConfig  `mapstructure:"catalogHealthCheck"`
	IndexCleanup        IndexCleanupConfig        `mapstructure:"indexCleanup"`
	TaskWorker          TaskWorkerConfig          `mapstructure:"taskWorker"`

	DBSetting           libdb.DBSetting
	MQSetting           libmq.MQSetting
	OpenSearchSetting   rest.OpenSearchClientConfig
	HydraAdminSetting   hydra.HydraAdminSetting
	KafkaConnectSetting KafkaConnectSetting

	PermissionUrl          string
	UserMgmtUrl            string
	ModelFactoryManagerUrl string
	ModelFactoryAPIUrl     string
	BknAgentUrl            string
}

const (
	// ConfigFile configuration file information
	configPath string = "./config/"
	configName string = "vega-backend-config"
	configType string = "yaml"

	rdsServiceName                 string = "rds"
	mqServiceName                  string = "mq"
	opensearchServiceName          string = "opensearch"
	permissionServiceName          string = "authorization-private"
	userMgmtServiceName            string = "user-management"
	hydraAdminServiceName          string = "hydra-admin"
	kafkaConnectServiceName        string = "kafka-connect"
	modelFactoryManagerServiceName string = "mf-model-manager"
	modelFactoryAPIServiceName     string = "mf-model-api"
	bknAgentServiceName            string = "bkn-agent"

	DATA_BASE_NAME string = "openbkn"
)

var (
	appSetting *AppSetting
	vp         *viper.Viper

	settingOnce sync.Once

	// Current system time zone
	APP_LOCATION *time.Location
)

// NewSetting reads the service configuration
func NewSetting() *AppSetting {
	settingOnce.Do(func() {
		appSetting = &AppSetting{}
		vp = viper.New()
		initSetting(vp)
	})

	return appSetting
}

// initSetting initializes application configuration.
func initSetting(vp *viper.Viper) {
	logger.Infof("Init Setting From File %s%s.%s", configPath, configName, configType)

	vp.AddConfigPath(configPath)
	vp.SetConfigName(configName)
	vp.SetConfigType(configType)

	loadSetting(vp)

	vp.WatchConfig()
	vp.OnConfigChange(func(e fsnotify.Event) {
		logger.Infof("Config file changed:%s", e)
		loadSetting(vp)
	})
}

// Read the configuration file
func loadSetting(vp *viper.Viper) {
	logger.Infof("Load Setting File %s%s.%s", configPath, configName, configType)

	if err := vp.ReadInConfig(); err != nil {
		logger.Fatalf("err:%s\n", err)
	}

	if err := vp.Unmarshal(appSetting); err != nil {
		logger.Fatalf("err:%s\n", err)
	}

	// Joint debugging /CI: Allows environment variables to override listening ports to avoid conflicts with local occupied ports
	if hp := strings.TrimSpace(os.Getenv("VEGA_HTTP_PORT")); hp != "" {
		if v, err := strconv.Atoi(hp); err == nil && v > 0 && v < 65536 {
			appSetting.ServerSetting.HttpPort = v
			logger.Infof("HttpPort overridden by VEGA_HTTP_PORT=%d", v)
		}
	}

	// Debugging script (such as issue382) : Disable crypto when no key is mounted to avoid reading /opt/... Failure
	if v := strings.TrimSpace(os.Getenv("VEGA_CRYPTO_DISABLED")); strings.EqualFold(v, "1") || strings.EqualFold(v, "true") {
		appSetting.CryptoSetting.Enabled = false
		logger.Info("Crypto disabled via VEGA_CRYPTO_DISABLED env")
	}

	// Loading time zone
	loc, err := time.LoadLocation(os.Getenv("TZ"))
	if err != nil {
		loc = time.Local
		logger.Warnf("WARNING: Failed to load timezone from env, using Local[%v] as default. Error: %v\n", time.Local, err)
	}
	APP_LOCATION = loc

	if err := loadCryptoKeys(); err != nil {
		logger.Fatalf("Failed to load crypto keys: %s\n", err)
	}

	SetLogSetting(appSetting.LogSetting)

	SetDBSetting()
	overrideDBSettingFromEnv()

	SetMQSetting()

	SetOpenSearchSetting()

	SetKafkaConnectSetting()

	SetHydraAdminSetting()

	SetPermissionSetting()

	SetUserMgmtSetting()

	SetModelFactoryManagerSetting()

	SetModelFactoryAPISetting()

	SetBknAgentSetting()

	appSetting.OtelSetting.ServiceName = version.ServerName
	appSetting.OtelSetting.ServiceVersion = version.ServerVersion
	logger.Infof("ServerName: %s, ServerVersion: %s, Language: %s, GoVersion: %s, GoArch: %s",
		version.ServerName, version.ServerVersion, version.LanguageGo,
		version.GoVersion, version.GoArch)

	s, _ := sonic.MarshalString(appSetting)
	logger.Debug(s)
	logger.Infof("Application settings loaded")
}

func SetDBSetting() {
	setting, ok := appSetting.DepServices[rdsServiceName]
	if !ok {
		logger.Fatalf("service %s not found in depServices", rdsServiceName)
	}

	appSetting.DBSetting = libdb.DBSetting{
		Host:     setting["host"].(string),
		Port:     setting["port"].(int),
		Username: setting["user"].(string),
		Password: setting["password"].(string),
		DBName:   DATA_BASE_NAME,
	}
}

// overrideDBSettingFromEnv for debugging/scripting: Overwrite the DB connection parsed by depServices (such as local 127.0.0.1:3306)
func overrideDBSettingFromEnv() {
	if h := strings.TrimSpace(os.Getenv("VEGA_DB_HOST")); h != "" {
		appSetting.DBSetting.Host = h
		logger.Infof("DB Host overridden by VEGA_DB_HOST=%s", h)
	}
	if p := strings.TrimSpace(os.Getenv("VEGA_DB_PORT")); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 && v < 65536 {
			appSetting.DBSetting.Port = v
			logger.Infof("DB Port overridden by VEGA_DB_PORT=%d", v)
		}
	}
	if u := strings.TrimSpace(os.Getenv("VEGA_DB_USER")); u != "" {
		appSetting.DBSetting.Username = u
		logger.Infof("DB Username overridden by VEGA_DB_USER")
	}
	if pw, ok := os.LookupEnv("VEGA_DB_PASSWORD"); ok {
		appSetting.DBSetting.Password = pw
		logger.Info("DB Password overridden by VEGA_DB_PASSWORD")
	}
	if db := strings.TrimSpace(os.Getenv("VEGA_DB_NAME")); db != "" {
		appSetting.DBSetting.DBName = db
		logger.Infof("DB Name overridden by VEGA_DB_NAME=%s", db)
	}
}

func SetMQSetting() {
	setting, ok := appSetting.DepServices[mqServiceName]
	if !ok {
		logger.Fatalf("service %s not found in depServices", mqServiceName)
	}
	authSetting, ok := setting["auth"].(map[string]any)
	if !ok {
		logger.Fatalf("service %s auth not found in depServices", mqServiceName)
	}

	appSetting.MQSetting = libmq.MQSetting{
		MQType: setting["mqtype"].(string),
		MQHost: setting["mqhost"].(string),
		MQPort: setting["mqport"].(int),
		Tenant: setting["tenant"].(string),
		Auth: libmq.MQAuthSetting{
			Username:  authSetting["username"].(string),
			Password:  authSetting["password"].(string),
			Mechanism: authSetting["mechanism"].(string),
		},
	}
}

func SetOpenSearchSetting() {
	setting, ok := appSetting.DepServices[opensearchServiceName]
	if !ok {
		logger.Fatalf("service %s not found in depServices", opensearchServiceName)
	}

	appSetting.OpenSearchSetting = rest.OpenSearchClientConfig{
		Host:     setting["host"].(string),
		Port:     setting["port"].(int),
		Protocol: setting["protocol"].(string),
		Username: setting["user"].(string),
		Password: setting["password"].(string),
	}
}

// GetAuthEnabled returns whether authentication is enabled.
// AUTH_ENABLED defaults to true and only explicit false values disable authentication.
func GetAuthEnabled() bool {
	envVal := os.Getenv("AUTH_ENABLED")
	// Disable authentication only when it is explicitly set to false or 0
	return envVal != "false" && envVal != "0"
}

func SetHydraAdminSetting() {
	if !GetAuthEnabled() {
		logger.Info("ISF authentication disabled via AUTH_ENABLED env, skipping hydra-admin configuration")
		return
	}
	setting, ok := appSetting.DepServices[hydraAdminServiceName]
	if !ok {
		logger.Fatalf("service %s not found in depServices", hydraAdminServiceName)
	}
	appSetting.HydraAdminSetting = hydra.HydraAdminSetting{
		HydraAdminProcotol: setting["protocol"].(string),
		HydraAdminHost:     setting["host"].(string),
		HydraAdminPort:     setting["port"].(int),
	}
}

func SetPermissionSetting() {
	if !GetAuthEnabled() {
		logger.Info("ISF authentication disabled via AUTH_ENABLED env, skipping authorization configuration")
		return
	}
	setting, ok := appSetting.DepServices[permissionServiceName]
	if !ok {
		logger.Fatalf("service %s not found in depServices", permissionServiceName)
	}

	protocol := setting["protocol"].(string)
	host := setting["host"].(string)
	port := setting["port"].(int)

	appSetting.PermissionUrl = fmt.Sprintf("%s://%s:%d/api/authorization/v1", protocol, host, port)
}

func SetUserMgmtSetting() {
	if !GetAuthEnabled() {
		logger.Info("ISF authentication disabled via AUTH_ENABLED env, skipping user-management configuration")
		return
	}
	setting, ok := appSetting.DepServices[userMgmtServiceName]
	if !ok {
		logger.Fatalf("service %s not found in depServices", userMgmtServiceName)
	}

	protocol := setting["protocol"].(string)
	host := setting["host"].(string)
	port := setting["port"].(int)

	appSetting.UserMgmtUrl = fmt.Sprintf("%s://%s:%d", protocol, host, port)
}

// loadCryptoKeys loads RSA keys from files
func loadCryptoKeys() error {
	if !appSetting.CryptoSetting.Enabled {
		return nil
	}

	if appSetting.CryptoSetting.PrivateKeyPath == "" {
		return fmt.Errorf("privateKeyPath is required when crypto is enabled")
	}
	if appSetting.CryptoSetting.PublicKeyPath == "" {
		return fmt.Errorf("publicKeyPath is required when crypto is enabled")
	}

	privateKeyContent, err := os.ReadFile(appSetting.CryptoSetting.PrivateKeyPath)
	if err != nil {
		return fmt.Errorf("failed to read private key file: %w", err)
	}
	appSetting.CryptoSetting.PrivateKey = string(privateKeyContent)

	publicKeyContent, err := os.ReadFile(appSetting.CryptoSetting.PublicKeyPath)
	if err != nil {
		return fmt.Errorf("failed to read public key file: %w", err)
	}
	appSetting.CryptoSetting.PublicKey = string(publicKeyContent)

	return nil
}

func SetKafkaConnectSetting() {
	setting, ok := appSetting.DepServices[kafkaConnectServiceName]
	if !ok {
		logger.Fatalf("service %s not found in depServices", kafkaConnectServiceName)
	}

	appSetting.KafkaConnectSetting = KafkaConnectSetting{
		Host:     setting["host"].(string),
		Port:     setting["port"].(int),
		Protocol: setting["protocol"].(string),
	}
}

func SetModelFactoryManagerSetting() {
	setting, ok := appSetting.DepServices[modelFactoryManagerServiceName]
	if !ok {
		logger.Fatalf("service %s not found in depServices", modelFactoryManagerServiceName)
	}

	protocol := setting["protocol"].(string)
	host := setting["host"].(string)
	port := setting["port"].(int)

	appSetting.ModelFactoryManagerUrl = fmt.Sprintf("%s://%s:%d/api/private/mf-model-manager/v1", protocol, host, port)
}

func SetModelFactoryAPISetting() {
	setting, ok := appSetting.DepServices[modelFactoryAPIServiceName]
	if !ok {
		logger.Fatalf("service %s not found in depServices", modelFactoryAPIServiceName)
	}

	protocol := setting["protocol"].(string)
	host := setting["host"].(string)
	port := setting["port"].(int)

	appSetting.ModelFactoryAPIUrl = fmt.Sprintf("%s://%s:%d/api/private/mf-model-api/v1", protocol, host, port)
}

func SetBknAgentSetting() {
	setting, ok := appSetting.DepServices[bknAgentServiceName]
	if !ok {
		return
	}

	protocol := setting["protocol"].(string)
	host := setting["host"].(string)
	port := setting["port"].(int)

	appSetting.BknAgentUrl = fmt.Sprintf("%s://%s:%d", protocol, host, port)
}
