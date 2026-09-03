// Package config defines configuration.
// @file config.go
// @description: define configuration.
package config

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/creasty/defaults"
	"github.com/go-playground/validator/v10"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/telemetry"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
	bknotel "github.com/openbkn-ai/bkn-foundry/comm-go/otel"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

const (
	DefaultOperatorNameMaxLength  = 50 // Maximum length of operator name.
	DefaultOperatorHistoryRecords = 10 // Maximum number of operator history records retained.
)

// Config configuration.
type Config struct {
	Project                 Project                   `yaml:"project"`
	OAuth                   OAuthConfig               `yaml:"oauth"`
	DB                      DBConfig                  `yaml:"db"`
	OperatorConfig          OperatorConfig            `yaml:"operator"`
	Logger                  interfaces.Logger         `yaml:"-"`
	RedisConfig             RedisConfig               `yaml:"redis"`
	ProxyModuleConfig       ProxyModuleConfig         `yaml:"proxy_module"`
	MCPConfig               MCPConfig                 `yaml:"mcp"`
	CategoryConfig          CategoryConfig            `yaml:"category"`
	MQConfigFile            string                    `yaml:"-"`
	Observability           ObservabilityConfig       `yaml:"-"`
	OTelProviders           *bknotel.Providers        `yaml:"-"`
	SandboxControlPlane     SandboxControlPlaneConfig `yaml:"sandbox-control-plane"`
	MFModelAPI              PrivateBaseConfig         `yaml:"mf-model-api"`
	MFModelManager          PrivateBaseConfig         `yaml:"mf-model-manager"`
	VegaBackend             PrivateBaseConfig         `yaml:"vega-backend"`
	AIGenerationConfig      AIGenerationConfig        `yaml:"ai_generation_config"`
	OSSGatewayBackendConfig OSSGatewayBackendConfig   `yaml:"oss-gateway-backend"`
	SkillIndexBuildConfig   SkillIndexBuildConfig     `yaml:"skill_index_build"`
}

type SkillIndexBuildConfig struct {
	EnablePeriodicFullScan   bool   `yaml:"enable_periodic_full_scan" default:"false"`
	PeriodicFullScanInterval string `yaml:"periodic_full_scan_interval" default:"168h"`
	EnableTaskCleanup        bool   `yaml:"enable_task_cleanup" default:"false"`
	TaskRetention            string `yaml:"task_retention" default:"720h"`
}

// OSSGatewayBackendConfig OSS gateway backend configuration.
type OSSGatewayBackendConfig struct {
	PrivateBaseConfig `yaml:",inline"`
	StorageID         string `yaml:"storage_id"`
	InternalRequest   bool   `yaml:"internal_request" default:"false"`
	Expires           int64  `yaml:"expires" default:"3600"` // Unit (second)
}

// SandboxControlPlaneConfig sandbox control service configuration.
type SandboxControlPlaneConfig struct {
	PrivateBaseConfig `yaml:",inline"`
	// Template ID.
	TemplateID string `yaml:"template_id" default:"python-basic"`
	// Session resource configuration.
	SessionResources SessionResourcesConfig `yaml:"session_resources"`
	// Maximum number of sessions, default 3.
	MaxSessions int `yaml:"max_sessions" default:"3"`
	// Number of active sessions, default 1.
	ActiveSessions int `yaml:"active_sessions" default:"1"`
	// The maximum number of tasks to be executed concurrently in a single session, default 1.
	MaxConcurrentTasks int `yaml:"max_concurrent_tasks" default:"100"`
}

// SessionResourcesConfig session resource configuration.
type SessionResourcesConfig struct {
	CPU     string `yaml:"cpu" default:"1"`        // Number of CPU cores.
	Memory  string `yaml:"memory" default:"512Mi"` // Memory size in Mi, default 512Mi.
	Disk    string `yaml:"disk" default:"1Gi"`     // Disk size in Gi, default 1Gi.
	Timeout int    `yaml:"timeout" default:"3600"` // Session timeout in seconds, default is 1 hour.
}

// AIGenerationConfig intelligent generation configuration.
type AIGenerationConfig struct {
	// python code generation system prompt word ID.
	PythonFunctionGeneratorPromptID string    `yaml:"python_function_generator_prompt_id"` // If empty or not found, the default prompt word is used.
	MetadataParamGeneratorPromptID  string    `yaml:"metadata_param_generator_prompt_id"`  // If empty or not found, the default prompt word is used.
	LLMConfig                       LLMConfig `yaml:"llm"`
}

