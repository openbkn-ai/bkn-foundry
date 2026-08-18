// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package setup

import (
	"fmt"

	"github.com/spf13/viper"
)

// TestConfig is the AT test configuration.
type TestConfig struct {
	BKNBackend BKNBackendConfig `mapstructure:"bkn_backend"`
	MariaDB    MariaDBConfig    `mapstructure:"mariadb"`
	OpenSearch OpenSearchConfig `mapstructure:"opensearch"`
}

// BKNBackendConfig is the BKN Backend service configuration.
type BKNBackendConfig struct {
	BaseURL string `mapstructure:"base_url"` // BKN Backend HTTP service address.
}

// MariaDBConfig is the target MariaDB configuration for tests.
type MariaDBConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Database string `mapstructure:"database"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
}

// OpenSearchConfig is the target OpenSearch configuration for tests.
type OpenSearchConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
	UseSSL   bool   `mapstructure:"use_ssl"`
}

// LoadTestConfig loads the test configuration.
// It reads testdata/test-config.yaml first.
// It supports environment variable overrides with the BKN_TEST_ prefix.
func LoadTestConfig() (*TestConfig, error) {
	viper.SetConfigName("test-config")
	viper.SetConfigType("yaml")

	// Add multiple possible configuration file paths.
	viper.AddConfigPath("./testdata")                         // Run from the test directory.
	viper.AddConfigPath("./integration_tests/testdata")       // Run from the tests directory.
	viper.AddConfigPath("./tests/integration_tests/testdata") // Run from the server directory.
	viper.AddConfigPath("../testdata")                        // Run from a subdirectory.
	viper.AddConfigPath("../../testdata")                     // Run from a deeper subdirectory.
	viper.AddConfigPath("../../../testdata")                  // Run from a deeper subdirectory.

	// Support environment variable overrides.
	viper.SetEnvPrefix("BKN_TEST")
	viper.AutomaticEnv()

	// Read the configuration file.
	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("读取test-config.yaml失败: %w\n提示: 请确保配置文件存在于tests/integration_tests/testdata/目录", err)
	}

	var config TestConfig
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}

	// Validate required fields.
	if config.BKNBackend.BaseURL == "" {
		return nil, fmt.Errorf("配置错误: bkn_backend.base_url 不能为空")
	}

	return &config, nil
}
