// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"testing"
)

type businessIdentityVega struct {
	recordingVega
	table  *interfaces.VegaTable
	denied bool
	as     interfaces.AccountInfo
}

func (v *businessIdentityVega) GetTables(_ context.Context, account interfaces.VegaAccount, _ []string) ([]*interfaces.VegaTable, error) {
	v.as = account
	if v.denied {
		return nil, errors.New("permission denied")
	}
	return []*interfaces.VegaTable{v.table}, nil
}
func businessIdentityFixture() (*interfaces.Draft, *interfaces.DraftRelationType, *businessIdentityVega) {
	source := &interfaces.DraftObjectType{ID: "agreement_doc", Adopted: true, PrimaryKeys: []string{"number"}, Document: &interfaces.DraftDocumentModel{Role: "entity"}, Properties: []*interfaces.DraftProperty{{Name: "number", Type: "string"}}}
	target := &interfaces.DraftObjectType{ID: "agreement", Adopted: true, PrimaryKeys: []string{"internal_id"}, Table: interfaces.DraftTable{ResourceID: "system-records"}, Properties: []*interfaces.DraftProperty{{Name: "internal_id", Type: "string", MappedField: "id"}, {Name: "number", Type: "string", MappedField: "business_number"}}}
	relation := &interfaces.DraftRelationType{ID: "same_agreement", Adopted: true, Source: source.ID, Target: target.ID, Cardinality: "one_to_one", Document: &interfaces.DraftDocumentModel{Role: "relation"}, DocumentMapping: &interfaces.DraftDocumentMapping{Mode: "linking", MentionProperty: "number", IdentityProperty: "number"}}
	backend := &businessIdentityVega{recordingVega: recordingVega{rows: []map[string]any{{"total": 10, "distinct_values": 10, "nulls": 0, "blanks": 0}}}, table: &interfaces.VegaTable{ID: target.Table.ResourceID, Columns: []interfaces.VegaColumn{{Name: "business_number", Type: "string"}}}}
	return &interfaces.Draft{Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{source, target}, RelationTypes: []*interfaces.DraftRelationType{relation}}}, relation, backend
}
func TestBusinessKeyVerificationUsesCurrentSchemaAndCountsWithoutReplacingPrimaryKey(t *testing.T) {
	for _, scenario := range []string{"valid", "duplicate", "null", "blank", "remapped", "revoked"} {
		t.Run(scenario, func(t *testing.T) {
			draft, relation, backend := businessIdentityFixture()
			caller := interfaces.AccountInfo{ID: "operator", Type: "user"}
			service := &draftService{va: backend}
			switch scenario {
			case "duplicate":
				backend.rows[0]["distinct_values"] = 9
			case "null":
				backend.rows[0]["nulls"] = 1
			case "blank":
				backend.rows[0]["blanks"] = 1
			case "remapped":
				backend.table.Columns[0].Name = "another_field"
			case "revoked":
				backend.denied = true
			}
			proof, err := service.verifyBusinessIdentity(context.Background(), draft, relation, caller)
			if scenario != "valid" {
				var problem *httperrors.HTTPError
				if !errors.As(err, &problem) || proof != nil {
					t.Fatal("unverified business key received proof", err)
				}
				return
			}
			if err != nil || proof == nil || proof.Rows != 10 || proof.ConfirmedBy != caller || backend.as != caller {
				t.Fatal("verification lost current operator or exact counts", err)
			}
			relation.DocumentMapping.BusinessIdentity = proof
			if err = service.checkBusinessIdentities(context.Background(), draft, caller); err != nil {
				t.Fatal(err)
			}
			backend.rows[0]["distinct_values"] = 9
			if err = service.checkBusinessIdentities(context.Background(), draft, caller); err == nil {
				t.Fatal("saved proof bypassed fresh confirmation counts")
			}
			if draft.Items.ObjectTypes[1].PrimaryKeys[0] != "internal_id" {
				t.Fatal("business matching changed the system primary key")
			}
		})
	}
}

func TestForeignReferenceBusinessKeyUsesCurrentOperatorAndRechecksCounts(t *testing.T) {
	for _, role := range []string{"entity", "in_document"} {
		t.Run(role, func(t *testing.T) {
			draft, relation, backend := businessIdentityFixture()
			source := draft.Items.ObjectTypes[0]
			source.Document.Role = role
			source.PrimaryKeys = []string{"invoice_number"}
			source.Properties = append(source.Properties, &interfaces.DraftProperty{Name: "invoice_number", Type: "string"})
			relation.Cardinality = "many_to_one"
			caller := interfaces.AccountInfo{ID: "operator", Type: "user"}
			service := &draftService{va: backend}
			proof, err := service.verifyBusinessIdentity(context.Background(), draft, relation, caller)
			if err != nil || proof == nil || backend.as != caller {
				t.Fatal("reference did not verify as the current operator", err)
			}
			relation.DocumentMapping.BusinessIdentity = proof
			if err = checkSourceFieldDecisions(context.Background(), draft); err != nil {
				t.Fatal("ordinary reference required same-business field authority", err)
			}
			if err = service.checkBusinessIdentities(context.Background(), draft, caller); err != nil {
				t.Fatal(err)
			}
			backend.rows[0]["distinct_values"] = 9
			if err = service.checkBusinessIdentities(context.Background(), draft, caller); err == nil {
				t.Fatal("reference bypassed fresh uniqueness check")
			}
			if draft.Items.ObjectTypes[1].PrimaryKeys[0] != "internal_id" {
				t.Fatal("system primary key replaced")
			}
		})
	}
}
