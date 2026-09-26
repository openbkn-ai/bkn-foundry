// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

// Package auditvalidator embeds the pinned canonical Audit v1 validator
// inputs. The registry JSON is a minimal derived runtime projection; its
// source digest is pinned to the canonical bkn-docs registry.
package auditvalidator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/auditstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/auditconsumer"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	SchemaVersion                 = "1.0"
	SchemaHeader                  = "bkn-audit-schema-version"
	CanonicalSchemaSHA256         = "4b1db1b116485e1b0432635406bcdffdc111be1b7cc583714a6a2c867efee69b"
	CanonicalRegistrySHA256       = "547f903c4b5f6b89ab5288a97fdf6e53e0e05babc2fba36125ad8b555323a54b"
	RuntimeRegistrySHA256         = "8cb47b1dba641af7c8cfac6b5671e87f23779774a0bfee64c7bbaa43a7fb6f79"
	CanonicalValueFixtureSHA256   = "2976cc4822bc9a9248b1aa66de29916a35fcb9988b61a313d6e86fc68c17ce40"
	KafkaFixtureSHA256            = "6ca65bf73f3345964d6a70eb95c3405e7145ebc64848aceabf16472057538dd4"
	ExecutionFactoryFixtureSHA256 = "2f39af3735b13f96b8d3205dfd584974ed5c2ce5d53e7458039a9e4234d757d0"
	maxAuditValueBytes            = 32 * 1024
	maxClockSkew                  = 5 * time.Minute
	maxRetentionAge               = 365 * 24 * time.Hour
	schemaURL                     = "https://openbkn.io/schemas/audit-event/1.0"
)

//go:embed assets/*.json
var assets embed.FS

type Validator struct {
	schema   *jsonschema.Schema
	registry registry
}

type registry struct {
	SourceRegistrySHA256 string            `json:"source_registry_sha256"`
	RegistryVersion      string            `json:"registry_version"`
	Sources              []sourceRule      `json:"sources"`
	Events               []eventRule       `json:"events"`
	SecretRules          []secretDetection `json:"secret_detection_rules"`
}

type sourceRule struct {
	ID                  string   `json:"source_id"`
	CollectionMethod    string   `json:"collection_method"`
	SchemaVersion       string   `json:"schema_version"`
	AllowedEnvironments []string `json:"allowed_environments"`
}

type eventRule struct {
	Name                string         `json:"event_name"`
	Category            string         `json:"log_category"`
	AllowedSourceIDs    []string       `json:"allowed_source_ids"`
	ResourceTypes       []string       `json:"resource_types"`
	RequiredAttributes  []string       `json:"required_attributes"`
	AllowedAttributes   []string       `json:"allowed_attributes"`
	SensitiveAttributes []string       `json:"sensitive_attributes"`
	OutcomeMapping      outcomeMapping `json:"outcome_mapping"`
	SchemaVersion       string         `json:"schema_version"`
	AllowUnknown        bool           `json:"allow_unknown"`
}

type outcomeMapping struct {
	Accepted []string `json:"accepted_outcomes"`
}

type secretDetection struct {
	Target  string `json:"match_target"`
	Pattern string `json:"pattern"`
	Action  string `json:"action"`
}

type rejection struct{ reason string }

func (e rejection) Error() string { return e.reason }

