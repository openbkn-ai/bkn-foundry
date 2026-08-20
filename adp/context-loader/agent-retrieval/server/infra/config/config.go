// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package config provides application configuration loading and management.
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
	"sync/atomic"

	"github.com/creasty/defaults"
	bknotel "github.com/openbkn-ai/bkn-foundry/comm-go/otel"
	"github.com/spf13/viper"
	yaml "gopkg.in/yaml.v3"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/telemetry"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/utils"
)

// Config configuration
type Config struct {
	Project              Project                `yaml:"project"`
	OAuth                OAuthConfig            `yaml:"oauth"`
	BknBackend           PrivateBaseConfig      `yaml:"bkn_backend"`
	OntologyQuery        PrivateBaseConfig      `yaml:"ontology_query"`
	Vega                 PrivateBaseConfig      `yaml:"vega"`                 // Vega data-catalog backend (run_sql / resource query)
	OperatorIntegration  PrivateBaseConfig      `yaml:"operator_integration"` // Operator integration service configuration
	BknSafe              PrivateBaseConfig      `yaml:"bkn_safe"`             // bkn-safe auth service (AppKey verification)
	RedisConfig          RedisConfig            `yaml:"redis"`
	Logger               interfaces.Logger      `yaml:"-"`
	ConceptSearchConfig  KnConceptSearchConfig  `yaml:"concept_search_config"`  // Knowledge network concept search configuration
	InstanceSearchConfig KnInstanceSearchConfig `yaml:"instance_search_config"` // Semantic instance retrieval configuration
	Observability        ObservabilityConfig    `yaml:"-"`
	OTelProviders        *bknotel.Providers     `yaml:"-"`
	// New configuration - knowledge rearrangement and retrieval related.
	MFModelAPI PrivateBaseConfig `yaml:"mf_model_api"` // MF-Model API unified service configuration.
	RerankLLM  RerankLLMConfig   `yaml:"rerank_llm"`   // LLM parameter configuration for Rerank.
	FindSkills FindSkillsConfig  `yaml:"find_skills"`  // find_skills Skill recall configuration.
}

// ObservabilityConfig trace configuration
type ObservabilityConfig struct {
	bknotel.OtelConfig `mapstructure:",squash"`

	TraceType                telemetry.ExporterType `mapstructure:"traceType"`
	TraceEnabled             bool                   `mapstructure:"traceEnabled"`
	TraceProvider            string                 `mapstructure:"traceProvider"`
	LogEnabled               bool                   `mapstructure:"logEnabled"`
	HttpTraceFeedIngesterURL string                 `mapstructure:"httpTraceFeedIngesterUrl"`
	GrpcTraceFeedIngesterURL string                 `mapstructure:"grpcTraceFeedIngesterUrl"`
}

// Project configuration
type Project struct {
	Host        string              `yaml:"host"`
	Port        int                 `yaml:"port"`
	Language    string              `yaml:"language"`
	LoggerLevel int                 `yaml:"logger_level"`
	Name        string              `yaml:"name" default:"agent-retrieval"`
	MachineID   string              `yaml:"machine_id"`
	PodID       string              `yaml:"pod_id" default:"DEFAULT_POD_ID"`
	Debug       bool                `yaml:"debug"`
	CommitInfo  utils.GitCommitInfo `yaml:"-"`
}

// GetLogger gets logger
func (conf *Config) GetLogger() interfaces.Logger {
	if conf.Logger == nil {
		return logger.DefaultLogger()
	}
	return conf.Logger
}

// OAuthConfig OAuth connection info
type OAuthConfig struct {
	PublicBaseConfig `yaml:",inline"`
	AdminHost        string `yaml:"admin_host"`
	AdminPort        int    `yaml:"admin_port"`
	AdminProtocol    string `yaml:"admin_protocol"`
	AdminPrefix      string `yaml:"admin_prefix"`
	AdminBasePath    string `yaml:"admin_base_path"`
}

// PublicBaseConfig public base configuration
type PublicBaseConfig struct {
	PublicHost     string `yaml:"public_host"`
	PublicPort     int    `yaml:"public_port"`
	PublicProtocol string `yaml:"public_protocol"`
}

// PrivateBaseConfig private base configuration
type PrivateBaseConfig struct {
	PrivateHost     string `yaml:"private_host"`
	PrivatePort     int    `yaml:"private_port"`
	PrivateProtocol string `yaml:"private_protocol"`
	PrivateBasePath string `yaml:"private_base_path"`
}

