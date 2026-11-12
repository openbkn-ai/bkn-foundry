// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package capability_binding

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

// TestCapabilityTotalsExcludeModelReferences proves that model usage alone is not a mount.
// No model reader is expected: statistics come from stored binding rows only.
func TestCapabilityTotalsExcludeModelReferences(t *testing.T) {
	Convey("capability totals read direct mounts only", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		cba := bmock.NewMockCapabilityBindingAccess(ctrl)
		ota := bmock.NewMockObjectTypeAccess(ctrl)
		ata := bmock.NewMockActionTypeAccess(ctrl)
		aoa := bmock.NewMockAgentOperatorAccess(ctrl)
		service := &capabilityBindingService{appSetting: &common.AppSetting{}, cba: cba, ota: ota, ata: ata, aoa: aoa}

		cba.EXPECT().GetBindingsTotalByType(gomock.Any(), "kn1", "main").Return(map[string]int{}, nil)

		totals, err := service.GetCapabilityTotalsByType(context.Background(), "kn1", "main")
		So(err, ShouldBeNil)
		So(totals[interfaces.CAPABILITY_TYPE_FUNCTION], ShouldEqual, 0)
		So(totals[interfaces.CAPABILITY_TYPE_MCP_TOOL], ShouldEqual, 0)
	})
}
