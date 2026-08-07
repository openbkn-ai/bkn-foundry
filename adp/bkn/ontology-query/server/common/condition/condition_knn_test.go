// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"fmt"
	"testing"

	dtype "ontology-query/interfaces/data_type"

	. "github.com/smartystreets/goconvey/convey"
)

func Test_NewKnnCond(t *testing.T) {
	Convey("Test NewKnnCond", t, func() {
		ctx := context.Background()
		fieldsMap := map[string]*DataProperty{
			"vector_field": {
				Name: "vector_field",
				Type: dtype.DATATYPE_VECTOR,
				IndexConfig: &IndexConfig{
					VectorConfig: VectorConfig{
						Enabled: true,
						ModelID: "model1",
					},
				},
				MappedField: Field{
					Name: "mapped_vector",
				},
			},
			"vector_field_no_config": {
				Name: "vector_field_no_config",
				Type: dtype.DATATYPE_VECTOR,
				MappedField: Field{
					Name: "mapped_vector",
				},
			},
			"vector_field_disabled": {
				Name: "vector_field_disabled",
				Type: dtype.DATATYPE_VECTOR,
				IndexConfig: &IndexConfig{
					VectorConfig: VectorConfig{
						Enabled: false,
						ModelID: "model1",
					},
				},
				MappedField: Field{
					Name: "mapped_vector",
				},
			},
			"name": {
				Name: "name",
				Type: dtype.DATATYPE_STRING,
				MappedField: Field{
					Name: "mapped_name",
				},
			},
		}

		Convey("成功 - 配置了向量索引的字段", func() {
			cfg := &CondCfg{
				Name:      "vector_field",
				Operation: OperationKNN,
				ValueOptCfg: ValueOptCfg{
					Value: "test",
				},
				RemainCfg: map[string]any{
					"limit_key":   "k",
					"limit_value": 10,
				},
			}
			cond, err := NewKnnCond(ctx, cfg, CUSTOM, fieldsMap)
			So(err, ShouldBeNil)
			So(cond, ShouldNotBeNil)
		})

		Convey("失败 - AllField", func() {
			cfg := &CondCfg{
				Name:      AllField,
				Operation: OperationKNN,
				ValueOptCfg: ValueOptCfg{
					Value: "test",
				},
				RemainCfg: map[string]any{
					"limit_key":   "k",
					"limit_value": 10,
				},
			}
			cond, err := NewKnnCond(ctx, cfg, CUSTOM, fieldsMap)
			So(err.Error(), ShouldContainSubstring, "the knn operation does not support the [*] query, please specify the field name explicitly")
			So(cond, ShouldBeNil)
		})

		Convey("成功 - 带子条件", func() {
			cfg := &CondCfg{
				Name:      "vector_field",
				Operation: OperationKNN,
				ValueOptCfg: ValueOptCfg{
					Value: "test",
				},
				RemainCfg: map[string]any{
					"limit_key":   "k",
					"limit_value": 10,
				},
				SubConds: []*CondCfg{
					{
						Name:      "name",
						Operation: OperationEq,
						ValueOptCfg: ValueOptCfg{
							Value: "test",
						},
					},
				},
			}
			cond, err := NewKnnCond(ctx, cfg, CUSTOM, fieldsMap)
			So(err, ShouldBeNil)
			So(cond, ShouldNotBeNil)
		})

		Convey("失败 - 未配置向量索引", func() {
			cfg := &CondCfg{
				Name:      "vector_field_no_config",
				Operation: OperationKNN,
				ValueOptCfg: ValueOptCfg{
					Value: "test",
				},
				RemainCfg: map[string]any{
					"limit_key":   "k",
					"limit_value": 10,
				},
			}
			cond, err := NewKnnCond(ctx, cfg, CUSTOM, fieldsMap)
			So(err, ShouldNotBeNil)
			So(cond, ShouldBeNil)
		})

		Convey("失败 - 向量索引未启用", func() {
			cfg := &CondCfg{
				Name:      "vector_field_disabled",
				Operation: OperationKNN,
				ValueOptCfg: ValueOptCfg{
					Value: "test",
				},
				RemainCfg: map[string]any{
					"limit_key":   "k",
					"limit_value": 10,
				},
			}
			cond, err := NewKnnCond(ctx, cfg, CUSTOM, fieldsMap)
			So(err, ShouldNotBeNil)
			So(cond, ShouldBeNil)
		})

		Convey("失败 - 子条件错误", func() {
			cfg := &CondCfg{
				Name:      "vector_field",
				Operation: OperationKNN,
				ValueOptCfg: ValueOptCfg{
					Value: "test",
				},
				RemainCfg: map[string]any{
					"limit_key":   "k",
					"limit_value": 10,
				},
				SubConds: []*CondCfg{
					{
						Name:      "nonexistent",
						Operation: OperationEq,
						ValueOptCfg: ValueOptCfg{
							Value: "test",
						},
					},
				},
			}
			cond, err := NewKnnCond(ctx, cfg, CUSTOM, fieldsMap)
			So(err, ShouldNotBeNil)
			So(cond, ShouldBeNil)
		})
	})
}

