// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See LICENSE-OPENBKN.txt in the project root for details.

package resource

import "vega-backend/interfaces"

// IsFeatureSupported reports whether a Property type can generate a Feature type.
func IsFeatureSupported(propertyType string, featureType string) bool {
	switch propertyType {
	case interfaces.DataType_String:
		return featureType == interfaces.PropertyFeatureType_Keyword ||
			featureType == interfaces.PropertyFeatureType_Fulltext ||
			featureType == interfaces.PropertyFeatureType_Vector
	case interfaces.DataType_Text:
		return featureType == interfaces.PropertyFeatureType_Keyword ||
			featureType == interfaces.PropertyFeatureType_Fulltext ||
			featureType == interfaces.PropertyFeatureType_Vector
	case interfaces.DataType_Vector:
		return featureType == interfaces.PropertyFeatureType_Vector
	default:
		return false
	}
}

// NormalizeSelfReferencingFeatures 抹平字段特征里指向属性自身的 ref_property。
//
// ref_property 的语义是「特征挂在 A 属性上、但作用于 B 字段」，指向字段自身是无意义的
// 冗余写法：能力派生在 ref_property 为空时本来就落回属性自身（见 bkn-backend 的
// VegaResourceIndexCaps），两种写法同解。
//
// 历史上平台自己写入的存量资源正是这个形状，因此必须就地抹平而不是报错。抹平要同时作用在
// 请求侧与库里读出来的 schema 上：只归一化一侧的话，两侧 Features 不再 DeepEqual，
// 资源更新会被 validateMutableSchemaUpdate 判成 build 相关变更，进而清空 LocalIndexName，
// 让一次普通编辑把已建好的索引废掉。
func NormalizeSelfReferencingFeatures(props []*interfaces.Property) {
	for _, prop := range props {
		if prop == nil {
			continue
		}
		for i := range prop.Features {
			if prop.Features[i].RefProperty == prop.Name {
				prop.Features[i].RefProperty = ""
			}
		}
	}
}

// IsFeatureRefPropertyTypeSupported reports whether a referenced result Property
// has the type produced by a Feature.
func IsFeatureRefPropertyTypeSupported(propertyType string, featureType string) bool {
	switch featureType {
	case interfaces.PropertyFeatureType_Keyword:
		return propertyType == interfaces.DataType_String
	case interfaces.PropertyFeatureType_Fulltext:
		return propertyType == interfaces.DataType_Text
	case interfaces.PropertyFeatureType_Vector:
		return propertyType == interfaces.DataType_Vector
	default:
		return false
	}
}
