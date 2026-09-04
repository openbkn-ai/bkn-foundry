package skill

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
)

// TestSearchSkillsFailClosed covers the security boundary of #1260: an empty whitelist means
// "nothing is in scope", never "do not filter". Both spellings — an empty array and a missing
// field — are locked here because the difference between them is exactly how the platform-wide
// leak would slip in.
func TestSearchSkillsFailClosed(t *testing.T) {
	Convey("白名单为空时不查库、返回空", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		// No EXPECT on the Vega client: the controller fails the test if a query is issued.
		service := &skillSearchService{
			logger:     logger.DefaultLogger(),
			vegaClient: mocks.NewMockVegaBackendClient(ctrl),
		}

		Convey("skill_ids 为空数组", func() {
			resp, err := service.SearchSkills(context.Background(), &interfaces.SearchSkillsReq{
				Query: "交期", SkillIDs: []string{},
			})
			So(err, ShouldBeNil)
			So(resp.Entries, ShouldBeEmpty)
		})

		Convey("skill_ids 字段缺失", func() {
			resp, err := service.SearchSkills(context.Background(), &interfaces.SearchSkillsReq{Query: "交期"})
			So(err, ShouldBeNil)
			So(resp.Entries, ShouldBeEmpty)
		})

		Convey("skill_ids 全是空白字符串", func() {
			resp, err := service.SearchSkills(context.Background(), &interfaces.SearchSkillsReq{
				Query: "交期", SkillIDs: []string{"", "   "},
			})
			So(err, ShouldBeNil)
			So(resp.Entries, ShouldBeEmpty)
		})
	})
}

// TestSearchSkillsPreFilter locks that the whitelist enters the query as a filter rather than
// being applied to the results: filtering after retrieval would return nothing whenever the
// caller's few skills lose the global top_k race.
func TestSearchSkillsPreFilter(t *testing.T) {
	Convey("白名单进查询条件做 pre-filter", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		vega := mocks.NewMockVegaBackendClient(ctrl)
		// The dataset read fails, so the vector channel is skipped and the query runs on
		// full-text alone — the whitelist must still be there.
		vega.EXPECT().GetResourceByID(gomock.Any(), gomock.Any()).Return(nil, context.DeadlineExceeded).AnyTimes()
		vega.EXPECT().QueryDatasetData(gomock.Any(), "bkn_execution_factory_skill_dataset", gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, params *interfaces.VegaDataQueryParams) (*interfaces.VegaDataQueryResp, error) {
				root := params.FilterCondition
				So(root["operation"], ShouldEqual, "and")
				subs, ok := root["sub_conditions"].([]map[string]any)
				So(ok, ShouldBeTrue)
				So(subs[0]["field"], ShouldEqual, "skill_id")
				So(subs[0]["operation"], ShouldEqual, "in")
				So(subs[0]["value"], ShouldResemble, []string{"skill-1", "skill-2"})
				So(params.Paging.Limit, ShouldEqual, 5)
				// Vega rejects any other mode with 400; caught on the test server, not here.
				So(params.Paging.Mode, ShouldEqual, "single")
				return &interfaces.VegaDataQueryResp{Entries: []map[string]any{
					{"skill_id": "skill-1", "name": "交期评估", "description": "评估交期", "_score": 1.5},
					{"name": "没有 id 的脏文档"},
				}}, nil
			})

		service := &skillSearchService{logger: logger.DefaultLogger(), vegaClient: vega}

		resp, err := service.SearchSkills(context.Background(), &interfaces.SearchSkillsReq{
			Query: "交期", SkillIDs: []string{"skill-1", "skill-2", "skill-1", " "}, TopK: 5,
		})

		So(err, ShouldBeNil)
		So(len(resp.Entries), ShouldEqual, 1)
		So(resp.Entries[0].SkillID, ShouldEqual, "skill-1")
		So(resp.Entries[0].Score, ShouldEqual, 1.5)
		So(resp.Entries[0].MatchedBy, ShouldEqual, interfaces.SkillMatchedByMatch)
	})
}

// TestSearchSkillsWhitelistBound rejects an oversized whitelist instead of building a request no
// one can debug.
func TestSearchSkillsWhitelistBound(t *testing.T) {
	Convey("白名单超过上限报 400", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ids := make([]string, 0, maxSkillSearchWhitelist+1)
		for i := 0; i <= maxSkillSearchWhitelist; i++ {
			ids = append(ids, string(rune('a'+i%26))+string(rune('0'+i%10))+"-"+string(rune(i)))
		}
		service := &skillSearchService{
			logger:     logger.DefaultLogger(),
			vegaClient: mocks.NewMockVegaBackendClient(ctrl),
		}

		resp, err := service.SearchSkills(context.Background(), &interfaces.SearchSkillsReq{
			Query: "x", SkillIDs: ids,
		})

		So(resp, ShouldBeNil)
		So(err, ShouldNotBeNil)
	})
}
