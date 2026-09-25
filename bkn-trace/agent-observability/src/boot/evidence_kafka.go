// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package boot

import (
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/conf"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/evidenceconsumer"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/kafkaaccess/kafkaruntime"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidenceadmission"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidencemigration"
)

var (
	_ evidenceconsumer.Ledger           = (*ledgersvc.Service)(nil)
	_ ievidenceadmission.ReadOnlySource = (*sessionstore.Store)(nil)
	_ evidenceconsumer.RejectionWriter  = (*sessionstore.Store)(nil)
)

func newEvidenceKafkaProcessor(
	admission ievidenceadmission.ReadOnlySource,
	migration ievidencemigration.AdmissionReader,
	migrationResults ievidencemigration.ConsumerResultWriter,
	ledger evidenceconsumer.Ledger,
	rejections evidenceconsumer.RejectionWriter,
) (*evidenceconsumer.Processor, error) {
	return evidenceconsumer.NewProcessorWithMigration(admission, migration, migrationResults, ledger, rejections)
}

func newEvidenceKafkaRuntime(
	kafkaConfig conf.KafkaConsumerConfig,
	topic conf.KafkaTopicConsumerConfig,
	admission ievidenceadmission.ReadOnlySource,
	migration ievidencemigration.AdmissionReader,
	migrationResults ievidencemigration.ConsumerResultWriter,
	ledger evidenceconsumer.Ledger,
	rejections evidenceconsumer.RejectionWriter,
	factory kafkaruntime.ReaderFactory,
) (*kafkaruntime.Runtime, error) {
	processor, err := newEvidenceKafkaProcessor(admission, migration, migrationResults, ledger, rejections)
	if err != nil {
		return nil, err
	}
	if factory == nil {
		return kafkaruntime.New(kafkaConfig, topic, evidenceconsumer.KafkaProcessor(processor))
	}
	return kafkaruntime.NewWithFactory(kafkaConfig, topic, evidenceconsumer.KafkaProcessor(processor), factory)
}
