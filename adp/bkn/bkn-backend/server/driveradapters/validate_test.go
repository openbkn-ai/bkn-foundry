// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	. "github.com/smartystreets/goconvey/convey"

	cond "bkn-backend/common/condition"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

func Test_validateImportMode(t *testing.T) {
	Convey("Test validateImportMode\n", t, func() {
		ctx := context.Background()

		Convey("Success with normal mode\n", func() {
			err := validateImportMode(ctx, interfaces.ImportMode_Normal)
			So(err, ShouldBeNil)
		})

		Convey("Success with ignore mode\n", func() {
			err := validateImportMode(ctx, interfaces.ImportMode_Ignore)
			So(err, ShouldBeNil)
		})

		Convey("Success with overwrite mode\n", func() {
			err := validateImportMode(ctx, interfaces.ImportMode_Overwrite)
			So(err, ShouldBeNil)
		})

		Convey("Failed with invalid mode\n", func() {
			httpErr := validateImportMode(ctx, "invalid_mode")
			So(httpErr, ShouldNotBeNil)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, berrors.BknBackend_InvalidParameter_ImportMode)
		})
	})
}

func Test_validateObjectName(t *testing.T) {
	Convey("Test validateObjectName\n", t, func() {
		ctx := context.Background()

		Convey("Success with valid name\n", func() {
			err := validateObjectName(ctx, "test_name", interfaces.MODULE_TYPE_OBJECT_TYPE)
			So(err, ShouldBeNil)
		})

		Convey("Failed with empty name\n", func() {
			err := validateObjectName(ctx, "", interfaces.MODULE_TYPE_OBJECT_TYPE)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, berrors.BknBackend_ObjectType_NullParameter_Name)
		})

		Convey("Failed with name too long\n", func() {
			longName := ""
			for i := 0; i < interfaces.OBJECT_NAME_MAX_LENGTH+1; i++ {
				longName += "a"
			}
			err := validateObjectName(ctx, longName, interfaces.MODULE_TYPE_OBJECT_TYPE)
			So(err, ShouldNotBeNil)
			httpErr, ok := err.(*rest.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, berrors.BknBackend_ObjectType_LengthExceeded_Name)
		})
	})
}

func Test_ValidateTags(t *testing.T) {
	Convey("Test ValidateTags\n", t, func() {
		ctx := context.Background()

		Convey("Success with valid tags\n", func() {
			tags := []string{"tag1", "tag2"}
			err := ValidateTags(ctx, tags)
			So(err, ShouldBeNil)
		})

		Convey("Success with empty tags\n", func() {
			tags := []string{}
			err := ValidateTags(ctx, tags)
			So(err, ShouldBeNil)
		})

		Convey("Failed with too many tags\n", func() {
			tags := make([]string, interfaces.TAGS_MAX_NUMBER+1)
			for i := 0; i < interfaces.TAGS_MAX_NUMBER+1; i++ {
				tags[i] = "tag"
			}
			err := ValidateTags(ctx, tags)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, berrors.BknBackend_CountExceeded_TagTotal)
		})

		Convey("Failed with invalid tag name\n", func() {
			tags := []string{""}
			err := ValidateTags(ctx, tags)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})
	})
}

func Test_validateDataTagName(t *testing.T) {
	Convey("Test validateDataTagName\n", t, func() {
		ctx := context.Background()

		Convey("Success with valid tag name\n", func() {
			err := validateDataTagName(ctx, "valid_tag")
			So(err, ShouldBeNil)
		})

		Convey("Failed with empty tag name\n", func() {
			err := validateDataTagName(ctx, "")
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with tag name too long\n", func() {
			longTag := ""
			for i := 0; i < interfaces.OBJECT_NAME_MAX_LENGTH+1; i++ {
				longTag += "a"
			}
			err := validateDataTagName(ctx, longTag)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with invalid characters\n", func() {
			err := validateDataTagName(ctx, "tag/name")
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})
	})
}