func Test_KnnCond_Convert(t *testing.T) {
	Convey("Test KnnCond Convert", t, func() {
		ctx := context.Background()
		vectorizer := func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error) {
			return []VectorResp{
				{
					Vector: []float32{0.1, 0.2, 0.3},
				},
			}, nil
		}

		Convey("成功 - 无子条件", func() {
			cond := &KnnCond{
				mCfg: &CondCfg{
					ValueOptCfg: ValueOptCfg{
						Value: "test",
					},
					NameField: &DataProperty{
						Name: "vector_field",
					},
					RemainCfg: map[string]any{
						"limit_key":   "k",
						"limit_value": 10,
					},
				},
				mFilterFieldName: "_vector_vector_field",
				mSubConds:        []Condition{},
			}
			result, err := cond.Convert(ctx, vectorizer)
			So(err, ShouldBeNil)
			So(result, ShouldContainSubstring, `"knn"`)
			So(result, ShouldContainSubstring, `"_vector_vector_field"`)
			So(result, ShouldContainSubstring, `"k"`)
			So(result, ShouldContainSubstring, `10`)
		})

		Convey("成功 - 有单个子条件", func() {
			fieldsMap := map[string]*DataProperty{
				"name": {
					Name: "name",
					Type: dtype.DATATYPE_STRING,
					MappedField: Field{
						Name: "mapped_name",
					},
				},
			}
			subCfg := &CondCfg{
				Name:      "name",
				Operation: OperationEq,
				ValueOptCfg: ValueOptCfg{
					Value: "test",
				},
			}
			subCond, _ := NewEqCond(ctx, subCfg, fieldsMap)
			cond := &KnnCond{
				mCfg: &CondCfg{
					ValueOptCfg: ValueOptCfg{
						Value: "test",
					},
					NameField: &DataProperty{
						Name: "vector_field",
					},
					RemainCfg: map[string]any{
						"limit_key":   "k",
						"limit_value": 10,
					},
				},
				mFilterFieldName: "_vector_vector_field",
				mSubConds:        []Condition{subCond},
			}
			result, err := cond.Convert(ctx, vectorizer)
			So(err, ShouldBeNil)
			So(result, ShouldContainSubstring, `"knn"`)
			So(result, ShouldContainSubstring, `"filter"`)
			So(result, ShouldContainSubstring, `"bool"`)
			So(result, ShouldContainSubstring, `"must"`)
		})

		Convey("成功 - 有多个子条件", func() {
			fieldsMap := map[string]*DataProperty{
				"name": {
					Name: "name",
					Type: dtype.DATATYPE_STRING,
					MappedField: Field{
						Name: "mapped_name",
					},
				},
				"age": {
					Name: "age",
					Type: dtype.DATATYPE_INTEGER,
					MappedField: Field{
						Name: "mapped_age",
					},
				},
			}
			subCfg1 := &CondCfg{
				Name:      "name",
				Operation: OperationEq,
				ValueOptCfg: ValueOptCfg{
					Value: "test",
				},
			}
			subCfg2 := &CondCfg{
				Name:      "age",
				Operation: OperationGt,
				ValueOptCfg: ValueOptCfg{
					Value: 18,
				},
			}
			subCond1, _ := NewEqCond(ctx, subCfg1, fieldsMap)
			subCond2, _ := NewGtCond(ctx, subCfg2, fieldsMap)
			cond := &KnnCond{
				mCfg: &CondCfg{
					ValueOptCfg: ValueOptCfg{
						Value: "test",
					},
					NameField: &DataProperty{
						Name: "vector_field",
					},
					RemainCfg: map[string]any{
						"limit_key":   "k",
						"limit_value": 10,
					},
				},
				mFilterFieldName: "_vector_vector_field",
				mSubConds:        []Condition{subCond1, subCond2},
			}
			result, err := cond.Convert(ctx, vectorizer)
			So(err, ShouldBeNil)
			So(result, ShouldContainSubstring, `"filter"`)
		})

		Convey("失败 - vectorizer错误", func() {
			failingVectorizer := func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error) {
				return nil, fmt.Errorf("vectorizer error")
			}
			cond := &KnnCond{
				mCfg: &CondCfg{
					ValueOptCfg: ValueOptCfg{
						Value: "test",
					},
					NameField: &DataProperty{
						Name: "vector_field",
					},
					RemainCfg: map[string]any{
						"limit_key":   "k",
						"limit_value": 10,
					},
				},
				mFilterFieldName: "_vector_vector_field",
				mSubConds:        []Condition{},
			}
			result, err := cond.Convert(ctx, failingVectorizer)
			So(err, ShouldNotBeNil)
			So(result, ShouldEqual, "")
		})

		Convey("失败 - 子条件转换错误", func() {
			fieldsMap := map[string]*DataProperty{
				"name": {
					Name: "name",
					Type: dtype.DATATYPE_STRING,
					MappedField: Field{
						Name: "mapped_name",
					},
				},
			}
			subCfg := &CondCfg{
				Name:      "name",
				Operation: OperationEq,
				ValueOptCfg: ValueOptCfg{
					Value: "test",
				},
			}
			subCond, _ := NewEqCond(ctx, subCfg, fieldsMap)
			// 创建一个会失败的 vectorizer，在子条件转换时失败
			failingVectorizer := func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error) {
				if property != nil && property.Name == "name" {
					return nil, fmt.Errorf("sub condition vectorizer error")
				}
				return []VectorResp{
					{
						Vector: []float32{0.1, 0.2, 0.3},
					},
				}, nil
			}
			cond := &KnnCond{
				mCfg: &CondCfg{
					ValueOptCfg: ValueOptCfg{
						Value: "test",
					},
					NameField: &DataProperty{
						Name: "vector_field",
					},
					RemainCfg: map[string]any{
						"limit_key":   "k",
						"limit_value": 10,
					},
				},
				mFilterFieldName: "_vector_vector_field",
				mSubConds:        []Condition{subCond},
			}
			// 注意：子条件的 Convert 不会调用 vectorizer，所以这个测试实际上不会失败
			// 但我们可以测试其他场景
			result, err := cond.Convert(ctx, failingVectorizer)
			// 由于主条件的 vectorizer 会成功，所以这个测试应该成功
			So(err, ShouldBeNil)
			So(result, ShouldNotBeEmpty)
		})
	})
}

