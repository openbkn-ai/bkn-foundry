package skill

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	"go.uber.org/mock/gomock"
)

func TestReconciledSkillCannotResurrectAfterDeleteOrOffline(t *testing.T) {
	for _, offline := range []bool{false, true} {
		name := "deleted"
		if offline {
			name = "offline"
		}
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			index := mocks.NewMockCapabilityIndexSyncService(ctrl)
			repo := mocks.NewMockISkillRepository(ctrl)
			started, release := make(chan struct{}), make(chan struct{})
			var mu sync.Mutex
			var current *model.SkillRepositoryDB
			indexed := false
			index.EXPECT().UpsertCapability(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ *interfaces.CapabilityDocument) error {
				close(started)
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
				mu.Lock()
				indexed = true
				mu.Unlock()
				return nil
			})
			repo.EXPECT().SelectSkillByID(gomock.Any(), gomock.Nil(), "skill").DoAndReturn(func(context.Context, any, string) (*model.SkillRepositoryDB, error) {
				mu.Lock()
				defer mu.Unlock()
				return current, nil
			})
			index.EXPECT().DeleteCapability(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, interfaces.CapabilityRef) error {
				mu.Lock()
				indexed = false
				mu.Unlock()
				return nil
			})
			syncer := &skillIndexSync{capabilitySync: index, skillRepo: repo}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- syncer.upsertReconciledSkill(ctx, &interfaces.CapabilityDocument{CapabilityRef: skillCapabilityRef("skill"), Name: "old", Version: "v1"})
			}()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("upsert did not start")
			}
			// Model a source transaction committing delete/offline and purging its old index
			// while the background embedding/write remains blocked.
			mu.Lock()
			if offline {
				current = &model.SkillRepositoryDB{SkillID: "skill", Status: string(interfaces.BizStatusOffline)}
			}
			indexed = false
			mu.Unlock()
			close(release)
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("reconciliation did not finish")
			}
			mu.Lock()
			defer mu.Unlock()
			if indexed {
				t.Fatal("stale upsert resurrected the purged skill")
			}
		})
	}
}

func TestReconciledSkillCleanupSurvivesPassCancellation(t *testing.T) {
	ctrl := gomock.NewController(t)
	index := mocks.NewMockCapabilityIndexSyncService(ctrl)
	repo := mocks.NewMockISkillRepository(ctrl)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	index.EXPECT().UpsertCapability(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, *interfaces.CapabilityDocument) error { cancel(); return nil })
	repo.EXPECT().SelectSkillByID(gomock.Any(), gomock.Nil(), "skill").DoAndReturn(func(checkCtx context.Context, _ any, _ string) (*model.SkillRepositoryDB, error) {
		if checkCtx.Err() != nil {
			t.Error("source recheck inherited expired pass")
		}
		if deadline, ok := checkCtx.Deadline(); !ok || time.Until(deadline) > skillIndexOperationTimeout {
			t.Error("source recheck is not bounded")
		}
		return nil, nil
	})
	index.EXPECT().DeleteCapability(gomock.Any(), gomock.Any()).DoAndReturn(func(checkCtx context.Context, _ interfaces.CapabilityRef) error {
		if checkCtx.Err() != nil {
			t.Error("stale index purge inherited expired pass")
		}
		return nil
	})
	syncer := &skillIndexSync{capabilitySync: index, skillRepo: repo}
	if err := syncer.upsertReconciledSkill(ctx, &interfaces.CapabilityDocument{CapabilityRef: skillCapabilityRef("skill")}); err != nil {
		t.Fatal(err)
	}
}

func TestSkillCapabilityNotificationsCoalesce(t *testing.T) {
	for len(skillCapabilitiesChanged) > 0 {
		<-skillCapabilitiesChanged
	}
	defer func() {
		for len(skillCapabilitiesChanged) > 0 {
			<-skillCapabilitiesChanged
		}
	}()
	for i := 0; i < 100; i++ {
		notifySkillCapabilitiesChanged()
	}
	if len(skillCapabilitiesChanged) != 1 {
		t.Fatal("committed notifications did not coalesce")
	}
}

func TestSkillReconcilerDisabledPeriodStillIndexesCommittedNotifications(t *testing.T) {
	for len(skillCapabilitiesChanged) > 0 {
		<-skillCapabilitiesChanged
	}
	ctrl := gomock.NewController(t)
	index := mocks.NewMockCapabilityIndexSyncService(ctrl)
	repo := mocks.NewMockISkillRepository(ctrl)
	invoked := make(chan struct{}, 1)
	index.EXPECT().EnsureInitialized(gomock.Any()).DoAndReturn(func(ctx context.Context) error {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > skillIndexOperationTimeout {
			t.Error("background pass lacks deadline")
		}
		invoked <- struct{}{}
		return nil
	})
	repo.EXPECT().SelectSkillBuildPage(gomock.Any(), gomock.Nil(), int64(0), "", skillIndexBuildBatchSize).Return(nil, nil)
	index.EXPECT().ListIndexed(gomock.Any(), interfaces.CapabilityTypeSkill).Return(nil, nil)
	syncer := &skillIndexSync{capabilitySync: index, skillRepo: repo, releaseRepo: &stubSkillReleaseRepo{}, logger: logger.DefaultLogger()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan struct{})
	go func() { defer close(stopped); syncer.runCapabilityReconciler(ctx, 0) }()
	select {
	case <-invoked:
		t.Fatal("disabled periodic worker performed a startup scan")
	case <-time.After(30 * time.Millisecond):
	}
	notifySkillCapabilitiesChanged()
	select {
	case <-invoked:
	case <-time.After(time.Second):
		t.Fatal("committed notification was ignored")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("event-only worker did not stop")
	}
}
