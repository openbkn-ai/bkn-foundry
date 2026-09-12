// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package main

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"

	_ "github.com/openbkn-ai/bkn-comm-go/db/driver"
	"gopkg.in/yaml.v3"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type databaseConfig struct {
	Type     string `yaml:"type"`
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Name     string `yaml:"name"`
	Params   string `yaml:"params"`
}

type migrationConfig struct {
	DB databaseConfig `yaml:"db"`
}

func loadDatabaseConfig(path string) (databaseConfig, error) {
	cfg := migrationConfig{DB: databaseConfig{
		Type: "MySQL", Host: "127.0.0.1", Port: 3306,
		User: "safe", Password: "secret", Name: "safe",
	}}
	if path == "" {
		path = os.Getenv("SAFE_CONFIG")
	}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return databaseConfig{}, fmt.Errorf("read config %q: %w", path, err)
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return databaseConfig{}, fmt.Errorf("parse config %q: %w", path, err)
		}
	}
	applyDatabaseEnvironment(&cfg.DB)
	return cfg.DB, nil
}

func applyDatabaseEnvironment(cfg *databaseConfig) {
	assignNonEmpty(&cfg.Type, "SAFE_DB_TYPE")
	assignNonEmpty(&cfg.Host, "SAFE_DB_HOST")
	assignNonEmpty(&cfg.User, "SAFE_DB_USER")
	assignNonEmpty(&cfg.Password, "SAFE_DB_PASSWORD")
	assignNonEmpty(&cfg.Name, "SAFE_DB_NAME")
	assignNonEmpty(&cfg.Params, "SAFE_DB_PARAMS")
	if value := os.Getenv("SAFE_DB_PORT"); value != "" {
		if port, err := strconv.Atoi(value); err == nil && port > 0 {
			cfg.Port = port
		}
	}
}

func assignNonEmpty(target *string, name string) {
	if value := os.Getenv(name); value != "" {
		*target = value
	}
}

func (cfg databaseConfig) dsn() string {
	params := cfg.Params
	if params == "" {
		params = "charset=utf8mb4&parseTime=true&loc=Local"
	}
	return fmt.Sprintf(
		"%s:%s@tcp(%s:%d)/%s?%s",
		cfg.User,
		cfg.Password,
		cfg.Host,
		cfg.Port,
		cfg.Name,
		params,
	)
}

func openDatabase(cfg databaseConfig) (*gorm.DB, error) {
	connection, err := sql.Open("openbkn-rds", cfg.dsn())
	if err != nil {
		return nil, fmt.Errorf("open openbkn-rds: %w", err)
	}
	db, err := gorm.Open(
		mysql.New(mysql.Config{Conn: connection}),
		migrationGORMConfig(),
	)
	if err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("gorm open: %w", err)
	}
	return db, nil
}

func migrationGORMConfig() *gorm.Config {
	// Standard output is reserved for the machine-readable migration report.
	// The command wraps database failures with actionable errors, so suppress
	// GORM's independent SQL logger rather than allowing it to corrupt JSON.
	return &gorm.Config{Logger: logger.Discard}
}