func buildServiceURL(protocol, host string, port int, basePath, servicePath string) string {
	var buf strings.Builder
	buf.WriteString(protocol)
	buf.WriteString("://")
	buf.WriteString(host)
	if port != 0 && !((protocol == "https" && port == 443) || (protocol == "http" && port == 80)) {
		fmt.Fprintf(&buf, ":%d", port)
	}
	basePath = strings.TrimRight(basePath, "/")
	if basePath != "" && !strings.HasPrefix(basePath, "/") {
		basePath = "/" + basePath
	}
	buf.WriteString(basePath)
	if servicePath != "" && !strings.HasPrefix(servicePath, "/") {
		servicePath = "/" + servicePath
	}
	buf.WriteString(servicePath)
	return buf.String()
}

// BuildURL builds the full base URL for a private service endpoint.
func (c *PrivateBaseConfig) BuildURL(servicePath string) string {
	return buildServiceURL(c.PrivateProtocol, c.PrivateHost, c.PrivatePort, c.PrivateBasePath, servicePath)
}

// BuildAdminURL builds the full base URL for the OAuth admin endpoint.
func (c *OAuthConfig) BuildAdminURL() string {
	return buildServiceURL(c.AdminProtocol, c.AdminHost, c.AdminPort, c.AdminBasePath, c.AdminPrefix)
}

// OpenSearchConfig OpenSearch configuration
type OpenSearchConfig struct {
	Protocol string `yaml:"protocol"`
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	UserName string `yaml:"user"`
	Password string `yaml:"password"`
}

// KnConceptSearchConfig knowledge network concept search configuration
type KnConceptSearchConfig struct {
	ConceptRecallSize int `yaml:"concept_recall_size"` // Concept rough recall size
	KnnKValue         int `yaml:"knn_k"`               // knn k value
}

// KnInstanceSearchConfig holds the deployment-calibrated knobs of semantic instance retrieval.
type KnInstanceSearchConfig struct {
	// MinRerankerScore drops instances the reranker scored below it, and returns nothing at all when
	// no candidate clears the bar. 0 (the default) leaves the gate off.
	//
	// It lives in deployment config rather than in the tool contract because the number does not
	// travel: measured on two of our own environments, "clearly relevant" tops out near 0.16 on one
	// and 0.74 on the other for the same kind of query. A caller cannot know which deployment it is
	// talking to, so it cannot pick this value; whoever calibrated the deployment can.
	MinRerankerScore float64 `yaml:"min_reranker_score"`
	// MinObjectTypeScoreRatio skips an object type whose concept-recall score is below this fraction
	// of the best-scoring object type of the same recall, before instance recall issues a query for
	// it. 0 (the default) leaves the pre-filter off.
	//
	// Off by default for want of a portable value: concept recall has already kept only its best
	// matches, so the spread among survivors is narrow — the worst one scored 0.21 and 0.27 of the
	// best on two of our own networks. A ratio that trims one of them barely touches the other.
	//
	// Those two numbers were measured when the concept-recall score was raw BM25. It is the
	// reranker's 0..1 relevance now, so a deployment that did calibrate this value has to measure
	// again — the ratios are not comparable across the two scales.
	MinObjectTypeScoreRatio float64 `yaml:"min_object_type_score_ratio"`
}

// MFModelAPI configuration uses a unified PrivateBaseConfig structure.

// RerankLLMConfig LLM parameter configuration for Rerank (the model is not configured here: the system default large model is used by default, per-request is overridden by rerank_llm_model)
type RerankLLMConfig struct {
	Temperature      float64 `yaml:"temperature" default:"0"`         // generate randomness.
	TopK             int     `yaml:"top_k" default:"2"`               // Sampling range.
	TopP             float64 `yaml:"top_p" default:"0.5"`             // Kernel sampling threshold.
	FrequencyPenalty float64 `yaml:"frequency_penalty" default:"0.5"` // Frequency penalty.
	PresencePenalty  float64 `yaml:"presence_penalty" default:"0.5"`  // Presence penalty.
	MaxTokens        int     `yaml:"max_tokens" default:"5000"`       // Maximum token count.
}