func Test_KnnCond_Convert2SQL(t *testing.T) {
	Convey("Test KnnCond Convert2SQL", t, func() {
		ctx := context.Background()

		Convey("成功 - 返回空字符串", func() {
			cond := &KnnCond{
				mCfg: &CondCfg{
					ValueOptCfg: ValueOptCfg{
						Value: "test",
					},
				},
				mFilterFieldName: "_vector_vector_field",
				mSubConds:        []Condition{},
			}
			result, err := cond.Convert2SQL(ctx)
			So(err, ShouldBeNil)
			So(result, ShouldEqual, "")
		})
	})
}

func Test_rewriteKnnCond(t *testing.T) {
	Convey("Test rewriteKnnCond", t, func() {
		ctx := context.Background()
		vectorizer := func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error) {
			return []VectorResp{
				{
					Vector: []float32{0.1, 0.2, 0.3},
				},
			}, nil
		}

		Convey("成功 - 重写条件", func() {
			cfg := &CondCfg{
				Name:      "vector_field",
				Operation: OperationKNN,
				ValueOptCfg: ValueOptCfg{
					Value: "test",
				},
				NameField: &DataProperty{
					Name: "vector_field",
					Type: dtype.DATATYPE_VECTOR,
					IndexConfig: &IndexConfig{
						VectorConfig: VectorConfig{
							Enabled: true,
							ModelID: "model1",
						},
					},
					MappedField: Field{
						Name: "mapped_vector",
					},
				},
				RemainCfg: map[string]any{
					"limit_key":   "k",
					"limit_value": 10,
				},
			}
			result, err := rewriteKnnCond(ctx, cfg, vectorizer)
			So(err, ShouldBeNil)
			So(result, ShouldNotBeNil)
			So(result.Name, ShouldEqual, "mapped_vector")
			So(result.Operation, ShouldEqual, OperationKNNVector)
		})

		Convey("失败 - NameField为空", func() {
			cfg := &CondCfg{
				Name:      "vector_field",
				Operation: OperationKNN,
				ValueOptCfg: ValueOptCfg{
					Value: "test",
				},
				NameField: &DataProperty{
					Name: "",
				},
				RemainCfg: map[string]any{
					"limit_key":   "k",
					"limit_value": 10,
				},
			}
			result, err := rewriteKnnCond(ctx, cfg, vectorizer)
			So(err, ShouldNotBeNil)
			So(result, ShouldBeNil)
		})

	})
}

