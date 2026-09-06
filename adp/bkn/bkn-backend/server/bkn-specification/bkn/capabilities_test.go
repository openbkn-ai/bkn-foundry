// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"strings"
	"testing"
)

func networkWithCapabilities() *BknNetwork {
	return &BknNetwork{
		BknNetworkFrontmatter: BknNetworkFrontmatter{
			Type: "knowledge_network", ID: "kn1", Name: "供应链", Branch: "main",
			Capabilities: &BknCapabilities{
				Skills: []*BknCapabilitySkill{{ID: "skill-1", Name: "交期评估"}},
				Functions: []*BknCapabilityFunction{{
					BoxID: "box-1", ToolID: "tool-1", BoxName: "供应链计算", ToolName: "BOM 多层展开",
				}},
			},
		},
	}
}

// TestCapabilitiesRoundTrip covers the section surviving a write and a read. Both halves of each
// identity are written: the ids match exactly in the environment that produced the file, and the
// names are the only thing that can match anywhere else.
func TestCapabilitiesRoundTrip(t *testing.T) {
	text := SerializeBknNetwork(networkWithCapabilities())

	parsed, err := ParseNetworkFile(text, "network.bkn")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Capabilities == nil {
		t.Fatal("capabilities section lost in the round trip")
	}
	if len(parsed.Capabilities.Skills) != 1 || parsed.Capabilities.Skills[0].Name != "交期评估" {
		t.Fatalf("skill not round-tripped: %+v", parsed.Capabilities.Skills)
	}
	fn := parsed.Capabilities.Functions[0]
	if fn.BoxID != "box-1" || fn.ToolID != "tool-1" || fn.BoxName != "供应链计算" || fn.ToolName != "BOM 多层展开" {
		t.Fatalf("function not round-tripped: %+v", fn)
	}
}

// TestCapabilitiesOmittedWhenEmpty keeps the section out of files that have nothing to declare,
// so an export from a network with no bindings is byte-identical to what it was before.
func TestCapabilitiesOmittedWhenEmpty(t *testing.T) {
	for name, doc := range map[string]*BknNetwork{
		"nil":   {BknNetworkFrontmatter: BknNetworkFrontmatter{Type: "knowledge_network", ID: "kn1", Name: "空"}},
		"empty": {BknNetworkFrontmatter: BknNetworkFrontmatter{Type: "knowledge_network", ID: "kn1", Name: "空", Capabilities: &BknCapabilities{}}},
	} {
		if strings.Contains(SerializeBknNetwork(doc), "capabilities") {
			t.Fatalf("%s: empty section should not be written", name)
		}
	}
}

// TestCapabilitiesQuotingSurvives covers the reason this block goes through yaml.Marshal instead
// of being hand-formatted: a name containing YAML punctuation must not produce a file that no
// longer parses.
func TestCapabilitiesQuotingSurvives(t *testing.T) {
	doc := networkWithCapabilities()
	doc.Capabilities.Skills[0].Name = "评估: 交期, 含 #备注"

	parsed, err := ParseNetworkFile(SerializeBknNetwork(doc), "network.bkn")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Capabilities.Skills[0].Name != "评估: 交期, 含 #备注" {
		t.Fatalf("name mangled: %q", parsed.Capabilities.Skills[0].Name)
	}
}

// TestUnknownCapabilitiesShapeIsIgnored covers forward compatibility in the other direction: a
// section this reader cannot make sense of must not fail the import of an otherwise valid model.
func TestUnknownCapabilitiesShapeIsIgnored(t *testing.T) {
	text := "---\ntype: knowledge_network\nid: kn1\nname: 供应链\ncapabilities: \"not a mapping\"\n---\n\n# 供应链\n"

	parsed, err := ParseNetworkFile(text, "network.bkn")
	if err != nil {
		t.Fatalf("a malformed section must not fail the parse: %v", err)
	}
	if parsed.Capabilities != nil {
		t.Fatalf("expected the section to be dropped, got %+v", parsed.Capabilities)
	}
}
