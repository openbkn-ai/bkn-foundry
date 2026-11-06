// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package docdraft

import (
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
)

func documentExportItems(items *interfaces.DraftItems, evidence *interfaces.DraftEvidenceExport) (*interfaces.DraftItems, error) {
	return kbdataset.ExportItems(items, evidence)
}
