// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Guards the exported frontmatter layout: plain values are written bare and tags as a [a, b] flow
// list, including an empty one. Exports and the stored BKN raw content depend on these bytes.
func TestSerializeFrontmatter_PlainValuesStayBare(t *testing.T) {
	text := SerializeObjectType(&BknObjectType{BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{
		Type: "object_type", ID: "pod", Name: "Pod实例（更新版）", Tags: []string{"拓扑架构", "Kubernetes"},
	}})
	assert.True(t, strings.Contains(text, "\nid: pod\n"), text)
	assert.True(t, strings.Contains(text, "\nname: Pod实例（更新版）\n"), text)
	assert.True(t, strings.Contains(text, "\ntags: [拓扑架构, Kubernetes]\n"), text)

	empty := SerializeRelationType(&BknRelationType{BknRelationTypeFrontmatter: BknRelationTypeFrontmatter{
		Type: "relation_type", ID: "r", Name: "属于",
	}})
	assert.True(t, strings.Contains(empty, "\ntags: []\n"), empty)
}
