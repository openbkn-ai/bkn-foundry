// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package capability_binding

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

// implicitSources is the provenance of a model that uses one function box tool and one openapi
// box tool, neither of them mounted.
func implicitSources() *provenance {
	return &provenance{
		available: true,
		byCapability: map[capabilityKey][]*interfaces.CapabilitySource{
			functionKeyOf("box-fn", "tool-fn"):   {{Kind: interfaces.CAPABILITY_SOURCE_OBJECT_TYPE}},
			functionKeyOf("box-api", "tool-api"): {{Kind: interfaces.CAPABILITY_SOURCE_ACTION_TYPE}},
		},
	}
}

func expectBoxKind(aoa *bmock.MockAgentOperatorAccess, boxID, kind string) {
	aoa.EXPECT().ListBoxTools(gomock.Any(), boxID).Return([]*interfaces.ToolBrief{
		{BoxID: boxID, BoxMetadataType: kind},
	}, nil).AnyTimes()
}

// TestBoxesOfKindCoversUnmountedCapabilities: a tool the model uses without a mount is listed,
// so filtering the list by its box's kind must keep it. Resolving the boxes from the mounted rows
// alone dropped it from every kind: shown unfiltered, absent from both the function and the
// openapi list, under a badge that still counted it.
func TestBoxesOfKindCoversUnmountedCapabilities(t *testing.T) {
	Convey("按类型过滤时，未挂载但被模型引用的能力所在的工具集同样参与判断", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		service, cba, aoa := newTestServiceWithFactory(t, ctrl)
		cba.EXPECT().GetFunctionTotalsByOwner(gomock.Any(), "kn1", "main").
			Return(map[string]int{}, nil).AnyTimes()
		expectBoxKind(aoa, "box-fn", interfaces.EXEC_BOX_METADATA_TYPE_FUNCTION)
		expectBoxKind(aoa, "box-api", interfaces.EXEC_BOX_METADATA_TYPE_OPENAPI)

		functionBoxes, err := service.boxesOfKind(context.Background(), "kn1", "main",
			interfaces.EXEC_BOX_METADATA_TYPE_FUNCTION, implicitSources())
		So(err, ShouldBeNil)
		So(functionBoxes, ShouldResemble, []string{"box-fn"})

		apiBoxes, err := service.boxesOfKind(context.Background(), "kn1", "main",
			interfaces.EXEC_BOX_METADATA_TYPE_OPENAPI, implicitSources())
		So(err, ShouldBeNil)
		So(apiBoxes, ShouldResemble, []string{"box-api"})
	})

	Convey("工具集读取失败时返回 502 并指出是哪个工具集", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		service, cba, aoa := newTestServiceWithFactory(t, ctrl)
		cba.EXPECT().GetFunctionTotalsByOwner(gomock.Any(), "kn1", "main").Return(map[string]int{}, nil)
		expectBoxKind(aoa, "box-fn", interfaces.EXEC_BOX_METADATA_TYPE_FUNCTION)
		aoa.EXPECT().ListBoxTools(gomock.Any(), "box-api").Return(nil, errors.New("factory down")).AnyTimes()

		_, err := service.boxesOfKind(context.Background(), "kn1", "main",
			interfaces.EXEC_BOX_METADATA_TYPE_OPENAPI, implicitSources())
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "box_id=box-api")
	})
}

// TestCountAPIBindingsWeighsUnmountedCapabilities: the totals count unmounted capabilities as
// functions first and then move the openapi ones to APIs. Moving only the mounted ones left an
// openapi tool the model uses on the Functions badge.
func TestCountAPIBindingsWeighsUnmountedCapabilities(t *testing.T) {
	Convey("API 计数包含 openapi 工具集里未挂载的能力", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		service, cba, aoa := newTestServiceWithFactory(t, ctrl)
		cba.EXPECT().GetFunctionTotalsByOwner(gomock.Any(), "kn1", "main").
			Return(map[string]int{"box-api": 2, "box-fn": 1}, nil)
		expectBoxKind(aoa, "box-api", interfaces.EXEC_BOX_METADATA_TYPE_OPENAPI)
		expectBoxKind(aoa, "box-fn", interfaces.EXEC_BOX_METADATA_TYPE_FUNCTION)
		expectBoxKind(aoa, "box-api-2", interfaces.EXEC_BOX_METADATA_TYPE_OPENAPI)

		apis, err := service.countAPIBindings(context.Background(), "kn1", "main",
			map[string]int{"box-api": 1, "box-api-2": 3, "box-fn": 4})
		So(err, ShouldBeNil)
		// Mounted 2 + unmounted 1 in box-api, unmounted 3 in box-api-2; box-fn is not an API.
		So(apis, ShouldEqual, 6)
	})

	Convey("工具集读取失败时返回错误，由调用方退回全部计为函数", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		service, cba, aoa := newTestServiceWithFactory(t, ctrl)
		cba.EXPECT().GetFunctionTotalsByOwner(gomock.Any(), "kn1", "main").Return(map[string]int{}, nil)
		aoa.EXPECT().ListBoxTools(gomock.Any(), "box-api").Return(nil, errors.New("factory down"))

		_, err := service.countAPIBindings(context.Background(), "kn1", "main", map[string]int{"box-api": 1})
		So(err, ShouldNotBeNil)
	})
}