// FindSkillsConfig find_skills Skill recall configuration.
type FindSkillsConfig struct {
	DefaultTopK        int    `yaml:"default_top_k" default:"10"`
	MaxTopK            int    `yaml:"max_top_k" default:"20"`
	RecallTimeoutMs    int    `yaml:"recall_timeout_ms" default:"5000"`
	TotalTimeoutMs     int    `yaml:"total_timeout_ms" default:"10000"`
	SkillsObjectTypeID string `yaml:"skills_object_type_id" default:"skills"`
}

// SetMachineID sets machine ID
func (conf *Project) SetMachineID() {
	// Generate MachineID
	if conf.MachineID == "" {
		mid := os.Getenv(conf.PodID)
		if mid == "" {
			mid, _ = os.Hostname()
			// Empty is allowed
			mid = utils.MD5(mid)
			mid = mid[:8]
		}
		conf.MachineID = mid
	}
}

// GetMachineID gets machine ID
func (conf *Project) GetMachineID() string {
	return conf.MachineID
}

var (
	once         sync.Once
	configLoader *Config

	authEnabledOnce sync.Once
	authEnabled     atomic.Bool
)

// parseAuthEnabled parses an AUTH_ENABLED env value into a boolean.
// Returns true unless the value is exactly "false" or "0" (case-insensitive, trimmed).
func parseAuthEnabled(envVal string) bool {
	v := strings.TrimSpace(strings.ToLower(envVal))
	return v != "false" && v != "0"
}

// GetAuthEnabled returns whether ISF authentication is enabled.
// Defaults to true (secure by default). Only returns false when
// AUTH_ENABLED is explicitly set to "false" or "0" (case-insensitive).
func GetAuthEnabled() bool {
	authEnabledOnce.Do(func() {
		authEnabled.Store(parseAuthEnabled(os.Getenv("AUTH_ENABLED")))
	})
	return authEnabled.Load()
}

// NewConfigLoader gets configuration
func NewConfigLoader() *Config {
	once.Do(func() {
		profileDir := os.Getenv("CONFIG_PROFILE")
		var configFilePath, secretFilePath string
		if profileDir != "" {
			configFilePath = filepath.Join(profileDir, "agent-retrieval.yaml")
			secretFilePath = filepath.Join(profileDir, "agent-retrieval-secret.yaml")
		} else {
			configFilePath = "/sysvol/config/agent-retrieval.yaml"
			secretFilePath = "/sysvol/secret/agent-retrieval-secret.yaml"
		}

		// Set default configuration
		configLoader = &Config{}
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
		// Initialize observability related configuration
		configLoader.initOTelAndLog()
		// Set machine ID
		configLoader.Project.SetMachineID()
		overrideWithEnv(configLoader)
	})
	return configLoader
}

func (conf *Config) localConfig(path string) (err error) {
	err = defaults.Set(conf)
	if err != nil {
		return
	}

	file, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return
	}
	err = yaml.Unmarshal(file, conf)
	return
}

// overrideWithEnv automatically traverses struct, using reflection to override with env variables based on tags
func overrideWithEnv(cfg any) {
	v := reflect.ValueOf(cfg).Elem() // Get pointer to struct
	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)

		if field.Kind() == reflect.Struct {
			// Recursively handle nested struct
			overrideWithEnv(field.Addr().Interface())
			continue
		}

		// Get env tag of field
		envTag := fieldType.Tag.Get("env")
		if envTag == "" {
			continue // Skip if env tag is not defined
		}

		// Check if env variable exists
		envValue, exists := os.LookupEnv(envTag)
		if !exists {
			continue // Skip if env key does not exist
		}

		// If key exists but value is empty, set field to zero value of type
		if envValue == "" {
			field.Set(reflect.Zero(field.Type()))
			continue
		}

		// Use reflection to set field value directly, type match required
		//nolint:exhaustive // Only handle String/Int/Bool, other types are skipped by default.
		switch field.Kind() {
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
			// Unsupported types are skipped directly and no longer panic.
		}
	}
}

// Load & Initialize observability related configuration
func (conf *Config) initOTelAndLog() {
	// Initialize logger
	level := logger.Level(configLoader.Project.LoggerLevel)
	if configLoader.Project.Debug {
		level = logger.LevelDebug
	}

	// Load configuration file
	viper.SetConfigName("observability")
	viper.SetConfigType("yaml")
	if profileDir := os.Getenv("CONFIG_PROFILE"); profileDir != "" {
		viper.AddConfigPath(profileDir)
	} else {
		viper.AddConfigPath("/sysvol/config/")
	}
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

	// Initialize logger
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
