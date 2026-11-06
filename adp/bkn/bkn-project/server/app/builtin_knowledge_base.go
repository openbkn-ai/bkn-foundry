// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package app

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/docdraft"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbinstance"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbpublication"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbrefresh"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbresource"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/project"
)

// useBuiltinKnowledgeBase registers the knowledge base still in the open core through the same
// hooks and workers an edition uses. It leaves with that knowledge base.
func (server *Application) useBuiltinKnowledgeBase() {
	projecthooks.Register(projecthooks.Hooks{
		Drafts:       docdraft.Hooks{Resources: logics.KBRA},
		Publications: kbpublication.Hooks{},
		Lineages:     kbrefresh.Hooks{Refreshes: logics.KBRefreshes},
		Projects:     kbresource.Hooks{Projects: project.NewProjectService(), Resources: logics.KBRA},
	})
	server.workers = append(server.workers,
		func(ctx context.Context) { kbinstance.RunBuildWorker(ctx, logics.KBBA, logics.DA, logics.KBRefs) },
		func(ctx context.Context) {
			settings := server.appSetting.KBRefreshSetting
			kbrefresh.RunWorker(ctx, &kbrefresh.Limits{MaxChangedPercent: settings.MaxChangedPercent, MaxRemovedPercent: settings.MaxRemovedPercent, MaxDurationMillis: settings.MaxDurationSeconds * 1000})
		})
}
