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
	"time"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/auditstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/auditconsumer"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	SchemaVersion                 = "1.0"
	SchemaHeader                  = "bkn-audit-schema-version"
	CanonicalSchemaSHA256         = "5aa7018a4b0b93cb3e336e1507b0d58a3d9f828c5e7be25e79863e345ef1e01f"
	CanonicalRegistrySHA256       = "555134f555aa4b802b9a69940a28f5332a136061335284f6a42ba9fbe594cb59"
	RuntimeRegistrySHA256         = "b8cf27603befc3c745c94572b2f209b741fb8d337a570153cba3de39333993c2"
	CanonicalValueFixtureSHA256   = "fa5115dd176c2539a6ce94a329324d8b257211699e6e3ef020a9a91f56ddbcf3"
	KafkaFixtureSHA256            = "8e6598557c196f536149404f28cb2113520b518543a9fc0ca648d22d7f8af29f"
	ExecutionFactoryFixtureSHA256 = "2f39af3735b13f96b8d3205dfd584974ed5c2ce5d53e7458039a9e4234d757d0"
	maxAuditValueBytes            = 32 * 1024
	maxClockSkew                  = 5 * time.Minute
	schemaURL                     = "https://openbkn.io/schemas/audit-event/1.0"
)

//go:embed assets/*.json
var assets embed.FS

type Validator struct {
	schema   *jsonschema.Schema
	registry registry
}

type registry struct {
	SourceRegistrySHA256 string       `json:"source_registry_sha256"`
	RegistryVersion      string       `json:"registry_version"`
	Sources              []sourceRule `json:"sources"`
	Events               []eventRule  `json:"events"`
}

type sourceRule struct {
	ID                  string   `json:"source_id"`
	Owner               string   `json:"owner"`
	Modules             []string `json:"modules"`
	CollectionMethod    string   `json:"collection_method"`
	Reliability         string   `json:"reliability"`
	SchemaVersion       string   `json:"schema_version"`
	AllowedEnvironments []string `json:"allowed_environments"`
}

// RegisteredSource describes a declared source, not an observed producer or
// proof of end-to-end collection. Query availability is reported separately.
type RegisteredSource struct {
	SourceID                 string   `json:"source_id"`
	Owner                    string   `json:"owner"`
	Modules                  []string `json:"modules"`
	DeclaredCollectionMethod string   `json:"declared_collection_method"`
	DeclaredReliability      string   `json:"declared_reliability"`
}

func RegisteredSources() (string, []RegisteredSource, error) {
	registryBytes, err := assets.ReadFile("assets/registry-runtime-v1.json")
	if err != nil {
		return "", nil, err
	}
	if digest(registryBytes) != RuntimeRegistrySHA256 {
		return "", nil, errors.New("embedded Audit registry runtime artifact digest mismatch")
	}
	var rules registry
	if err := json.Unmarshal(registryBytes, &rules); err != nil {
		return "", nil, err
	}
	if rules.SourceRegistrySHA256 != CanonicalRegistrySHA256 || rules.RegistryVersion == "" {
		return "", nil, errors.New("embedded Audit registry source digest mismatch")
	}
	sources := make([]RegisteredSource, 0, len(rules.Sources))
	for _, source := range rules.Sources {
		sources = append(sources, RegisteredSource{
			SourceID: source.ID, Owner: source.Owner,
			Modules:                  append([]string(nil), source.Modules...),
			DeclaredCollectionMethod: source.CollectionMethod,
			DeclaredReliability:      source.Reliability,
		})
	}
	return rules.RegistryVersion, sources, nil
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
	if record.BrokerTime.Sub(occurredAt) > auditstore.MaxAcceptedOccurredAtAge {
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
	if source.CollectionMethod != "kafka_audit" {
		return permanent("source_collection_method_rejected")
	}
	return nil
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
