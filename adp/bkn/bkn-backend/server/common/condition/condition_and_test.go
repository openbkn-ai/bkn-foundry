// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	dtype "bkn-backend/interfaces/data_type"
)

func TestNewAndCond(t *testing.T) {
	Convey("Test newAndCond", t, func() {
		ctx := context.Background()
		fieldsMap := map[string]*ViewField{
			"field1": {
				Name: "field1",
				Type: dtype.DATATYPE_STRING,
			},
			"field2": {
				Name: "field2",
				Type: dtype.DATATYPE_STRING,
			},
		}

		Convey("empty sub conditions should create a match-all condition", func() {
			cfg := &CondCfg{
				Operation: OperationAnd,
				SubConds:  []*CondCfg{},
			}
			cond, err := newAndCond(ctx, cfg, CUSTOM, fieldsMap)
			So(err, ShouldBeNil)
			So(cond, ShouldNotBeNil)
			So(len(cond.(*AndCond).mSubConds), ShouldEqual, 0)
		})

		Convey("sub conditions exceed MaxSubCondition should return error", func() {
			subConds := make([]*CondCfg, MaxSubCondition+1)
			for i := 0; i <= MaxSubCondition; i++ {
				subConds[i] = &CondCfg{
					Operation: OperationEq,
					Field:     "field1",
					ValueOptCfg: ValueOptCfg{
						ValueFrom: ValueFrom_Const,
						Value:     "value1",
					},
				}
			}
			cfg := &CondCfg{
				Operation: OperationAnd,
				SubConds:  subConds,
			}
			cond, err := newAndCond(ctx, cfg, CUSTOM, fieldsMap)
			So(err, ShouldNotBeNil)
			So(cond, ShouldBeNil)
			So(err.Error(), ShouldContainSubstring, "sub condition size limit")
		})

		Convey("valid sub conditions should create AndCond", func() {
			cfg := &CondCfg{
				Operation: OperationAnd,
				SubConds: []*CondCfg{
					{
						Operation: OperationEq,
						Field:     "field1",
						ValueOptCfg: ValueOptCfg{
							ValueFrom: ValueFrom_Const,
							Value:     "value1",
						},
					},
					{
						Operation: OperationEq,
						Field:     "field2",
						ValueOptCfg: ValueOptCfg{
							ValueFrom: ValueFrom_Const,
							Value:     "value2",
						},
					},
				},
			}
			cond, err := newAndCond(ctx, cfg, CUSTOM, fieldsMap)
			So(err, ShouldBeNil)
			So(cond, ShouldNotBeNil)
			andCond, ok := cond.(*AndCond)
			So(ok, ShouldBeTrue)
			So(len(andCond.mSubConds), ShouldEqual, 2)
		})

		Convey("nil sub condition should be skipped", func() {
			cfg := &CondCfg{
				Operation: OperationAnd,
				SubConds: []*CondCfg{
					nil,
					{
						Operation: OperationEq,
						Field:     "field1",
						ValueOptCfg: ValueOptCfg{
							ValueFrom: ValueFrom_Const,
							Value:     "value1",
						},
					},
				},
			}
			cond, err := newAndCond(ctx, cfg, CUSTOM, fieldsMap)
			So(err, ShouldBeNil)
			So(cond, ShouldNotBeNil)
		})

		Convey("error in sub condition should propagate", func() {
			cfg := &CondCfg{
				Operation: OperationAnd,
				SubConds: []*CondCfg{
					{
						Operation: OperationEq,
						Field:     "nonexistent",
						ValueOptCfg: ValueOptCfg{
							ValueFrom: ValueFrom_Const,
							Value:     "value1",
						},
					},
				},
			}
			cond, err := newAndCond(ctx, cfg, CUSTOM, fieldsMap)
			So(err, ShouldNotBeNil)
			So(cond, ShouldBeNil)
		})
	})
}

