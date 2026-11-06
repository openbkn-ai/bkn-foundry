// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func (s *draftService) commitModelDefinitions(ctx context.Context, entry *interfaces.ProjectEntry, draft *interfaces.Draft, files map[string][]byte, owned []string, message string, author interfaces.GitAuthor) (*interfaces.CommitDefinitionsResult, error) {
	modelService, available := s.ls.(interfaces.ModelLineageService)
	if !available {
		if _, err := documentHooks().DefinitionRules(ctx, draft, false); err != nil {
			return nil, err
		}
		return s.ls.AddDefinitions(ctx, entry.KNID, entry.Branch, files, owned, message, author)
	}
	rules, err := documentHooks().DefinitionRules(ctx, draft, true)
	if err != nil {
		return nil, err
	}
	return modelService.AddModelDefinitions(ctx, entry.KNID, entry.Branch, files, rules, owned, message, author)
}