// TestBoxKindsBoundedConcurrency: the lookups run side by side, but never more than
// boxKindLookupLimit at once, and every box gets an answer.
func TestBoxKindsBoundedConcurrency(t *testing.T) {
	Convey("工具集类型并发查询且不超过上限", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		service, _, aoa := newTestServiceWithFactory(t, ctrl)
		var inFlight, peak int32
		aoa.EXPECT().ListBoxTools(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, boxID string) ([]*interfaces.ToolBrief, error) {
				now := atomic.AddInt32(&inFlight, 1)
				for {
					old := atomic.LoadInt32(&peak)
					if now <= old || atomic.CompareAndSwapInt32(&peak, old, now) {
						break
					}
				}
				time.Sleep(20 * time.Millisecond)
				atomic.AddInt32(&inFlight, -1)
				return []*interfaces.ToolBrief{{BoxID: boxID, BoxMetadataType: interfaces.EXEC_BOX_METADATA_TYPE_OPENAPI}}, nil
			}).Times(3 * boxKindLookupLimit)

		boxIDs := map[string]struct{}{"": {}}
		for i := 0; i < 3*boxKindLookupLimit; i++ {
			boxIDs[fmt.Sprintf("box-%02d", i)] = struct{}{}
		}

		kinds, _, err := service.boxKinds(context.Background(), boxIDs)
		So(err, ShouldBeNil)
		// The empty ID is skipped, every other box is resolved.
		So(len(kinds), ShouldEqual, 3*boxKindLookupLimit)
		So(atomic.LoadInt32(&peak), ShouldBeGreaterThan, 1)
		So(atomic.LoadInt32(&peak), ShouldBeLessThanOrEqualTo, boxKindLookupLimit)
	})
}

// TestBoxKindsStopsAtFirstFailure: one unreadable box decides the answer, so the lookups still in
// flight are cancelled and no new ones start. Without that, a failure next to hung lookups would
// wait out the client timeout of every one of them.
func TestBoxKindsStopsAtFirstFailure(t *testing.T) {
	Convey("首个工具集读取失败后取消其余查询", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		service, _, aoa := newTestServiceWithFactory(t, ctrl)
		aoa.EXPECT().ListBoxTools(gomock.Any(), "box-bad").Return(nil, errors.New("factory down"))
		aoa.EXPECT().ListBoxTools(gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, _ string) ([]*interfaces.ToolBrief, error) {
				// A hung factory: only cancellation ends the call.
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(5 * time.Second):
					return nil, errors.New("lookup was not cancelled")
				}
			}).AnyTimes()

		// One batch, so every lookup is in flight when box-bad fails whatever the map order.
		boxIDs := map[string]struct{}{"box-bad": {}}
		for i := 0; i < boxKindLookupLimit-1; i++ {
			boxIDs[fmt.Sprintf("box-%02d", i)] = struct{}{}
		}

		start := time.Now()
		_, failedBox, err := service.boxKinds(context.Background(), boxIDs)
		So(err, ShouldNotBeNil)
		So(failedBox, ShouldEqual, "box-bad")
		So(time.Since(start), ShouldBeLessThan, 2*time.Second)
	})

	Convey("调用方 context 已结束时返回错误而不是不完整的结果", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		service, _, aoa := newTestServiceWithFactory(t, ctrl)
		aoa.EXPECT().ListBoxTools(gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, _ string) ([]*interfaces.ToolBrief, error) {
				return nil, ctx.Err()
			}).AnyTimes()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		kinds, _, err := service.boxKinds(ctx, map[string]struct{}{"box-1": {}, "box-2": {}})
		So(err, ShouldNotBeNil)
		So(kinds, ShouldBeNil)
	})
}
