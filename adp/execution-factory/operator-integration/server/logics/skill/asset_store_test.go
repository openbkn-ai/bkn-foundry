package skill

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

func TestOSSGatewaySkillAssetStoreUploadUsesCurrentStorageIDEvenWhenClientStartsNotReady(t *testing.T) {
	Convey("Upload relies on CurrentStorageID instead of failing early on IsReady", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockClient := mocks.NewMockOSSGatewayBackendClient(ctrl)
		store := &ossGatewaySkillAssetStore{
			client:      mockClient,
			SkillPrefix: "skill/",
		}

		mockClient.EXPECT().CurrentStorageID(gomock.Any()).Return("storage-1", nil)
		mockClient.EXPECT().UploadFile(gomock.Any(), &interfaces.OssObject{
			StorageID:  "storage-1",
			StorageKey: "skill/skill-1/v1/refs/guide.md",
		}, []byte("content")).Return(nil)

		object, checksum, err := store.Upload(context.Background(), "skill-1", "v1", "refs/guide.md", []byte("content"))
		So(err, ShouldBeNil)
		So(object, ShouldResemble, &interfaces.OssObject{
			StorageID:  "storage-1",
			StorageKey: "skill/skill-1/v1/refs/guide.md",
		})
		So(checksum, ShouldEqual, checksumSHA256([]byte("content")))
	})
}

func TestOSSGatewaySkillAssetStoreDownloadFallsBackToCurrentStorageID(t *testing.T) {
	Convey("Download retries with current storage id when stored id is missing", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockClient := mocks.NewMockOSSGatewayBackendClient(ctrl)
		store := &ossGatewaySkillAssetStore{client: mockClient}
		object := &interfaces.OssObject{
			StorageID:  "stale-storage",
			StorageKey: "skill/skill-1/v1/refs/guide.md",
		}

		mockClient.EXPECT().DownloadFile(gomock.Any(), object).
			Return(nil, errors.New("download file failed, respData: storage not found"))
		mockClient.EXPECT().CurrentStorageID(gomock.Any()).Return("storage-current", nil)
		mockClient.EXPECT().DownloadFile(gomock.Any(), &interfaces.OssObject{
			StorageID:  "storage-current",
			StorageKey: object.StorageKey,
		}).Return([]byte("# Guide\n"), nil)

		data, err := store.Download(context.Background(), object)
		So(err, ShouldBeNil)
		So(string(data), ShouldEqual, "# Guide\n")
	})
}
