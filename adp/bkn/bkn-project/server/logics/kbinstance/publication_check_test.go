// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestPublicationRequiresAcknowledgedCurrentFullLinkingWithoutWriting(t *testing.T) {
	service, queue, data, draft, _ := factServiceFixture(t, "full")
	guard := func(context.Context) error { return nil }
	if _, err := service.VerifyCompleted(context.Background(), queue.record, draft, guard); err == nil {
		t.Fatal("unreviewed full results accepted")
	}
	preview, err := service.Preview(context.Background(), "Bearer operator-token", "project", "job", queue.record.Build.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(context.Background(), "Bearer operator-token", "project", "job", queue.record.Build.Owner, SystemLinkRequest{OperationID: uuid.NewString(), Signature: preview.Signature}); err != nil {
		t.Fatal(err)
	}
	before := len(data.written)
	if _, err := service.VerifyCompleted(context.Background(), queue.record, draft, guard); err != nil {
		t.Fatal("acknowledged complete generation rejected", err)
	}
	if len(data.written) != before {
		t.Fatal("publication verification rewrote instances")
	}
	queue.record.Build.Linking.Signature = "stale"
	if _, err := service.VerifyCompleted(context.Background(), queue.record, draft, guard); err == nil {
		t.Fatal("stale reviewed result accepted")
	}
}
