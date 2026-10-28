// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
	"go.uber.org/mock/gomock"
)

type deleteMetricAccessStub struct {
	interfaces.ObjectMetricAccessV1
	interfaces.ObjectMetricArchiveAccessV1
	listIDs       func(context.Context, interfaces.ObjectMetricListQueryV1) ([]string, error)
	deleteArchive func(context.Context, *sql.Tx, string, string) error
}

func (s *deleteMetricAccessStub) ListObjectMetricIDs(ctx context.Context, query interfaces.ObjectMetricListQueryV1) ([]string, error) {
	return s.listIDs(ctx, query)
}
func (s *deleteMetricAccessStub) DeleteObjectMetricArchive(ctx context.Context, tx *sql.Tx, knID, branch string) error {
	return s.deleteArchive(ctx, tx, knID, branch)
}

func TestDeleteKNWithoutMetricsSkipsMetricAuthorization(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := bmock.NewMockPermissionService(ctrl)
	ats := bmock.NewMockActionTypeService(ctrl)
	previous := logics.OMA
	t.Cleanup(func() { logics.OMA = previous })
	logics.OMA = &deleteMetricAccessStub{
		listIDs: func(context.Context, interfaces.ObjectMetricListQueryV1) ([]string, error) {
			return nil, nil
		},
	}
	// Stop at the next resource type; no Safe call should target an empty metric set.
	ats.EXPECT().GetActionTypeIDsByKnID(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH).
		Return(nil, errors.New("stop after metric cleanup"))
	service := &knowledgeNetworkService{ps: ps, ats: ats}
	if err := service.cleanupKNChildren(context.Background(), &interfaces.KN{KNID: "kn-1", Branch: interfaces.MAIN_BRANCH}); err == nil {
		t.Fatal("expected the next resource lookup to fail")
	}
}

func TestDeleteKNMetricCleanupFailureLeavesBusinessRecordsForRetry(t *testing.T) {
	for _, failure := range []string{"lookup", "policies", "parents"} {
		t.Run(failure, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			db, sqlMock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			ps := bmock.NewMockPermissionService(ctrl)
			injected := errors.New("metric cleanup failed")
			previous := logics.OMA
			t.Cleanup(func() { logics.OMA = previous })
			lookupCount := 0
			logics.OMA = &deleteMetricAccessStub{listIDs: func(ctx context.Context, query interfaces.ObjectMetricListQueryV1) ([]string, error) {
				lookupCount++
				if want := (interfaces.ObjectMetricListQueryV1{KNID: "kn-1", Branch: interfaces.MAIN_BRANCH}); !reflect.DeepEqual(query, want) {
					t.Fatalf("metric query = %#v, want %#v", query, want)
				}
				if db.Stats().InUse != 0 {
					t.Fatal("metric cleanup holds a business transaction")
				}
				if failure == "lookup" {
					return nil, injected
				}
				return []string{"metric-1"}, nil
			}}
			// Retrying must repeat preflight while the source records still exist.
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), []string{interfaces.OPERATION_TYPE_DELETE}).Return(nil).Times(2)
			ids := []string{"kn-1/metric-1"}
			if failure == "policies" {
				ps.EXPECT().DeleteResources(gomock.Any(), interfaces.RESOURCE_TYPE_METRIC, ids).Return(injected).Times(2)
			} else if failure == "parents" {
				ps.EXPECT().DeleteResources(gomock.Any(), interfaces.RESOURCE_TYPE_METRIC, ids).Return(nil).Times(2)
				ps.EXPECT().DeleteResourceParents(gomock.Any(), interfaces.RESOURCE_TYPE_METRIC, ids).Return(injected).Times(2)
			}
			svc := &knowledgeNetworkService{db: db, ps: ps}
			for i := 0; i < 2; i++ {
				if err := svc.DeleteKN(context.Background(), &interfaces.KN{KNID: "kn-1", Branch: interfaces.MAIN_BRANCH}); err == nil {
					t.Fatal("DeleteKN succeeded after metric cleanup failed")
				}
			}
			if lookupCount != 2 {
				t.Fatalf("metric lookups = %d, want 2", lookupCount)
			}
			// No BEGIN, metric archive deletion, or network deletion is expected.
			if err := sqlMock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
