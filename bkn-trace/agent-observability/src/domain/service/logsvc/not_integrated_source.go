// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package logsvc

import (
	"context"
	"errors"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

var ErrAuditNotConfigured = errors.New("audit Kafka consumer is not configured")

const AuditConfigurationAction = "Configure kafkaConsumers.audit in Helm values with brokers, an independent consumer group, SASL and an existing credentials Secret; use Core MariaDB with autoMigrate enabled."

// Keep the disabled ledger visible without querying it or falling back to runtime logs.
type unconfiguredAuditSource struct{ *NotIntegratedSource }

func NewUnconfiguredAuditSource() Source {
	return &unconfiguredAuditSource{NewNotIntegratedSource("audit-ledger", []string{
		observabilityvo.CategoryAccessUser, observabilityvo.CategoryAuditAdmin, observabilityvo.CategoryAuditSecurity,
	}, []string{"system_management"})}
}

func (source *unconfiguredAuditSource) Metadata() observabilityvo.SourceStatus {
	status := source.NotIntegratedSource.Metadata()
	status.Reason = "audit_consumer_not_configured"
	status.RequiredAction = AuditConfigurationAction
	return status
}

type NotIntegratedSource struct {
	id         string
	categories []string
	modules    []string
}

func NewNotIntegratedSource(id string, categories, modules []string) *NotIntegratedSource {
	return &NotIntegratedSource{
		id: id, categories: append([]string(nil), categories...), modules: append([]string(nil), modules...),
	}
}

func (source *NotIntegratedSource) ID() string { return source.id }

func (source *NotIntegratedSource) Search(context.Context, observabilityvo.LogQuery) (observabilityvo.SourcePage, error) {
	return observabilityvo.SourcePage{}, ErrSourcesUnavailable
}

func (source *NotIntegratedSource) Metadata() observabilityvo.SourceStatus {
	return observabilityvo.SourceStatus{
		SourceID: source.id, Status: "not_integrated", Reason: "source_not_integrated",
		Reliability: "best_effort", CollectionMethod: "not_integrated",
		CoveredModules: append([]string(nil), source.modules...), CountAccuracy: "unavailable",
		Categories: append([]string(nil), source.categories...),
	}
}
