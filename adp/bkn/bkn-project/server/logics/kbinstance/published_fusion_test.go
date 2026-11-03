// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbinstance

import (
	"context"
	"strings"
	"testing"
)

func TestPublishedFusionCanPreviewValidatedStagingWithoutClaimingLiveAcknowledgements(t *testing.T) {
	service, queue, data, _, _, draft := systemLinkServiceFixture(t)
	build := queue.record.Build
	build.Mode = "full"
	for i := range build.Files {
		build.Files[i].State = "validated"
		build.Files[i].RowsDigest = strings.Repeat("a", 64)
		build.Files[i].RowsWritten = 0
	}
	guard := func(context.Context) error { return nil }
	// The ordinary construction reader must still reject unacknowledged rows.
	if _, err := ReadSourceClaims(context.Background(), data, build, draft, guard); err == nil {
		t.Fatal("staging masqueraded as live build acknowledgement")
	}
	plan, err := PreparePublishedFusion(context.Background(), data, service.Vega, build, draft, 1234, guard)
	if err != nil || plan == nil || len(plan.Preview.Signature) != 64 {
		t.Fatal("staged fusion could not be previewed", err)
	}
	for _, file := range build.Files {
		if file.State != "validated" || file.RowsWritten != 0 {
			t.Fatal("preview modified live write receipts")
		}
	}
}
func TestPublishedFusionHandlesTheCompleteAuthorizedRemovalOfAllFiles(t *testing.T) {
	service, queue, data, _, _, draft := systemLinkServiceFixture(t)
	build := queue.record.Build
	build.Mode = "full"
	build.Files = nil
	plan, err := PreparePublishedFusion(context.Background(), data, service.Vega, build, draft, 1234, func(context.Context) error { return nil })
	if err != nil || plan == nil || len(plan.FusionWrites) != 0 || len(plan.Entities) == 0 {
		t.Fatal("empty source could not retire canonical entities", err)
	}
	for _, scope := range plan.Entities {
		if scope.Model != build.ModelFingerprint || len(scope.Keep) != 0 {
			t.Fatal("all-file removal escaped current model scope")
		}
	}
}