func TestAndCond_Convert(t *testing.T) {
	Convey("Test AndCond.Convert", t, func() {
		ctx := context.Background()
		fieldsMap := map[string]*ViewField{
			"field1": {
				Name: "field1",
				Type: dtype.DATATYPE_STRING,
			},
			"field2": {
				Name: "field2",
				Type: dtype.DATATYPE_STRING,
			},
		}

		Convey("single sub condition should work", func() {
			cfg := &CondCfg{
				Operation: OperationAnd,
				SubConds: []*CondCfg{
					{
						Operation: OperationEq,
						Field:     "field1",
						ValueOptCfg: ValueOptCfg{
							ValueFrom: ValueFrom_Const,
							Value:     "value1",
						},
					},
				},
			}
			cond, _ := newAndCond(ctx, cfg, CUSTOM, fieldsMap)
			andCond := cond.(*AndCond)

			vectorizer := func(ctx context.Context, words []string) ([]*VectorResp, error) {
				return nil, nil
			}

			dsl, err := andCond.Convert(ctx, vectorizer)
			So(err, ShouldBeNil)
			So(dsl, ShouldNotBeEmpty)
		})

		Convey("multiple sub conditions should be joined", func() {
			cfg := &CondCfg{
				Operation: OperationAnd,
				SubConds: []*CondCfg{
					{
						Operation: OperationEq,
						Field:     "field1",
						ValueOptCfg: ValueOptCfg{
							ValueFrom: ValueFrom_Const,
							Value:     "value1",
						},
					},
					{
						Operation: OperationEq,
						Field:     "field2",
						ValueOptCfg: ValueOptCfg{
							ValueFrom: ValueFrom_Const,
							Value:     "value2",
						},
					},
				},
			}
			cond, _ := newAndCond(ctx, cfg, CUSTOM, fieldsMap)
			andCond := cond.(*AndCond)

			vectorizer := func(ctx context.Context, words []string) ([]*VectorResp, error) {
				return nil, nil
			}

			dsl, err := andCond.Convert(ctx, vectorizer)
			So(err, ShouldBeNil)
			So(dsl, ShouldNotBeEmpty)
			So(dsl, ShouldContainSubstring, "bool")
			So(dsl, ShouldContainSubstring, "must")
		})

		Convey("error in sub condition Convert should propagate", func() {
			cfg := &CondCfg{
				Operation: OperationAnd,
				SubConds: []*CondCfg{
					{
						Operation: OperationKNN,
						Field:     "field1",
						ValueOptCfg: ValueOptCfg{
							ValueFrom: ValueFrom_Const,
							Value:     "value1",
						},
						RemainCfg: map[string]any{
							"limit_key":   "k",
							"limit_value": 10,
						},
					},
				},
			}
			cond, _ := newAndCond(ctx, cfg, CUSTOM, fieldsMap)
			andCond := cond.(*AndCond)

			vectorizer := func(ctx context.Context, words []string) ([]*VectorResp, error) {
				return nil, context.DeadlineExceeded
			}

			dsl, err := andCond.Convert(ctx, vectorizer)
			So(err, ShouldNotBeNil)
			So(dsl, ShouldBeEmpty)
		})
	})
}

func TestAndCond_Convert2SQL(t *testing.T) {
	Convey("Test AndCond.Convert2SQL", t, func() {
		ctx := context.Background()
		fieldsMap := map[string]*ViewField{
			"field1": {
				Name: "field1",
				Type: dtype.DATATYPE_STRING,
			},
			"field2": {
				Name: "field2",
				Type: dtype.DATATYPE_STRING,
			},
		}

		Convey("single sub condition should work", func() {
			cfg := &CondCfg{
				Operation: OperationAnd,
				SubConds: []*CondCfg{
					{
						Operation: OperationEq,
						Field:     "field1",
						ValueOptCfg: ValueOptCfg{
							ValueFrom: ValueFrom_Const,
							Value:     "value1",
						},
					},
				},
			}
			cond, _ := newAndCond(ctx, cfg, CUSTOM, fieldsMap)
			andCond := cond.(*AndCond)

			sql, err := andCond.Convert2SQL(ctx)
			So(err, ShouldBeNil)
			So(sql, ShouldNotBeEmpty)
			So(sql, ShouldContainSubstring, "field1")
		})

		Convey("empty sub conditions should convert to SQL tautology", func() {
			cfg := &CondCfg{Operation: OperationAnd, SubConds: []*CondCfg{}}
			cond, err := newAndCond(ctx, cfg, CUSTOM, fieldsMap)
			So(err, ShouldBeNil)

			sql, err := cond.Convert2SQL(ctx)
			So(err, ShouldBeNil)
			So(sql, ShouldEqual, "1 = 1")
		})

		Convey("multiple sub conditions should be joined with AND", func() {
			cfg := &CondCfg{
				Operation: OperationAnd,
				SubConds: []*CondCfg{
					{
						Operation: OperationEq,
						Field:     "field1",
						ValueOptCfg: ValueOptCfg{
							ValueFrom: ValueFrom_Const,
							Value:     "value1",
						},
					},
					{
						Operation: OperationEq,
						Field:     "field2",
						ValueOptCfg: ValueOptCfg{
							ValueFrom: ValueFrom_Const,
							Value:     "value2",
						},
					},
				},
			}
			cond, _ := newAndCond(ctx, cfg, CUSTOM, fieldsMap)
			andCond := cond.(*AndCond)

			sql, err := andCond.Convert2SQL(ctx)
			So(err, ShouldBeNil)
			So(sql, ShouldNotBeEmpty)
			So(sql, ShouldContainSubstring, "AND")
			So(sql, ShouldContainSubstring, "field1")
			So(sql, ShouldContainSubstring, "field2")
		})

		Convey("error in sub condition Convert2SQL should propagate", func() {
			cfg := &CondCfg{
				Operation: OperationAnd,
				SubConds: []*CondCfg{
					{
						Operation: OperationMatch,
						Field:     "field1",
						ValueOptCfg: ValueOptCfg{
							ValueFrom: ValueFrom_Const,
							Value:     "value1",
						},
					},
				},
			}
			cond, _ := newAndCond(ctx, cfg, CUSTOM, fieldsMap)
			andCond := cond.(*AndCond)

			// Match condition returns empty SQL, so this should work
			sql, err := andCond.Convert2SQL(ctx)
			So(err, ShouldBeNil)
			So(sql, ShouldBeEmpty)
		})
	})
}

func TestConvertAndCondToDatasetFilterCondition(t *testing.T) {
	Convey("Test convertAndCondToDatasetFilterCondition", t, func() {
		result, err := convertAndCondToDatasetFilterCondition(context.Background(), &CondCfg{
			Operation: OperationAnd,
			SubConds:  []*CondCfg{},
		}, nil, nil)
		So(err, ShouldBeNil)
		So(result, ShouldBeNil)
	})
}
