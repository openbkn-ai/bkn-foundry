// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	libmq "github.com/openbkn-ai/bkn-foundry/comm-go/mq"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/spf13/viper"

	"bkn-backend/version"
)

// ServerSetting contains server configuration.
type ServerSetting struct {
	RunMode                  string        `mapstructure:"runMode"`
	HttpPort                 int           `mapstructure:"httpPort"`
	Language                 string        `mapstructure:"language"`
	ReadTimeOut              time.Duration `mapstructure:"readTimeOut"`
	WriteTimeout             time.Duration `mapstructure:"writeTimeOut"`
	DefaultSmallModelName    string        `mapstructure:"defaultSmallModelName"`
	DefaultSmallModelEnabled bool          `mapstructure:"defaultSmallModelEnabled"`
	// Schedule worker settings
	SchedulePollInterval int `mapstructure:"schedulePollInterval"` // in seconds, default 10
	ScheduleLockTimeout  int `mapstructure:"scheduleLockTimeout"`  // in seconds, default 300 (5 min)
}

// AppSetting contains application configuration.
type AppSetting struct {
	ServerSetting ServerSetting             `mapstructure:"server"`
	LogSetting    logger.LogSetting         `mapstructure:"log"`
	OtelSetting   otel.OtelConfig           `mapstructure:"otel"`
	DepServices   map[string]map[string]any `mapstructure:"depServices"`

	DBSetting         libdb.DBSetting
	MQSetting         libmq.MQSetting
	OpenSearchSetting rest.OpenSearchClientConfig
	HydraAdminSetting hydra.HydraAdminSetting

	// model factory url
	ModelFactoryManagerUrl string
	// model factory api url
	ModelFactoryAPIUrl string
	// ontology query url
	OntologyQueryUrl string
	// vega backend url
	VegaBackendUrl string
	// AgentOperatorUrl is the single agent-operator-integration internal-v1 base, e.g.
	// {scheme}://{host}:{port}/api/agent-operator-integration/internal-v1
	// (A trailing /operator suffix is accepted for backward compatibility and normalized away.)
	AgentOperatorUrl string
}

const (
	// ConfigFile contains configuration-file information.
	configPath string = "./config/"
	configName string = "bkn-backend-config"
	configType string = "yaml"

	rdsServiceName                 string = "rds"
	mqServiceName                  string = "mq"
	opensearchServiceName          string = "opensearch"
	hydraAdminServiceName          string = "hydra-admin"
	modelFactoryManagerServiceName string = "mf-model-manager"
	modelFactoryAPIServiceName     string = "mf-model-api"
	ontologyQueryServiceName       string = "ontology-query"
	vegaBackendServiceName         string = "vega-backend"
	agentOperatorServiceName       string = "agent-operator-integration"

	DATA_BASE_NAME string = "openbkn"
)

var (
	appSetting *AppSetting
	vp         *viper.Viper

	settingOnce sync.Once
)

// NewSetting reads service configuration.
func NewSetting() *AppSetting {
	settingOnce.Do(func() {
		appSetting = &AppSetting{}
		vp = viper.New()
		initSetting(vp)
	})

	return appSetting
}

// Initialize configuration.
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

// Read the configuration file.
func loadSetting(vp *viper.Viper) {
	logger.Infof("Load Setting File %s%s.%s", configPath, configName, configType)

	if err := vp.ReadInConfig(); err != nil {
		logger.Fatalf("err:%s\n", err)
	}

	if err := vp.Unmarshal(appSetting); err != nil {
		logger.Fatalf("err:%s\n", err)
	}

	SetLogSetting(appSetting.LogSetting)

	SetDBSetting()

	SetMQSetting()

	SetOpenSearchSetting()

	SetHydraAdminSetting()

	SetModelFactoryManagerSetting()

	SetModelFactoryAPISetting()

	SetOntologyQuerySetting()

	SetVegaBackendSetting()

	SetAgentOperatorSetting()

	appSetting.OtelSetting.ServiceName = version.ServerName
	appSetting.OtelSetting.ServiceVersion = version.ServerVersion
	logger.Infof("ServerName: %s, ServerVersion: %s, Language: %s, GoVersion: %s, GoArch: %s",
		version.ServerName, version.ServerVersion, version.LanguageGo,
		version.GoVersion, version.GoArch)

	logger.Debug("Application settings loaded")
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
// It is controlled by AUTH_ENABLED and defaults to true for security.
func GetAuthEnabled() bool {
	envVal := os.Getenv("AUTH_ENABLED")
	// Disable authentication only when explicitly set to false or 0.
	return envVal != "false" && envVal != "0"
}

// GetActionExecutionPEPEnabled reports whether the complete action execution
// authorization chain is enabled. It defaults to false until migration and
// cross-service validation are complete.
func GetActionExecutionPEPEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("ACTION_EXECUTION_PEP_ENABLED")))
	return value == "true" || value == "1"
}

func SetHydraAdminSetting() {
	if !GetAuthEnabled() {
		logger.Info("Authentication disabled via AUTH_ENABLED env, skipping hydra-admin configuration")
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

func SetOntologyQuerySetting() {
	setting, ok := appSetting.DepServices[ontologyQueryServiceName]
	if !ok {
		// Optional service, default to localhost for development
		logger.Warnf("service %s not found in depServices, using default", ontologyQueryServiceName)
		appSetting.OntologyQueryUrl = "http://localhost:8080"
		return
	}

	protocol := setting["protocol"].(string)
	host := setting["host"].(string)
	port := setting["port"].(int)

	appSetting.OntologyQueryUrl = fmt.Sprintf("%s://%s:%d", protocol, host, port)
}

func SetVegaBackendSetting() {
	setting, ok := appSetting.DepServices[vegaBackendServiceName]
	if !ok {
		logger.Fatalf("service %s not found in depServices", vegaBackendServiceName)
	}

	protocol := setting["protocol"].(string)
	host := setting["host"].(string)
	port := setting["port"].(int)

	appSetting.VegaBackendUrl = fmt.Sprintf("%s://%s:%d/api/vega-backend/in/v1", protocol, host, port)
}

func SetAgentOperatorSetting() {
	setting, ok := appSetting.DepServices[agentOperatorServiceName]
	if !ok {
		logger.Fatalf("service %s not found in depServices", agentOperatorServiceName)
	}

	protocol := setting["protocol"].(string)
	host := setting["host"].(string)
	port := setting["port"].(int)

	appSetting.AgentOperatorUrl = fmt.Sprintf("%s://%s:%d/api/agent-operator-integration/internal-v1", protocol, host, port)
}
