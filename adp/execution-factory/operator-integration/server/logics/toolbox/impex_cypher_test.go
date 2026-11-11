// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package toolbox

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
)

const (
	impexCypherBoxID     = "box-cypher"
	impexCypherToolID    = "tool-cypher"
	impexCypherVersion   = "function-version-1"
	impexCypherKnID      = "kn-supply"
	impexCypherStatement = "MATCH (s:supplier {code: $code}) RETURN count(s)"
)

// impexCypherService is a toolbox service whose validators accept everything,
// so a test sees only what export writes and import reads.
func impexCypherService(t *testing.T) (*ToolServiceImpl, *mocks.MockIToolDB, *mocks.MockIMetadataService) {
	t.Helper()
	ctrl := gomock.NewController(t)
	toolDB := mocks.NewMockIToolDB(ctrl)
	metadataService := mocks.NewMockIMetadataService(ctrl)
	categories := mocks.NewMockCategoryManager(ctrl)
	categories.EXPECT().GetCategoryName(gomock.Any(), gomock.Any()).Return("other").AnyTimes()
	categories.EXPECT().CheckCategory(gomock.Any()).Return(true).AnyTimes()
	validator := mocks.NewMockValidator(ctrl)
	validator.EXPECT().ValidatorStruct(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	validator.EXPECT().ValidatorToolBoxName(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	validator.EXPECT().ValidatorToolBoxDesc(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	validator.EXPECT().ValidatorToolName(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	validator.EXPECT().ValidatorToolDesc(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	return &ToolServiceImpl{
		ToolDB:          toolDB,
		MetadataService: metadataService,
		CategoryManager: categories,
		Validator:       validator,
		Logger:          logger.DefaultLogger(),
	}, toolDB, metadataService
}

// exportCypherBox exports a function toolbox holding one Cypher function and
// returns the toolbox as an export file carries it, after JSON encoding.
func exportCypherBox(t *testing.T, shape interfaces.CypherResultShape) (*ToolServiceImpl, *interfaces.ToolBoxImpexItem) {
	t.Helper()
	svc, toolDB, metadataService := impexCypherService(t)
	toolDB.EXPECT().SelectToolBoxByIDs(gomock.Any(), []string{impexCypherBoxID}).Return([]*model.ToolDB{{
		ToolID:     impexCypherToolID,
		BoxID:      impexCypherBoxID,
		Name:       "supplier_count",
		SourceType: model.SourceTypeFunction,
		SourceID:   impexCypherVersion,
		Status:     string(interfaces.ToolStatusTypeEnabled),
		Parameters: `{}`,
	}}, nil)
	metadataService.EXPECT().BatchGetMetadataBySourceIDs(gomock.Any(), gomock.Any()).Return(
		map[string]interfaces.IMetadataDB{impexCypherVersion: &model.FunctionMetadataDB{
			Version:     impexCypherVersion,
			Summary:     "supplier_count",
			Path:        "/",
			Method:      http.MethodPost,
			APISpec:     `{}`,
			ScriptType:  string(interfaces.ScriptTypeCypher),
			Code:        impexCypherStatement,
			KnID:        impexCypherKnID,
			ResultShape: string(shape),
		}}, nil)

	exported, err := svc.batchGetToolBoxInfo(context.Background(), []*model.ToolboxDB{{
		BoxID:        impexCypherBoxID,
		Name:         "suppliers",
		Description:  "supplier functions",
		MetadataType: string(interfaces.MetadataTypeFunc),
	}})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	encoded, err := json.Marshal(exported)
	if err != nil {
		t.Fatalf("encode export: %v", err)
	}
	file := &interfaces.ToolBoxImpexConfig{}
	if err = json.Unmarshal(encoded, file); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if len(file.Configs) != 1 || len(file.Configs[0].Tools) != 1 {
		t.Fatalf("export file = %s, want one toolbox with one tool", encoded)
	}
	return svc, file.Configs[0]
}

// A Cypher function exported and imported again keeps what it needs to run:
// its network, its result shape and its statement. Losing kn_id or
// result_shape on the way would make the import refuse the package, or the
// function fail only when it is called.
func TestCypherFunctionSurvivesExportAndImport(t *testing.T) {
	for _, shape := range []interfaces.CypherResultShape{
		interfaces.CypherResultShapeValue,
		interfaces.CypherResultShapeTable,
	} {
		t.Run(string(shape), func(t *testing.T) {
			svc, box := exportCypherBox(t, shape)
			tool := box.Tools[0]
			if tool.KnID != impexCypherKnID || tool.ResultShape != shape {
				t.Fatalf("exported kn_id=%q result_shape=%q, want %q %q", tool.KnID, tool.ResultShape, impexCypherKnID, shape)
			}

			_, metadataList, err := svc.importCheck(context.Background(), box, "importer")
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			if len(metadataList) != 1 {
				t.Fatalf("imported %d metadata rows, want 1", len(metadataList))
			}
			function, ok := metadataList[0].(*model.FunctionMetadataDB)
			if !ok {
				t.Fatalf("imported metadata is %T, want *model.FunctionMetadataDB", metadataList[0])
			}
			if !function.IsCypher() || function.Code != impexCypherStatement {
				t.Fatalf("imported script_type=%q code=%q, want a Cypher function with the exported statement",
					function.ScriptType, function.Code)
			}
			if function.KnID != impexCypherKnID || function.ResultShape != string(shape) {
				t.Fatalf("imported kn_id=%q result_shape=%q, want %q %q",
					function.KnID, function.ResultShape, impexCypherKnID, shape)
			}
			// A Cypher function installs nothing: no dependency list is stored for it.
			if function.Dependencies != "" {
				t.Fatalf("imported dependencies = %q, want none", function.Dependencies)
			}
		})
	}
}

// A package edited by hand, or written by an older export, can lack what a
// Cypher function needs. The import refuses it up front, naming the function,
// instead of storing a function that cannot run.
func TestImportRefusesAnIncompleteCypherFunction(t *testing.T) {
	cases := []struct {
		name   string
		damage func(item *interfaces.ToolImpexItem)
		want   string
	}{
		{"no network", func(item *interfaces.ToolImpexItem) { item.KnID = "" }, "has no kn_id"},
		{"no result shape", func(item *interfaces.ToolImpexItem) { item.ResultShape = "" }, "invalid result_shape"},
		{"unknown result shape", func(item *interfaces.ToolImpexItem) { item.ResultShape = "rows" }, "invalid result_shape"},
		{"no statement", func(item *interfaces.ToolImpexItem) { item.Code = "  " }, "has no statement"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, box := exportCypherBox(t, interfaces.CypherResultShapeValue)
			tc.damage(box.Tools[0])

			_, _, err := svc.importCheck(context.Background(), box, "importer")
			if err == nil {
				t.Fatal("import accepted the function, want it refused")
			}
			httpErr, ok := err.(*httperrors.HTTPError)
			if !ok || httpErr.HTTPCode != http.StatusBadRequest {
				t.Fatalf("err = %v, want a 400", err)
			}
			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "supplier_count") {
				t.Fatalf("err = %v, want it to say %q and name the function", err, tc.want)
			}
		})
	}
}
