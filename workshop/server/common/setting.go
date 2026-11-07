// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package common

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel"
	"github.com/spf13/viper"

	"github.com/openbkn-ai/bkn-foundry/workshop/server/version"
)

// ServerSetting contains server configuration.
type ServerSetting struct {
	RunMode      string        `mapstructure:"runMode"`
	HttpPort     int           `mapstructure:"httpPort"`
	Language     string        `mapstructure:"language"`
	ReadTimeOut  time.Duration `mapstructure:"readTimeOut"`
	WriteTimeout time.Duration `mapstructure:"writeTimeOut"`
}

// AppSetting contains application configuration.
type AppSetting struct {
	ServerSetting ServerSetting             `mapstructure:"server"`
	LogSetting    logger.LogSetting         `mapstructure:"log"`
	OtelSetting   otel.OtelConfig           `mapstructure:"otel"`
	DepServices   map[string]map[string]any `mapstructure:"depServices"`

	DBSetting         libdb.DBSetting
	HydraAdminSetting hydra.HydraAdminSetting

	// BknSafeUrl is the bkn-safe base URL (BKN_SAFE_URL), e.g. http://bkn-safe:3000.
	BknSafeUrl string
	// BknAgentUrl is the bkn-agent base URL, e.g. http://bkn-agent:30800.
	BknAgentUrl string
	// BknBackendUrl is the bkn-backend base URL, e.g. http://bkn-backend-svc:13014;
	// knowledge network events live there.
	BknBackendUrl string
}

const (
	configPath string = "./config/"
	configName string = "workshop-config"
	configType string = "yaml"

	rdsServiceName        string = "rds"
	hydraAdminServiceName string = "hydra-admin"
	bknAgentServiceName   string = "bkn-agent"
	bknBackendServiceName string = "bkn-backend"

	bknSafeURLEnv string = "BKN_SAFE_URL"

	DATA_BASE_NAME string = "openbkn"
)

var (
	appSetting *AppSetting
	vp         *viper.Viper

	settingOnce sync.Once
)

// NewSetting reads service configuration once.
func NewSetting() *AppSetting {
	settingOnce.Do(func() {
		appSetting = &AppSetting{}
		vp = viper.New()
		initSetting(vp)
	})

	return appSetting
}

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

func loadSetting(vp *viper.Viper) {
	logger.Infof("Load Setting File %s%s.%s", configPath, configName, configType)

	if err := vp.ReadInConfig(); err != nil {
		logger.Fatalf("err:%s\n", err)
	}

	if err := vp.Unmarshal(appSetting); err != nil {
		logger.Fatalf("err:%s\n", err)
	}

	SetLogSetting(appSetting.LogSetting)
	setDBSetting()
	setHydraAdminSetting()
	setBknAgentSetting()
	setBknBackendSetting()

	bknSafeURL, err := NormalizeServiceURL(os.Getenv(bknSafeURLEnv))
	if err != nil {
		logger.Fatalf("Invalid %s: %v", bknSafeURLEnv, err)
	}
	appSetting.BknSafeUrl = bknSafeURL

	appSetting.OtelSetting.ServiceName = version.ServerName
	appSetting.OtelSetting.ServiceVersion = version.ServerVersion
	logger.Infof("ServerName: %s, ServerVersion: %s, Language: %s, GoVersion: %s, GoArch: %s",
		version.ServerName, version.ServerVersion, version.LanguageGo,
		version.GoVersion, version.GoArch)
}

func setDBSetting() {
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

func setHydraAdminSetting() {
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

func setBknAgentSetting() {
	setting, ok := appSetting.DepServices[bknAgentServiceName]
	if !ok {
		logger.Fatalf("service %s not found in depServices", bknAgentServiceName)
	}
	appSetting.BknAgentUrl = fmt.Sprintf("%s://%s:%d",
		setting["protocol"].(string), setting["host"].(string), setting["port"].(int))
}

func setBknBackendSetting() {
	setting, ok := appSetting.DepServices[bknBackendServiceName]
	if !ok {
		logger.Fatalf("service %s not found in depServices", bknBackendServiceName)
	}
	appSetting.BknBackendUrl = fmt.Sprintf("%s://%s:%d",
		setting["protocol"].(string), setting["host"].(string), setting["port"].(int))
}

var (
	errServiceURLRequired = errors.New("a service URL is required")
	errServiceURLInvalid  = errors.New("the service URL must be an absolute HTTP or HTTPS URL with a host and without credentials, query, or fragment")
)

// NormalizeServiceURL requires an absolute HTTP(S) URL with a host and no
// credentials, query or fragment, and strips trailing slashes. Errors never
// echo the value, which may carry secrets when misconfigured.
func NormalizeServiceURL(rawURL string) (string, error) {
	normalized := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	if normalized == "" {
		return "", errServiceURLRequired
	}
	parsed, err := url.Parse(normalized)
	if err != nil || !parsed.IsAbs() || parsed.Hostname() == "" {
		return "", errServiceURLInvalid
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errServiceURLInvalid
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", errServiceURLInvalid
	}
	parsed.Scheme = scheme
	return strings.TrimRight(parsed.String(), "/"), nil
}
