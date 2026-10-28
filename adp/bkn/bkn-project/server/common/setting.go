// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package common

import (
	"fmt"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel"
	"github.com/spf13/viper"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/version"
)

// ServerSetting contains server configuration.
type ServerSetting struct {
	RunMode      string        `mapstructure:"runMode"`
	HttpPort     int           `mapstructure:"httpPort"`
	Language     string        `mapstructure:"language"`
	ReadTimeOut  time.Duration `mapstructure:"readTimeOut"`
	WriteTimeout time.Duration `mapstructure:"writeTimeOut"`
}

// GitStoreSetting locates the built-in git store. Each release lineage keeps
// one bare repository under RootDir, which the Helm chart backs with a
// persistent volume.
// DraftSetting holds the rules of drafting an ontology from table structure.
type DraftSetting struct {
	// A code table, drafted as an enum, has a key, a name field, at most CodeTableMaxRows rows and
	// at most CodeTableMaxColumns fields besides its key.
	CodeTableMaxRows    int64 `mapstructure:"codeTableMaxRows"`
	CodeTableMaxColumns int   `mapstructure:"codeTableMaxColumns"`
	// A relation whose full-table hit rate reaches AdoptHitRate is adopted; one inferred from
	// field names below SuggestHitRate is not proposed.
	AdoptHitRate   float64 `mapstructure:"adoptHitRate"`
	SuggestHitRate float64 `mapstructure:"suggestHitRate"`
	// PrivacyKeywords match field names and comments, case-insensitively; matching fields are excluded.
	PrivacyKeywords []string `mapstructure:"privacyKeywords"`
	// NameFieldKeywords name the fields that hold a record's name, matched as a field name or its end.
	NameFieldKeywords []string `mapstructure:"nameFieldKeywords"`
	// QueryTimeout bounds each count query in seconds; Timeout bounds the whole drafting.
	QueryTimeout int           `mapstructure:"queryTimeout"`
	Timeout      time.Duration `mapstructure:"timeout"`
	// CheckTimeout bounds each attempt of the check after a confirmation.
	CheckTimeout time.Duration `mapstructure:"checkTimeout"`
}

type GitStoreSetting struct {
	RootDir string `mapstructure:"rootDir"`
}

// AppSetting contains application configuration.
type AppSetting struct {
	ServerSetting   ServerSetting             `mapstructure:"server"`
	LogSetting      logger.LogSetting         `mapstructure:"log"`
	OtelSetting     otel.OtelConfig           `mapstructure:"otel"`
	GitStoreSetting GitStoreSetting           `mapstructure:"gitStore"`
	DraftSetting    DraftSetting              `mapstructure:"draft"`
	DepServices     map[string]map[string]any `mapstructure:"depServices"`

	DBSetting         libdb.DBSetting
	HydraAdminSetting hydra.HydraAdminSetting

	// BknBackendUrl is the bkn-backend public API base, e.g.
	// {scheme}://{host}:{port}/api/bkn-backend/v1. BKN import and export
	// (/bkns) are only served under this prefix.
	BknBackendUrl string

	// ExecutionFactoryUrl is the agent-operator-integration public API base, e.g.
	// {scheme}://{host}:{port}/api/agent-operator-integration/v1. Toolbox impex
	// and Skill registration are only served under this prefix, with the caller's token.
	ExecutionFactoryUrl string

	// VegaBackendUrl is the vega-backend internal API base, e.g.
	// http://vega-backend-svc:13014/api/vega-backend/in/v1. Internal routes take the
	// caller's account in headers, and vega-backend applies the caller's permissions.
	VegaBackendUrl string
}

const (
	// ConfigFile contains configuration-file information.
	configPath string = "./config/"
	configName string = "bkn-project-config"
	configType string = "yaml"

	rdsServiceName           string = "rds"
	hydraAdminServiceName    string = "hydra-admin"
	bknBackendServiceName    string = "bkn-backend"
	agentOperatorServiceName string = "agent-operator-integration"
	vegaBackendServiceName   string = "vega-backend"

	// DefaultGitStoreRootDir is the git store root when the configuration does not name one.
	DefaultGitStoreRootDir string = "/data/git"

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
	vp.SetDefault("gitStore.rootDir", DefaultGitStoreRootDir)
	vp.SetDefault("draft.codeTableMaxRows", 500)
	vp.SetDefault("draft.codeTableMaxColumns", 3)
	vp.SetDefault("draft.adoptHitRate", 0.95)
	vp.SetDefault("draft.suggestHitRate", 0.8)
	vp.SetDefault("draft.privacyKeywords", DefaultPrivacyKeywords)
	vp.SetDefault("draft.nameFieldKeywords", DefaultNameFieldKeywords)
	vp.SetDefault("draft.queryTimeout", 60)
	vp.SetDefault("draft.timeout", 10*time.Minute)
	vp.SetDefault("draft.checkTimeout", 10*time.Minute)

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

	SetHydraAdminSetting()

	SetBknBackendSetting()

	SetExecutionFactorySetting()

	SetVegaBackendSetting()

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

func SetHydraAdminSetting() {
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

func SetBknBackendSetting() {
	setting, ok := appSetting.DepServices[bknBackendServiceName]
	if !ok {
		logger.Fatalf("service %s not found in depServices", bknBackendServiceName)
	}

	protocol := setting["protocol"].(string)
	host := setting["host"].(string)
	port := setting["port"].(int)

	appSetting.BknBackendUrl = fmt.Sprintf("%s://%s:%d/api/bkn-backend/v1", protocol, host, port)
}

func SetExecutionFactorySetting() {
	setting, ok := appSetting.DepServices[agentOperatorServiceName]
	if !ok {
		logger.Fatalf("service %s not found in depServices", agentOperatorServiceName)
	}

	protocol := setting["protocol"].(string)
	host := setting["host"].(string)
	port := setting["port"].(int)

	appSetting.ExecutionFactoryUrl = fmt.Sprintf("%s://%s:%d/api/agent-operator-integration/v1", protocol, host, port)
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

// DefaultPrivacyKeywords and DefaultNameFieldKeywords are the fallbacks when the configuration
// gives none. They are business vocabulary, so the shipped configuration carries the full
// lists, Chinese included; the code keeps only the English ones.
var DefaultPrivacyKeywords = []string{
	"full_name", "first_name", "last_name", "phone", "mobile", "telephone",
	"id_card", "id_number", "passport", "email", "address", "bank_card", "card_number", "birth",
}

var DefaultNameFieldKeywords = []string{"name", "label", "title"}
