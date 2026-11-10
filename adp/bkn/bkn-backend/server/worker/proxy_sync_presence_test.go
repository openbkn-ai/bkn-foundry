// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"context"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
	permissionlogic "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/permission"
)

type stillDefined map[string]bool

func (s stillDefined) PresentKNChildIDs(_ context.Context, _, _ string, childIDs []string) ([]string, error) {
	var present []string
	for _, id := range childIDs {
		if s[id] {
			present = append(present, id)
		}
	}
	return present, nil
}

// A main delete reaches Safe through the outbox; a child the draft or a version still defines
// keeps its policy there too, and an event left with nothing to delete still completes.
func TestProxySyncWorkerKeepsPoliciesOfChildrenStillDefined(t *testing.T) {
	permissionlogic.SetKNChildPresence(stillDefined{"object-kept": true})
	t.Cleanup(func() { permissionlogic.SetKNChildPresence(nil) })

	ctrl := gomock.NewController(t)
	proxy := bmock.NewMockManagedProxyAccess(ctrl)
	permission := bmock.NewMockPermissionService(ctrl)
	event := &interfaces.KNProxyOutboxEvent{
		ID: "event-1", KNID: "kn-1", ProxyAccountID: "proxy-1", GrantorID: "editor-1",
		Generation: 2, BaseVersion: "v1", TargetVersion: "v2",
		AuthorizationCleanups: []interfaces.AuthorizationCleanupTarget{
			{ResourceType: interfaces.RESOURCE_TYPE_OBJECT_TYPE, ResourceIDs: []string{"kn-1/object-gone", "kn-1/object-kept"}},
			{ResourceType: interfaces.RESOURCE_TYPE_ACTION_TYPE, ResourceIDs: []string{"kn-1/object-kept"}},
		},
	}
	proxy.EXPECT().SyncGrantDelta(gomock.Any(), "proxy-1", "editor-1", int64(2), "v1", "v2",
		event.Upserts, event.Removals).Return(interfaces.ProxyGrantSyncResult{}, nil)
	permission.EXPECT().DeleteResources(gomock.Any(), interfaces.RESOURCE_TYPE_OBJECT_TYPE,
		[]string{"kn-1/object-gone"}).Return(nil)
	outbox := &proxySyncOutboxStub{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	w := &ProxySyncWorker{outbox: outbox, proxy: proxy, permission: permission,
		lease: time.Minute, maxAttempts: 12, ctx: ctx}
	w.process("pod-1", event)
	if outbox.completed != event || outbox.retriedID != "" {
		t.Fatalf("worker result = completed %#v, retried %q", outbox.completed, outbox.retriedID)
	}
}