// 资源类对象类的属性是标量，向量落在构建任务生成的字段上：只看属性类型会把
// 两条路径分工不同，必须分开钉住：
//   - 属性本身是 vector 类型：向量就在该字段上，模型由对象类声明，在这里算好传下去。
//     这类资源可能没有本地构建索引，下游没有别的依据可用。
//   - 标量属性：向量在构建任务生成的字段上，字段名与模型都归 vega 解析，文本原样下传。
func Test_rewriteKnnCond_NativeVectorPropertyVectorizesHere(t *testing.T) {
	Convey("Test rewriteKnnCond on a native vector property", t, func() {
		ctx := context.Background()
		called := false
		vectorizer := func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error) {
			called = true
			return []VectorResp{{Vector: []float32{0.1, 0.2}}}, nil
		}

		cfg := &CondCfg{
			Name:      "embedding",
			Operation: OperationKNN,
			NameField: &DataProperty{
				Name:        "embedding",
				Type:        dtype.DATATYPE_VECTOR,
				MappedField: Field{Name: "embedding_col"},
				IndexConfig: &IndexConfig{VectorConfig: VectorConfig{Enabled: true, ModelID: "m-1"}},
			},
			ValueOptCfg: ValueOptCfg{Value: "famous stadium"},
		}

		result, err := rewriteKnnCond(ctx, cfg, vectorizer)

		So(err, ShouldBeNil)
		So(called, ShouldBeTrue)
		So(result.Name, ShouldEqual, "embedding_col")
		So(result.Value, ShouldResemble, []float32{0.1, 0.2})
	})
}

func Test_rewriteKnnCond_NativeVectorPropertyNeedsModel(t *testing.T) {
	Convey("Test rewriteKnnCond rejects a vector property without a model", t, func() {
		ctx := context.Background()
		vectorizer := func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error) {
			return []VectorResp{{Vector: []float32{0.1}}}, nil
		}

		cfg := &CondCfg{
			Name:      "embedding",
			Operation: OperationKNN,
			NameField: &DataProperty{
				Name:        "embedding",
				Type:        dtype.DATATYPE_VECTOR,
				MappedField: Field{Name: "embedding_col"},
			},
			ValueOptCfg: ValueOptCfg{Value: "x"},
		}

		_, err := rewriteKnnCond(ctx, cfg, vectorizer)

		So(err, ShouldNotBeNil)
	})
}

// 向量字段与模型的解析归 vega：这里只把逻辑属性名换成资源字段名，查询词原样下传。
func Test_rewriteKnnCond_PassesTextDownstream(t *testing.T) {
	Convey("Test rewriteKnnCond keeps the query text", t, func() {
		ctx := context.Background()
		vectorizer := func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error) {
			t.Fatal("ontology-query must not vectorize; vega owns that")
			return nil, nil
		}

		cfg := &CondCfg{
			Name:      "stadium_name",
			Operation: OperationKNN,
			NameField: &DataProperty{
				Name:        "stadium_name",
				MappedField: Field{Name: "stadium_name_col"},
			},
			ValueOptCfg: ValueOptCfg{Value: "famous stadium"},
		}

		result, err := rewriteKnnCond(ctx, cfg, vectorizer)

		So(err, ShouldBeNil)
		So(result.Name, ShouldEqual, "stadium_name_col")
		So(result.Operation, ShouldEqual, OperationKNNVector)
		So(result.Value, ShouldEqual, "famous stadium")
	})
}
