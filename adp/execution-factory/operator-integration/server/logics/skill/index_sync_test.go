package skill

import (
	"context"
	"database/sql"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
)

// The Skill dataset is retired (#1370): Skills are documents in the unified capability index, and
// the lifecycle this file used to cover — the dataset's own schema, embedding model, rebuild and
// restore — belongs to that index and is tested with it. What remains here is the seam: turning a
// Skill row into a capability document, and deciding which snapshot of a Skill counts.

func newSync(capabilitySync interfaces.CapabilityIndexSyncService,
	releaseRepo model.ISkillReleaseDB) *skillIndexSync {
	return &skillIndexSync{
		capabilitySync: capabilitySync,
		releaseRepo:    releaseRepo,
		logger:         logger.DefaultLogger(),
	}
}

// TestSkillIsWrittenAsACapability locks the identity a Skill takes in the shared index. Getting the
// owner wrong would collide a Skill with a Function tool of the same id.
func TestSkillIsWrittenAsACapability(t *testing.T) {
	Convey("Skill 以三段式身份写入能力索引", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		index := mocks.NewMockCapabilityIndexSyncService(ctrl)
		var written *interfaces.CapabilityDocument
		index.EXPECT().UpsertCapability(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, doc *interfaces.CapabilityDocument) error {
				written = doc
				return nil
			}).Times(1)

		skill := &model.SkillRepositoryDB{
			SkillID: "skill-1", Name: "库存阈值预警通知", Description: "库存低于阈值时通知负责人",
			Version: "v3", Category: "supply", CreateUser: "u1", UpdateUser: "u2",
			CreateTime: 11, UpdateTime: 22,
		}
		So(newSync(index, nil).UpsertSkill(context.Background(), skill), ShouldBeNil)

		So(written.CapabilityType, ShouldEqual, interfaces.CapabilityTypeSkill)
		So(written.CapabilityID, ShouldEqual, "skill-1")
		// A Skill belongs to the platform, not to a box or a server.
		So(written.OwnerID, ShouldEqual, "")
		So(written.Name, ShouldEqual, "库存阈值预警通知")
		So(written.Description, ShouldEqual, "库存低于阈值时通知负责人")
		So(written.Version, ShouldEqual, "v3")
		So(written.Category, ShouldEqual, "supply")
		So(written.UpdateTime, ShouldEqual, int64(22))
	})
}

// TestUpdateAndDeleteAddressTheSameDocument keeps an update from creating a second row and a delete
// from missing the one that exists.
func TestUpdateAndDeleteAddressTheSameDocument(t *testing.T) {
	Convey("更新与删除指向同一份文档", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		index := mocks.NewMockCapabilityIndexSyncService(ctrl)
		index.EXPECT().UpsertCapability(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, doc *interfaces.CapabilityDocument) error {
				So(doc.CapabilityID, ShouldEqual, "skill-1")
				return nil
			})
		index.EXPECT().DeleteCapability(gomock.Any(), interfaces.CapabilityRef{
			CapabilityType: interfaces.CapabilityTypeSkill, CapabilityID: "skill-1",
		}).Return(nil)

		svc := newSync(index, nil)
		So(svc.UpdateSkill(context.Background(), &model.SkillRepositoryDB{SkillID: "skill-1"}), ShouldBeNil)
		So(svc.DeleteSkill(context.Background(), "skill-1"), ShouldBeNil)
	})
}

// TestSkillIndexPayloadPicksTheRunnableSnapshot covers the rule that decides what the index holds.
//
// It has to match what a caller would actually run: an editing Skill with no published release is
// not runnable, so indexing it would advertise something that cannot be invoked.
func TestSkillIndexPayloadPicksTheRunnableSnapshot(t *testing.T) {
	Convey("索引持有的是可运行的那份快照", t, func() {
		release := &model.SkillReleaseDB{SkillID: "skill-1", Name: "已发布版", Version: "v2"}
		withRelease := &stubSkillReleaseRepo{
			selectBySkillID: func(context.Context, *sql.Tx, string) (*model.SkillReleaseDB, error) {
				return release, nil
			},
		}
		withoutRelease := &stubSkillReleaseRepo{
			selectBySkillID: func(context.Context, *sql.Tx, string) (*model.SkillReleaseDB, error) {
				return nil, nil
			},
		}

		Convey("已发布且有 release：取 release", func() {
			payload, err := newSync(nil, withRelease).skillIndexPayload(context.Background(),
				&model.SkillRepositoryDB{SkillID: "skill-1", Status: interfaces.BizStatusPublished.String()})
			So(err, ShouldBeNil)
			So(payload.Name, ShouldEqual, "已发布版")
		})

		Convey("已发布但没有 release：退回本体", func() {
			payload, err := newSync(nil, withoutRelease).skillIndexPayload(context.Background(),
				&model.SkillRepositoryDB{SkillID: "skill-1", Name: "本体", Status: interfaces.BizStatusPublished.String()})
			So(err, ShouldBeNil)
			So(payload.Name, ShouldEqual, "本体")
		})

		Convey("编辑中但有已发布 release：取 release", func() {
			payload, err := newSync(nil, withRelease).skillIndexPayload(context.Background(),
				&model.SkillRepositoryDB{SkillID: "skill-1", Status: interfaces.BizStatusEditing.String()})
			So(err, ShouldBeNil)
			So(payload.Name, ShouldEqual, "已发布版")
		})

		Convey("编辑中且从未发布：不入索引", func() {
			payload, err := newSync(nil, withoutRelease).skillIndexPayload(context.Background(),
				&model.SkillRepositoryDB{SkillID: "skill-1", Status: interfaces.BizStatusEditing.String()})
			So(err, ShouldBeNil)
			So(payload, ShouldBeNil)
		})

		Convey("已删除：不入索引", func() {
			payload, err := newSync(nil, withRelease).skillIndexPayload(context.Background(),
				&model.SkillRepositoryDB{SkillID: "skill-1", IsDeleted: true,
					Status: interfaces.BizStatusPublished.String()})
			So(err, ShouldBeNil)
			So(payload, ShouldBeNil)
		})
	})
}
