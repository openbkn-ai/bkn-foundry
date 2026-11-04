// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package draft

import (
	"context"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"testing"
)

func TestOwnedDocumentDatasetsDelegateSQLCountsWithoutInventingStatistics(t *testing.T) {
	for _, scenario := range []string{"ready", "unprepared", "foreign_binding", "foreign_catalog", "unready_dataset", "structured", "excluded"} {
		t.Run(scenario, func(t *testing.T) {
			items := checkFixture()
			object := items.ObjectTypes[0]
			object.Document = &interfaces.DraftDocumentModel{Role: "entity"}
			relation := items.RelationTypes[1]
			relation.Document = &interfaces.DraftDocumentModel{Role: "relation"}
			items.RelationTypes = []*interfaces.DraftRelationType{relation}
			items.DatasetPlans = []*interfaces.DraftDatasetPlan{{State: "ready", Datasets: []interfaces.DraftDatasetSpec{
				{ObjectTypeID: object.ID, ResourceID: object.Table.ResourceID, CatalogID: object.Table.CatalogID, Ready: true},
				{RelationTypeID: relation.ID, ResourceID: relation.LinkTable.ResourceID, CatalogID: relation.LinkTable.CatalogID, Ready: true},
			}}}
			switch scenario {
			case "unprepared":
				items.DatasetPlans[0].State = "preparing"
			case "foreign_binding":
				for i := range items.DatasetPlans[0].Datasets {
					items.DatasetPlans[0].Datasets[i].ResourceID = "foreign"
				}
			case "foreign_catalog":
				for i := range items.DatasetPlans[0].Datasets {
					items.DatasetPlans[0].Datasets[i].CatalogID = "foreign"
				}
			case "unready_dataset":
				for i := range items.DatasetPlans[0].Datasets {
					items.DatasetPlans[0].Datasets[i].Ready = false
				}
			case "structured":
				object.Document = nil
				relation.Document = nil
			case "excluded":
				items.Excluded = []*interfaces.DraftExcludedField{{ObjectType: object.ID, Field: object.Properties[0].MappedField}}
			}
			counter := &fakeCounter{failed: map[string]bool{object.Table.ResourceID: true, relation.LinkTable.ResourceID: true}}
			results := runCheck(context.Background(), counter, checkInputOf(items), nil)
			delegated := scenario == "ready" || scenario == "excluded"
			key := results.Keys[0]
			if delegated {
				if key.State != checkNotCounted || key.Reason != "knowledge_base_build" || key.Rows != 0 || key.Duplicates != 0 || key.Nulls != 0 {
					t.Fatal("Dataset received fabricated SQL statistics", key)
				}
				for _, j := range results.Relations[0].Joins {
					if j.State != checkNotCounted || j.Reason != "knowledge_base_build" {
						t.Fatal("Dataset join used physical-table SQL", j)
					}
				}
				if results.Keys[1].State != checkCounted {
					t.Fatal("structured table lost its SQL check")
				}
				if scenario == "ready" && checkStatus(results) != interfaces.CHECK_STATUS_PARTIAL {
					t.Fatal("unmeasured Dataset was claimed as a full SQL pass", checkStatus(results))
				}
				if scenario == "excluded" && len(results.ExcludedMapped) == 0 {
					t.Fatal("delegation bypassed excluded-field review")
				}
			} else if key.State != checkFailed {
				t.Fatal("unprepared, foreign or structured resource bypassed SQL validation", key)
			}
		})
	}
}