// LLMConfig LLM configuration.
type LLMConfig struct {
	Model            string  `yaml:"model"`
	MaxTokens        int     `yaml:"max_tokens" default:"2048"`
	Temperature      float64 `yaml:"temperature" default:"0.1"`
	TopK             int     `yaml:"top_k" default:"40"`
	TopP             float64 `yaml:"top_p" default:"0.9"`
	FrequencyPenalty float64 `yaml:"frequency_penalty" default:"0.0"`
	PresencePenalty  float64 `yaml:"presence_penalty" default:"0.0"`
}

// ObservabilityConfig tracking configuration.
type ObservabilityConfig struct {
	bknotel.OtelConfig `mapstructure:",squash"`

	TraceType                telemetry.ExporterType `mapstructure:"traceType"`
	TraceEnabled             bool                   `mapstructure:"traceEnabled"`
	TraceProvider            string                 `mapstructure:"traceProvider"`
	LogEnabled               bool                   `mapstructure:"logEnabled"`
	HttpTraceFeedIngesterURL string                 `mapstructure:"httpTraceFeedIngesterUrl"`
	GrpcTraceFeedIngesterURL string                 `mapstructure:"grpcTraceFeedIngesterUrl"`
}

// Project configuration.
type Project struct {
	Host        string              `yaml:"host"`
	Port        int                 `yaml:"port"`
	Language    string              `yaml:"language"`
	LoggerLevel int                 `yaml:"logger_level" default:"0"`
	Name        string              `yaml:"name" default:"agent-operator-integration"`
	MachineID   string              `yaml:"machine_id"`
	PodID       string              `yaml:"pod_id" default:"DEFAULT_POD_ID"`
	Debug       bool                `yaml:"debug"`
	CommitInfo  utils.GitCommitInfo `yaml:"-"`
}

// SetMachineID Set machine ID.
func (conf *Project) SetMachineID() {
	// GenerateMachineID.
	if conf.MachineID == "" {
		mid := os.Getenv(conf.PodID)
		if mid == "" {
			mid, _ = os.Hostname()
			// It can also be empty.
			mid = utils.MD5(mid)
			mid = mid[:8]
		}
		conf.MachineID = mid
	}
}

// GetMachineID Gets the machine ID.
func (conf *Project) GetMachineID() string {
	return conf.MachineID
}

// ProxyModuleConfig proxy module configuration information.
type ProxyModuleConfig struct {
	// Agent configuration.
	DefaultTimeout int64 `yaml:"default_timeout" default:"30"` // Unit: seconds.
	MaxTimeout     int64 `yaml:"max_timeout" default:"300"`    // Unit: seconds.
	// Agent pool configuration.
	MaxClients     int   `yaml:"max_clients" default:"50"`      // Maximum number of client connections.
	ClientLifetime int64 `yaml:"client_lifetime" default:"300"` // Unit: seconds.
}

// OperatorConfig operator configuration.
type OperatorConfig struct {
	ImportFileSizeLimit    int64 `yaml:"import_file_size_limit" default:"2097152"  validate:"min=0,max=104857600"` // Default 2MB.
	ImportOperatorMaxCount int64 `yaml:"import_operator_max_count" default:"10" validate:"min=1"`                  // Default 10.
	DescLengthLimit        int64 `yaml:"operator_description_length_limit" default:"255" validate:"min=1"`         // Maximum length of operator description, unit: bytes.
}

// MCPConfig MCP configuration.
type MCPConfig struct {
	ConnTimeout int64 `yaml:"conn_timeout" default:"10"` // Unit: seconds.

	// MaxInstances controls the maximum number of running MCP instances retained within the process:
	// - <=0: No limit (not recommended, it will occupy more memory when the number of instances is large)
	// - >0: After exceeding the limit, press LRU to eliminate the instance that has not been accessed for the longest time (instances with active SSE/Stream connections will not be eliminated)
	MaxInstances int `yaml:"max_instances" default:"200"`

	// InstanceTTL controls the expiration cleanup threshold of an instance (by last access time):
	// - <=0: disable TTL.
	// - >0: lastAccess can be cleaned if it exceeds this threshold (instances with active SSE/Stream connections are not cleaned)
	InstanceTTL int64 `yaml:"instance_ttl" default:"1800"` // Unit: seconds.

	// CleanupInterval scheduled cleaning cycle:
	// - <=0: disable scheduled cleaning.
	// - >0: Periodically scan and clean up expired instances (valid only when InstanceTTL>0)
	CleanupInterval int64 `yaml:"cleanup_interval" default:"60"` // Unit: seconds.
}

