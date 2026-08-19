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
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/spf13/viper"

	"ontology-query/version"
)

// ServerSetting contains server configuration.
type ServerSetting struct {
	RunMode                  string        `mapstructure:"runMode"`
	HttpPort                 int           `mapstructure:"httpPort"`
	Language                 string        `mapstructure:"language"`
	ReadTimeOut              time.Duration `mapstructure:"readTimeOut"`
	WriteTimeout             time.Duration `mapstructure:"writeTimeOut"`
	ViewDataTimeout          string        `mapstructure:"viewDataTimeout"`
	DefaultSmallModelEnabled bool          `mapstructure:"defaultSmallModelEnabled"`
	// FilteredCrossJoinMaxEdgeExpand caps virtual edge expansion per filtered_cross_join step in subgraph BFS (silent truncate). 0 uses default in code.
	FilteredCrossJoinMaxEdgeExpand int `mapstructure:"filteredCrossJoinMaxEdgeExpand"`
}

// AppSetting contains application configuration.
type AppSetting struct {
	ServerSetting ServerSetting             `mapstructure:"server"`
	LogSetting    logger.LogSetting         `mapstructure:"log"`
	OtelSetting   otel.OtelConfig           `mapstructure:"otel"`
	DepServices   map[string]map[string]any `mapstructure:"depServices"`

	DBSetting         libdb.DBSetting
	OpenSearchSetting rest.OpenSearchClientConfig
	HydraAdminSetting hydra.HydraAdminSetting

	BKNBackendUrl  string
	VegaBackendUrl string
	// Operator execution URL.
	AgentOperatorUrl string
	// Toolbox execution URL.
	ToolBoxUrl string
	// MCP execution URL.
	MCPUrl string
	// model factory url
	ModelFactoryManagerUrl string
	// model factory api url
	ModelFactoryAPIUrl string
}

const (
	// ConfigFile contains configuration-file information.
	configPath string = "./config/"
	configName string = "ontology-query-config"
	configType string = "yaml"

	opensearchServiceName          string = "opensearch"
	rdsServiceName                 string = "rds"
	hydraAdminServiceName          string = "hydra-admin"
	modelFactoryManagerServiceName string = "mf-model-manager"
	modelFactoryAPIServiceName     string = "mf-model-api"
	bknBackendServiceName          string = "bkn-backend"
	vegaBackendServiceName         string = "vega-backend"
	agentOperatorServiceName       string = "agent-operator-integration"
)

var (
	appSetting *AppSetting
	vp         *viper.Viper

	settingOnce sync.Once

	// Current system time zone.
	APP_LOCATION *time.Location
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

	// Load the time zone.
	loc, err := time.LoadLocation(os.Getenv("TZ"))
	if err != nil {
		loc = time.Local
		logger.Warnf("WARNING: Failed to load timezone from env, using Local[%v] as default. Error: %v\n", time.Local, err)
	}
	APP_LOCATION = loc

	SetLogSetting(appSetting.LogSetting)
	if strings.EqualFold(strings.TrimSpace(os.Getenv("BKN_TRACE_OUTBOX_ENABLED")), "true") {
		SetDBSetting()
	}

	SetOpenSearchSetting()
	SetHydraAdminSetting()

	SetBKNBackendSetting()
	SetModelFactoryManagerSetting()

	SetModelFactoryAPISetting()

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
		Host: setting["host"].(string), Port: setting["port"].(int),
		Username: setting["user"].(string), Password: setting["password"].(string), DBName: "openbkn",
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

func SetBKNBackendSetting() {
	setting, ok := appSetting.DepServices[bknBackendServiceName]
	if !ok {
		logger.Fatalf("service %s not found in depServices", bknBackendServiceName)
	}

	protocol := setting["protocol"].(string)
	host := setting["host"].(string)
	port := setting["port"].(int)

	appSetting.BKNBackendUrl = fmt.Sprintf("%s://%s:%d/api/bkn-backend/in/v1/knowledge-networks", protocol, host, port)
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

	appSetting.AgentOperatorUrl = fmt.Sprintf("%s://%s:%d/api/agent-operator-integration/internal-v1/operator", protocol, host, port)
	// ToolBox URL: /api/agent-operator-integration/internal-v1/tool-box/{box_id}/proxy/{tool_id}
	appSetting.ToolBoxUrl = fmt.Sprintf("%s://%s:%d/api/agent-operator-integration/internal-v1/tool-box", protocol, host, port)
	// MCP URL: /api/agent-operator-integration/internal-v1/mcp/proxy/{mcp_id}/tool/call
	appSetting.MCPUrl = fmt.Sprintf("%s://%s:%d/api/agent-operator-integration/internal-v1/mcp", protocol, host, port)
}