func Test_validatePaginationQueryParameters(t *testing.T) {
	Convey("Test validatePaginationQueryParameters\n", t, func() {
		ctx := context.Background()
		supportedSortTypes := interfaces.OBJECT_TYPE_SORT

		Convey("Success with valid parameters\n", func() {
			params, err := validatePaginationQueryParameters(ctx, "0", "10", "name", "desc", supportedSortTypes)
			So(err, ShouldBeNil)
			So(params.Offset, ShouldEqual, 0)
			So(params.Limit, ShouldEqual, 10)
		})

		Convey("Failed with invalid offset\n", func() {
			_, err := validatePaginationQueryParameters(ctx, "invalid", "10", "name", "desc", supportedSortTypes)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with negative offset\n", func() {
			_, err := validatePaginationQueryParameters(ctx, "-1", "10", "name", "desc", supportedSortTypes)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with invalid limit\n", func() {
			_, err := validatePaginationQueryParameters(ctx, "0", "invalid", "name", "desc", supportedSortTypes)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with invalid sort\n", func() {
			_, err := validatePaginationQueryParameters(ctx, "0", "10", "invalid_sort", "desc", supportedSortTypes)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with invalid direction\n", func() {
			_, err := validatePaginationQueryParameters(ctx, "0", "10", "name", "invalid", supportedSortTypes)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Success with NO_LIMIT\n", func() {
			params, err := validatePaginationQueryParameters(ctx, "0", interfaces.NO_LIMIT, "name", "desc", supportedSortTypes)
			So(err, ShouldBeNil)
			So(params.Limit, ShouldEqual, -1)
		})
	})
}

func Test_validateID(t *testing.T) {
	Convey("Test validateID\n", t, func() {
		ctx := context.Background()

		Convey("Success with valid ID\n", func() {
			err := validateID(ctx, "valid_id123")
			So(err, ShouldBeNil)
		})

		Convey("Success with empty ID\n", func() {
			err := validateID(ctx, "")
			So(err, ShouldBeNil)
		})

		Convey("Failed with ID starting with underscore\n", func() {
			err := validateID(ctx, "_invalid_id")
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with ID containing invalid characters\n", func() {
			err := validateID(ctx, "invalid@id")
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with ID too long\n", func() {
			longID := ""
			for i := 0; i < 41; i++ {
				longID += "a"
			}
			err := validateID(ctx, longID)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})
	})
}

func Test_ValidateHeaderMethodOverride(t *testing.T) {
	Convey("Test ValidateHeaderMethodOverride\n", t, func() {
		ctx := context.Background()

		Convey("Success with GET\n", func() {
			err := ValidateHeaderMethodOverride(ctx, "GET")
			So(err, ShouldBeNil)
		})

		Convey("Failed with empty method\n", func() {
			err := ValidateHeaderMethodOverride(ctx, "")
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with invalid method\n", func() {
			err := ValidateHeaderMethodOverride(ctx, "POST")
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})
	})
}

func Test_validateConceptsQuery(t *testing.T) {
	Convey("Test validateConceptsQuery\n", t, func() {
		ctx := context.Background()

		Convey("Success with valid query\n", func() {
			query := &interfaces.ConceptsQuery{
				KNID:       "kn1",
				ModuleType: interfaces.MODULE_TYPE_OBJECT_TYPE,
				Condition: map[string]any{
					"operation": "==",
					"field":     "test",
					"value":     "value",
				},
			}
			err := validateConceptsQuery(ctx, query)
			So(err, ShouldBeNil)
			So(query.ActualCondition, ShouldNotBeNil)
		})

		Convey("Success with nil condition\n", func() {
			query := &interfaces.ConceptsQuery{
				KNID:       "kn1",
				ModuleType: interfaces.MODULE_TYPE_OBJECT_TYPE,
				Condition:  nil,
			}
			err := validateConceptsQuery(ctx, query)
			So(err, ShouldBeNil)
		})

		Convey("Failed with invalid condition format\n", func() {
			query := &interfaces.ConceptsQuery{
				KNID:       "kn1",
				ModuleType: interfaces.MODULE_TYPE_OBJECT_TYPE,
				Condition: map[string]any{
					"invalid": make(chan int), // Non-serializable type.
				},
			}
			err := validateConceptsQuery(ctx, query)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})
	})
}

