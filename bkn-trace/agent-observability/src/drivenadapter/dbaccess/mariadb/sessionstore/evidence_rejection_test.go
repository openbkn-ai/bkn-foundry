// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package sessionstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidenceadmission"
)

func TestEvidenceRejectionDurablyStoresSafeCoordinateSummaryWithoutPayload(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	brokerTime := time.Date(2026, 9, 24, 8, 30, 0, 0, time.UTC)
	record := ievidenceadmission.Record{
		Topic: "openbkn.evidence.v1", Key: "stream:boot", Value: []byte(`sensitive-payload`), Partition: 2, Offset: 19,
		BrokerTime: brokerTime, ProducerStreamID: "stream:boot", ProducerSequence: 5,
		Headers: []ievidenceadmission.Header{{Key: "content-type", Value: "application/json"}, {Key: "producer_instance_id", Value: "service#boot"}},
	}
	mock.ExpectExec("INSERT INTO bkn_trace_evidence_ingest_rejections").
		WithArgs("openbkn.evidence.v1", 2, int64(19), brokerTime, sqlmock.AnyArg(), sqlmock.AnyArg(), len(record.Value), uint64(9), "event-1", "hash-1", "producer-1", "stream:boot", "", "source_mapping_rejected").
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := store.RecordKafkaRejection(context.Background(), record, ievidenceadmission.RejectionDetails{EventID: "event-1", PayloadHash: "hash-1", ProducerID: "producer-1", ReasonCode: "source_mapping_rejected"}); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