// CategoryConfig operator classification configuration.
type CategoryConfig struct {
	InitSwitch bool `yaml:"init_switch"` // Whether to initialize operator classification.
}

// DBConfig database configuration.
type DBConfig struct {
	Host         string `yaml:"host"`
	Port         int    `yaml:"port"`
	UserName     string `yaml:"user_name"`
	Password     string `yaml:"password"`
	ConnTimeout  int    `yaml:"conn_timeout"`
	MaxOpenConns int    `yaml:"max_open_conns"`
	ReadTimeout  int    `yaml:"read_timeout"`
	WriteTimeout int    `yaml:"write_timeout"`
	CertFile     string `yaml:"cert_file"`
	KeyFile      string `yaml:"key_file"`
	DBName       string `yaml:"db_name"`
	Charset      string `yaml:"charset"`
	SystemID     string `yaml:"system_id"`
}

// GetDBName Gets the database name.
func (conf *Config) GetDBName() string {
	if conf.DB.DBName == "" {
		conf.DB.DBName = "dip_data_operator_hub"
	}
	if conf.DB.SystemID == "" {
		return conf.DB.DBName
	}
	return fmt.Sprintf("%s%s", conf.DB.SystemID, conf.DB.DBName)
}

// GetLogger Get Logger.
func (conf *Config) GetLogger() interfaces.Logger {
	if conf.Logger == nil {
		return logger.DefaultLogger()
	}
	return conf.Logger
}

// OAuthConfig OAuth connection information.
type OAuthConfig struct {
	PublicBaseConfig `yaml:",inline"`
	AdminHost        string `yaml:"admin_host"`
	AdminPort        int    `yaml:"admin_port"`
	AdminProtocol    string `yaml:"admin_protocol"`
	AdminPrefix      string `yaml:"admin_prefix"`
}

// PublicBaseConfig public basic configuration.
type PublicBaseConfig struct {
	PublicHost     string `yaml:"public_host"`
	PublicPort     int    `yaml:"public_port"`
	PublicProtocol string `yaml:"public_protocol"`
}

// PrivateBaseConfig private basic configuration.
type PrivateBaseConfig struct {
	PrivateHost     string `yaml:"private_host"`
	PrivatePort     int    `yaml:"private_port"`
	PrivateProtocol string `yaml:"private_protocol"`
}

var (
	once         sync.Once
	configLoader *Config
)

// NewConfigLoader Get configuration.
func NewConfigLoader() *Config {
	once.Do(func() {
		profileDir := os.Getenv("CONFIG_PROFILE")
		var configFilePath, secretFilePath, mqConfigFilePath string
		if profileDir == "" {
			configFilePath = "/sysvol/config/agent-operator-integration.yaml"
			secretFilePath = "/sysvol/secret/agent-operator-integration-secret.yaml"
			mqConfigFilePath = "/sysvol/config/mq_config.yaml"
		} else {
			configFilePath = filepath.Join(profileDir, "agent-operator-integration.yaml")
			secretFilePath = filepath.Join(profileDir, "agent-operator-integration-secret.yaml")
			mqConfigFilePath = filepath.Join(profileDir, "mq_config.yaml")
		}
		// Set default configuration.
		configLoader = &Config{
			MQConfigFile: mqConfigFilePath,
		}
		err := configLoader.localConfig(configFilePath)
		if err != nil {
			log.Panicln("Error: load local config failed: ", err)
			return
		}
		err = configLoader.localConfig(secretFilePath)
		if err != nil {
			log.Panicln("Error: load local secret failed: ", err)
			return
		}
		overrideWithEnv(configLoader)
		// Add verification validator.
		err = validator.New().Struct(configLoader)
		if err != nil {
			log.Panicln("Error: validate config failed: ", err)
			return
		}
		// Initialize observability related configurations.
		configLoader.initO11yAndLog()
		// Set machine ID.
		configLoader.Project.SetMachineID()
	})
	return configLoader
}

func (conf *Config) localConfig(path string) (err error) {
	file, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return
	}
	err = yaml.Unmarshal(file, conf)
	if err != nil {
		return
	}
	err = defaults.Set(conf)
	return
}