func Test_validateCond(t *testing.T) {
	Convey("Test validateCond\n", t, func() {
		ctx := context.Background()

		Convey("Success with nil condition\n", func() {
			err := validateCond(ctx, nil)
			So(err, ShouldBeNil)
		})

		Convey("Success with valid AND condition\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationAnd,
				SubConds: []*cond.CondCfg{
					{
						Operation: cond.OperationEq,
						Field:     "field1",
						ValueOptCfg: cond.ValueOptCfg{
							Value: "value1",
						},
					},
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldBeNil)
		})

		Convey("Failed with empty operation\n", func() {
			cfg := &cond.CondCfg{
				Operation: "",
				Field:     "field1",
				ValueOptCfg: cond.ValueOptCfg{
					Value: "value1",
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with invalid operation\n", func() {
			cfg := &cond.CondCfg{
				Operation: "invalid_operation",
				Field:     "field1",
				ValueOptCfg: cond.ValueOptCfg{
					Value: "value1",
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with empty name for eq operation\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationEq,
				Field:     "",
				ValueOptCfg: cond.ValueOptCfg{
					Value: "value1",
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with array value for eq operation\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationEq,
				Field:     "field1",
				ValueOptCfg: cond.ValueOptCfg{
					Value: []any{"value1", "value2"},
				},
			}
			// ValueOptCfg is embedded, so Value can be accessed directly.
			err := validateCond(ctx, cfg)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with non-array value for in operation\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationIn,
				Field:     "field1",
				ValueOptCfg: cond.ValueOptCfg{
					Value: "value1",
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with empty array for in operation\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationIn,
				Field:     "field1",
				ValueOptCfg: cond.ValueOptCfg{
					Value: []any{},
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with invalid range array length\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationRange,
				Field:     "field1",
				ValueOptCfg: cond.ValueOptCfg{
					Value: []any{1},
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with invalid regex\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationRegex,
				Field:     "field1",
				ValueOptCfg: cond.ValueOptCfg{
					Value: "[invalid regex",
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Success with OperationOr condition\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationOr,
				SubConds: []*cond.CondCfg{
					{
						Operation: cond.OperationEq,
						Field:     "field1",
						ValueOptCfg: cond.ValueOptCfg{
							Value: "value1",
						},
					},
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldBeNil)
		})

		Convey("Success with OperationKNN condition\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationKNN,
				SubConds: []*cond.CondCfg{
					{
						Operation: cond.OperationEq,
						Field:     "field1",
						ValueOptCfg: cond.ValueOptCfg{
							Value: "value1",
						},
					},
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldBeNil)
		})

		Convey("Failed with too many sub conditions\n", func() {
			subConds := make([]*cond.CondCfg, cond.MaxSubCondition+1)
			for i := 0; i < cond.MaxSubCondition+1; i++ {
				subConds[i] = &cond.CondCfg{
					Operation: cond.OperationEq,
					Field:     "field1",
					ValueOptCfg: cond.ValueOptCfg{
						Value: "value1",
					},
				}
			}
			cfg := &cond.CondCfg{
				Operation: cond.OperationAnd,
				SubConds:  subConds,
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Success with OperationMultiMatch without name\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationMultiMatch,
				Field:     "",
				ValueOptCfg: cond.ValueOptCfg{
					Value: "value1",
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldBeNil)
		})

		Convey("Failed with Like operation having non-string value\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationLike,
				Field:     "field1",
				ValueOptCfg: cond.ValueOptCfg{
					Value: 123,
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with NotLike operation having non-string value\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationNotLike,
				Field:     "field1",
				ValueOptCfg: cond.ValueOptCfg{
					Value: 123,
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with Prefix operation having non-string value\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationPrefix,
				Field:     "field1",
				ValueOptCfg: cond.ValueOptCfg{
					Value: 123,
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with NotPrefix operation having non-string value\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationNotPrefix,
				Field:     "field1",
				ValueOptCfg: cond.ValueOptCfg{
					Value: 123,
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Success with Like operation having string value\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationLike,
				Field:     "field1",
				ValueOptCfg: cond.ValueOptCfg{
					Value: "value1",
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldBeNil)
		})

		Convey("Failed with NotIn operation having non-array value\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationNotIn,
				Field:     "field1",
				ValueOptCfg: cond.ValueOptCfg{
					Value: "value1",
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with NotIn operation having empty array\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationNotIn,
				Field:     "field1",
				ValueOptCfg: cond.ValueOptCfg{
					Value: []any{},
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with OutRange operation having non-array value\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationOutRange,
				Field:     "field1",
				ValueOptCfg: cond.ValueOptCfg{
					Value: "value1",
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with OutRange operation having wrong array length\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationOutRange,
				Field:     "field1",
				ValueOptCfg: cond.ValueOptCfg{
					Value: []any{1},
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with Before operation having non-array value\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationBefore,
				Field:     "field1",
				ValueOptCfg: cond.ValueOptCfg{
					Value: "value1",
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with Before operation having wrong array length\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationBefore,
				Field:     "field1",
				ValueOptCfg: cond.ValueOptCfg{
					Value: []any{1},
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with Between operation having non-array value\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationBetween,
				Field:     "field1",
				ValueOptCfg: cond.ValueOptCfg{
					Value: "value1",
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Failed with Between operation having wrong array length\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationBetween,
				Field:     "field1",
				ValueOptCfg: cond.ValueOptCfg{
					Value: []any{1},
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("Success with Range operation having correct array length\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationRange,
				Field:     "field1",
				ValueOptCfg: cond.ValueOptCfg{
					Value: []any{1, 2},
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldBeNil)
		})

		Convey("Success with OutRange operation having correct array length\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationOutRange,
				Field:     "field1",
				ValueOptCfg: cond.ValueOptCfg{
					Value: []any{1, 2},
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldBeNil)
		})

		Convey("Success with Before operation having correct array length\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationBefore,
				Field:     "field1",
				ValueOptCfg: cond.ValueOptCfg{
					Value: []any{1, 2},
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldBeNil)
		})

		Convey("Success with Between operation having correct array length\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationBetween,
				Field:     "field1",
				ValueOptCfg: cond.ValueOptCfg{
					Value: []any{1, 2},
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldBeNil)
		})

		Convey("Success with valid regex\n", func() {
			cfg := &cond.CondCfg{
				Operation: cond.OperationRegex,
				Field:     "field1",
				ValueOptCfg: cond.ValueOptCfg{
					Value: "^test.*",
				},
			}
			err := validateCond(ctx, cfg)
			So(err, ShouldBeNil)
		})

		Convey("Success with other single value operations\n", func() {
			operations := []string{
				cond.OperationNotEq, cond.OperationGt, cond.OperationGte,
				cond.OperationLt, cond.OperationLte, cond.OperationMatch,
				cond.OperationMatchPhrase, cond.OperationCurrent,
			}
			for _, op := range operations {
				cfg := &cond.CondCfg{
					Operation: op,
					Field:     "field1",
					ValueOptCfg: cond.ValueOptCfg{
						Value: "value1",
					},
				}
				err := validateCond(ctx, cfg)
				So(err, ShouldBeNil)
			}
		})
	})
}

func TestValidateRiskTypesLocalizesInvalidParameterDetails(t *testing.T) {
	testCases := []struct {
		name     string
		language string
		want     string
	}{
		{"English", rest.AmericanEnglish, "Risk type ID 'risk-1' already exists in the request body."},
		{"SimplifiedChinese", rest.SimplifiedChinese, "请求体中已存在风险类 ID 'risk-1'。"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			riskTypes := []*interfaces.RiskType{
				{RTID: "risk-1", RTName: "risk-one"},
				{RTID: "risk-1", RTName: "risk-two"},
			}
			err := ValidateRiskTypes(
				rest.WithLanguage(context.Background(), testCase.language),
				"kn-1",
				riskTypes,
			)
			if err == nil {
				t.Fatal("ValidateRiskTypes() error = nil, want duplicated ID error")
			}
			httpErr, ok := err.(*rest.HTTPError)
			if !ok {
				t.Fatalf("error type = %T, want *rest.HTTPError", err)
			}
			if got, ok := httpErr.BaseError.ErrorDetails.(string); !ok || got != testCase.want {
				t.Fatalf("error_details = %#v, want %q", httpErr.BaseError.ErrorDetails, testCase.want)
			}
		})
	}
}

func TestCommonValidationDetailsRespectLanguage(t *testing.T) {
	testCases := []struct {
		name     string
		language string
		want     string
	}{
		{"English", rest.AmericanEnglish, "x-http-method-override must be GET; got POST."},
		{"SimplifiedChinese", rest.SimplifiedChinese, "x-http-method-override 必须为 GET，当前为 POST。"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := ValidateHeaderMethodOverride(
				rest.WithLanguage(context.Background(), testCase.language),
				"POST",
			)
			if err == nil {
				t.Fatal("ValidateHeaderMethodOverride() error = nil, want invalid method error")
			}
			httpErr, ok := err.(*rest.HTTPError)
			if !ok {
				t.Fatalf("error type = %T, want *rest.HTTPError", err)
			}
			if got, ok := httpErr.BaseError.ErrorDetails.(string); !ok || got != testCase.want {
				t.Fatalf("error_details = %#v, want %q", httpErr.BaseError.ErrorDetails, testCase.want)
			}
		})
	}
}
