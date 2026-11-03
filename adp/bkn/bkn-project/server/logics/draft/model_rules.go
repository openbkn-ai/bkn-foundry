// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbpublication"
)

func (s *draftService) commitModelDefinitions(ctx context.Context, entry *interfaces.ProjectEntry, draft *interfaces.Draft, files map[string][]byte, owned []string, message string, author interfaces.GitAuthor) (*interfaces.CommitDefinitionsResult, error) {
	modelService, available := s.ls.(interfaces.ModelLineageService)
	if !available {
		if adoptedDocuments(draft) {
			return nil, datasetNotReady(ctx, http.StatusServiceUnavailable)
		}
		return s.ls.AddDefinitions(ctx, entry.KNID, entry.Branch, files, owned, message, author)
	}
	var rules []byte
	if adoptedDocuments(draft) {
		var err error
		rules, err = kbpublication.Definition(draft.Items)
		if err != nil {
			return nil, err
		}
	}
	return modelService.AddModelDefinitions(ctx, entry.KNID, entry.Branch, files, rules, owned, message, author)
}