// overrideWithEnv automatically traverses the structure and uses reflection to override environment variables based on tags.
func overrideWithEnv(cfg interface{}) {
	v := reflect.ValueOf(cfg).Elem() // Get pointer to structure.
	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)

		if field.Kind() == reflect.Struct {
			// Recursively process nested structures.
			overrideWithEnv(field.Addr().Interface())
			continue
		}

		// Get the env tag of a field.
		envTag := fieldType.Tag.Get("env")
		if envTag == "" {
			continue // If env tag is not defined, skip.
		}

		// Determine whether environment variables exist.
		envValue, exists := os.LookupEnv(envTag)
		if !exists {
			continue // If the environment variable key does not exist, skip.
		}

		// If key exists but the value is null, set the field to the zero value of type.
		if envValue == "" {
			field.Set(reflect.Zero(field.Type()))
			continue
		}

		// Use reflection to set field values directly, requiring type matching.
		switch field.Kind() { //nolint:exhaustive
		case reflect.String:
			field.SetString(envValue)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			intValue, err := strconv.ParseInt(envValue, 10, 64)
			if err == nil {
				field.SetInt(intValue)
			}
		case reflect.Bool:
			boolValue, err := strconv.ParseBool(envValue)
			if err == nil {
				field.SetBool(boolValue)
			}
		default:
			panic("Unsupported field type for env override")
		}
	}
}

// Load & initialize observability related configurations.
func (conf *Config) initO11yAndLog() {
	// Initialization log.
	level := logger.Level(configLoader.Project.LoggerLevel)
	if configLoader.Project.Debug {
		level = logger.LevelDebug
	}

	// Load configuration file.
	viper.SetConfigName("observability")
	viper.SetConfigType("yaml")
	profileDir := os.Getenv("CONFIG_PROFILE")
	if profileDir == "" {
		profileDir = "/sysvol/config"
	}
	viper.AddConfigPath(profileDir)
	if err := viper.ReadInConfig(); err != nil {
		panic(err)
	}
	if err := viper.Unmarshal(&conf.Observability); err != nil {
		panic(err)
	}

	otelConfig := conf.Observability.toOTelConfig(conf.Project.Name)
	providers, err := bknotel.InitOTel(context.Background(), &otelConfig)
	if err != nil {
		panic(err)
	}
	conf.Observability.OtelConfig = otelConfig
	conf.OTelProviders = providers

	// Initialization log.
	if otelConfig.Log.Enabled {
		configLoader.Logger = telemetry.NewSamplerLogger(logger.NewLogger(level, logger.MaxCalldepth))
		return
	}
	configLoader.Logger = logger.NewLogger(level, logger.DefaultCalldepth)
}

func (conf ObservabilityConfig) toOTelConfig(serviceName string) bknotel.OtelConfig {
	otelConfig := conf.OtelConfig
	if otelConfig.ServiceName == "" {
		otelConfig.ServiceName = serviceName
	}
	if otelConfig.ServiceVersion == "" {
		otelConfig.ServiceVersion = "1.0.0"
	}
	if otelConfig.Environment == "" {
		otelConfig.Environment = "production"
	}
	if otelConfig.OTLPEndpoint == "" {
		otelConfig.OTLPEndpoint = conf.otelEndpoint()
	}
	if conf.TraceEnabled {
		otelConfig.Trace.Enabled = true
	}
	if conf.LogEnabled {
		otelConfig.Log.Enabled = true
	}
	otelConfig.SetDefaults(otelConfig.ServiceName, otelConfig.ServiceVersion)
	return otelConfig
}

func (conf ObservabilityConfig) otelEndpoint() string {
	if conf.TraceProvider == "http" && conf.HttpTraceFeedIngesterURL != "" {
		return strings.TrimPrefix(strings.TrimPrefix(conf.HttpTraceFeedIngesterURL, "http://"), "https://")
	}
	if conf.GrpcTraceFeedIngesterURL != "" {
		return conf.GrpcTraceFeedIngesterURL
	}
	return ""
}

// GetAuthEnabled returns whether authentication and authorization are enabled.
// Only explicit false/0 disables the feature; default is enabled.
func GetAuthEnabled() bool {
	envVal := os.Getenv("AUTH_ENABLED")
	return envVal != "false" && envVal != "0"
}
