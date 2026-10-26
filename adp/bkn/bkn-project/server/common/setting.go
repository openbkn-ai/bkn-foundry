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
type GitStoreSetting struct {
	RootDir string `mapstructure:"rootDir"`
}

// AppSetting contains application configuration.
type AppSetting struct {
	ServerSetting   ServerSetting             `mapstructure:"server"`
	LogSetting      logger.LogSetting         `mapstructure:"log"`
	OtelSetting     otel.OtelConfig           `mapstructure:"otel"`
	GitStoreSetting GitStoreSetting           `mapstructure:"gitStore"`
	DepServices     map[string]map[string]any `mapstructure:"depServices"`

	DBSetting         libdb.DBSetting
	HydraAdminSetting hydra.HydraAdminSetting

	// BknBackendUrl is the bkn-backend public API base, e.g.
	// {scheme}://{host}:{port}/api/bkn-backend/v1. BKN import and export
	// (/bkns) are only served under this prefix.
	BknBackendUrl string
}

const (
	// ConfigFile contains configuration-file information.
	configPath string = "./config/"
	configName string = "bkn-project-config"
	configType string = "yaml"

	rdsServiceName        string = "rds"
	hydraAdminServiceName string = "hydra-admin"
	bknBackendServiceName string = "bkn-backend"

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