func New() (*Validator, error) {
	schemaBytes, err := assets.ReadFile("assets/schema.json")
	if err != nil {
		return nil, fmt.Errorf("read embedded audit schema: %w", err)
	}
	if digest(schemaBytes) != CanonicalSchemaSHA256 {
		return nil, errors.New("embedded audit schema source digest mismatch")
	}
	registryBytes, err := assets.ReadFile("assets/registry-runtime-v1.json")
	if err != nil {
		return nil, fmt.Errorf("read embedded audit registry: %w", err)
	}
	if digest(registryBytes) != RuntimeRegistrySHA256 {
		return nil, errors.New("embedded audit registry runtime artifact digest mismatch")
	}
	var rules registry
	if err := json.Unmarshal(registryBytes, &rules); err != nil {
		return nil, fmt.Errorf("decode embedded audit registry: %w", err)
	}
	if rules.SourceRegistrySHA256 != CanonicalRegistrySHA256 || rules.RegistryVersion == "" {
		return nil, errors.New("embedded audit registry source digest mismatch")
	}
	var schemaDocument any
	decoder := json.NewDecoder(bytes.NewReader(schemaBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&schemaDocument); err != nil {
		return nil, fmt.Errorf("decode embedded audit schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource(schemaURL, schemaDocument); err != nil {
		return nil, fmt.Errorf("register embedded audit schema: %w", err)
	}
	compiled, err := compiler.Compile(schemaURL)
	if err != nil {
		return nil, fmt.Errorf("compile embedded audit schema: %w", err)
	}
	return &Validator{schema: compiled, registry: rules}, nil
}

func (v *Validator) Validate(_ context.Context, record auditconsumer.Record) (auditstore.Event, error) {
	if record.Topic != auditconsumer.Topic {
		return auditstore.Event{}, permanent("wrong_topic")
	}
	if record.BrokerTime.IsZero() {
		return auditstore.Event{}, errors.New("kafka broker append timestamp is unavailable")
	}
	if len(record.Value) == 0 || len(record.Value) > maxAuditValueBytes {
		return auditstore.Event{}, permanent("record_too_large")
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(record.Value))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return auditstore.Event{}, permanent("json_invalid")
	}
	if err := v.schema.Validate(value); err != nil {
		return auditstore.Event{}, permanent("schema_invalid")
	}
	if err := validateHeaders(record.Headers, value); err != nil {
		return auditstore.Event{}, permanent("header_contract_invalid")
	}
	if err := validateRegistry(value, v.registry); err != nil {
		return auditstore.Event{}, err
	}
	if containsSecret(value, v.registry.SecretRules) {
		return auditstore.Event{}, permanent("secret_detected")
	}
	canonical, err := jsoncanonicalizer.Transform(record.Value)
	if err != nil {
		return auditstore.Event{}, permanent("json_invalid")
	}
	var eventID, sourceID string
	eventID, _ = value["event_id"].(string)
	sourceID, _ = value["source_id"].(string)
	target, _ := value["target"].(map[string]any)
	targetType, _ := target["type"].(string)
	targetID, _ := target["id"].(string)
	wantKey := []byte(sourceID + "\x1f" + targetType + "\x1f" + targetID)
	if !bytes.Equal(record.Key, wantKey) {
		return auditstore.Event{}, permanent("record_key_mismatch")
	}
	contentDigest := sha256.Sum256(canonical)
	contentHash := "sha256:" + hex.EncodeToString(contentDigest[:])
	var occurredAt time.Time
	occurredText, _ := value["occurred_at"].(string)
	occurredAt, err = time.Parse(time.RFC3339Nano, occurredText)
	if err != nil {
		return auditstore.Event{}, permanent("occurred_at_invalid")
	}
	if occurredAt.After(record.BrokerTime.Add(maxClockSkew)) {
		return auditstore.Event{}, permanent("clock_skew_future")
	}
	if record.BrokerTime.Sub(occurredAt) > maxRetentionAge {
		return auditstore.Event{}, permanent("retention_expired")
	}
	return auditstore.Event{
		EventID: eventID, ContentHash: contentHash, SourceID: sourceID,
		Payload: canonical, OccurredAt: occurredAt.UTC(), BrokerReceivedAt: record.BrokerTime.UTC(),
		Kafka: auditstore.KafkaCoordinate{Topic: record.Topic, Partition: record.Partition, Offset: record.Offset},
	}, nil
}

func validateHeaders(headers []auditconsumer.Header, value map[string]any) error {
	if len(headers) != 1 || headers[0].Key != SchemaHeader || string(headers[0].Value) != SchemaVersion || value["schema_version"] != SchemaVersion {
		return errors.New("audit schema header/value mismatch")
	}
	return nil
}

func validateRegistry(value map[string]any, rules registry) error {
	sourceID, _ := value["source_id"].(string)
	eventName, _ := value["event_name"].(string)
	category, _ := value["category"].(string)
	source, sourceFound := findSource(rules.Sources, sourceID)
	var event *eventRule
	for i := range rules.Events {
		if rules.Events[i].Name == eventName {
			event = &rules.Events[i]
			break
		}
	}
	if !sourceFound || event == nil || event.Category != category || !contains(event.AllowedSourceIDs, sourceID) {
		return permanent("registry_mapping_rejected")
	}
	if source.SchemaVersion == "" || event.SchemaVersion == "" {
		return permanent("registry_mapping_rejected")
	}
	if !contains(event.ResourceTypes, mapString(value["target"], "type")) {
		return permanent("registry_mapping_rejected")
	}
	if !contains(source.AllowedEnvironments, mapString(value["scope"], "environment")) {
		return permanent("source_environment_rejected")
	}
	outcome, _ := value["outcome"].(string)
	if !contains(event.OutcomeMapping.Accepted, outcome) {
		return permanent("registry_mapping_rejected")
	}
	facts, _ := value["facts"].(map[string]any)
	for _, key := range event.RequiredAttributes {
		if _, ok := facts[key]; !ok {
			return permanent("registry_mapping_rejected")
		}
	}
	for key := range facts {
		if !contains(event.AllowedAttributes, key) && !event.AllowUnknown {
			return permanent("registry_mapping_rejected")
		}
	}
	if source.CollectionMethod == "not_integrated" {
		return permanent("source_not_integrated")
	}
	if source.CollectionMethod != "source_adapter" {
		return permanent("source_collection_method_rejected")
	}
	return nil
}

func containsSecret(value any, rules []secretDetection) bool {
	compiled := make([]struct {
		target  string
		pattern *regexp.Regexp
	}, 0, len(rules))
	for _, rule := range rules {
		pattern, err := regexp.Compile(rule.Pattern)
		if err == nil {
			compiled = append(compiled, struct {
				target  string
				pattern *regexp.Regexp
			}{rule.Target, pattern})
		}
	}
	var walk func(any, string) bool
	walk = func(current any, key string) bool {
		for _, rule := range compiled {
			if rule.target == "field_name_regex" && rule.pattern.MatchString(key) {
				return true
			}
		}
		switch item := current.(type) {
		case map[string]any:
			for childKey, child := range item {
				if walk(child, childKey) {
					return true
				}
			}
		case []any:
			for _, child := range item {
				if walk(child, key) {
					return true
				}
			}
		case string:
			for _, rule := range compiled {
				if rule.target == "value_regex" && rule.pattern.MatchString(item) {
					return true
				}
			}
		}
		return false
	}
	return walk(value, "")
}

func findSource(sources []sourceRule, id string) (sourceRule, bool) {
	for _, source := range sources {
		if source.ID == id {
			return source, true
		}
	}
	return sourceRule{}, false
}

func mapString(value any, key string) string {
	if object, ok := value.(map[string]any); ok {
		result, _ := object[key].(string)
		return result
	}
	return ""
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func permanent(reason string) error {
	return &auditconsumer.PermanentError{Err: rejection{reason: reason}}
}

func IsPermanent(err error) bool {
	var permanentErr *auditconsumer.PermanentError
	return errors.As(err, &permanentErr)
}

func IsPermanentReason(err error, reason string) bool {
	var permanentErr *auditconsumer.PermanentError
	var rejectionErr rejection
	return errors.As(err, &permanentErr) && errors.As(err, &rejectionErr) && rejectionErr.reason == reason
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
