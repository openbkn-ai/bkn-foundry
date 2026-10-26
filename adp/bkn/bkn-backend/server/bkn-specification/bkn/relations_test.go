// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	fGuess = "relation_types/pod_guess.bkn"
	fLink  = "relation_types/pod_link.bkn"
	fNode  = "object_types/node.bkn"

	endpointPeer = "| Source | Target | Type |\n|--------|--------|------|\n| pod | pod | direct |"

	// guessFile is a fallback path for pod_peer: same ends, matched by a rule.
	guessFile = "---\ntype: relation_type\nid: pod_guess\nname: 推测同组\ntags: []\nimportance: secondary\nfallback_for: pod_peer\nverification: unconfirmed\n---\n\n" +
		"## RelationType: 推测同组\n\n按名称前缀推测。\n\n" +
		"### Endpoint\n\n| Source | Target | Type |\n|--------|--------|------|\n| pod | pod | direct |\n\n" +
		"### Mapping Rules\n\n| Source Property | Target Property |\n|-----------------|-----------------|\n| pod_name | pod_name |\n"

	// linkFile is an indirect relation with relation properties.
	linkFile = "---\ntype: relation_type\nid: pod_link\nname: 连接\ntags: []\n---\n\n## RelationType: 连接\n\n" +
		"### Endpoint\n\n| Source | Target | Type |\n|--------|--------|------|\n| pod | pod | indirect |\n\n" +
		"### Backing Resource\n\n| Type | ID |\n|------|----|\n| resource | res_link |\n\n" +
		"### Source Mapping\n\n| Source Property | Resource Property |\n|-----------------|-------------------|\n| id | src_id |\n\n" +
		"### Target Mapping\n\n| Resource Property | Target Property |\n|-------------------|-----------------|\n| dst_id | id |\n\n" +
		"### Data Properties\n\n| Name | Display Name | Type | Semantic | Description | Mapped Field |\n|------|--------------|------|----------|-------------|--------------|\n" +
		"| since | 起始 | date | valid_from | | since |\n| weight | 权重 | float | | | weight |\n\n" +
		"### Property Constraints\n\n```yaml\nweight:\n  range: {min: 0}\n```\n"

	// crossFile is a filtered_cross_join relation between pods.
	crossFile = "---\ntype: relation_type\nid: pod_cross\nname: 交叉\ntags: []\n---\n\n## RelationType: 交叉\n\n" +
		"### Endpoint\n\n| Source | Target | Type |\n|--------|--------|------|\n| pod | pod | filtered_cross_join |\n\n" +
		"### Source Condition\n\n```yaml\nfield: pod_name\noperation: not_empty\n```\n\n" +
		"### Target Condition\n\n```yaml\nfield: pod_name\noperation: not_empty\n```\n"
)

// withRelations declares on pod_peer a reverse name, an alias, verification, Cardinality and
// required, adds a fallback path for it and an indirect relation with properties, and deprecates the
// action type.
func withRelations(extra ...v3Edit) []v3Edit {
	return append([]v3Edit{
		{fRel, "name: 同组\ntags: []\n", "name: 同组\ntags: []\nreverse_name: 同组于\naliases: [同伴]\nverification: confirmed\n"},
		{fRel, endpointPeer, "| Source | Target | Type | Cardinality |\n|--------|--------|------|-------------|\n| pod | pod | direct | many_to_one |\n\n" +
			"### Relation Constraints\n\n```yaml\nrequired: [source]\n```"},
		{file: fGuess, new: guessFile},
		{file: fLink, new: linkFile},
		{fAct, "action_intent: modify\n", "action_intent: modify\nlifecycle:\n  status: deprecated\n  reason: 改用滚动更新\n"},
	}, extra...)
}

func relationByID(net *BknNetwork, id string) *BknRelationType {
	for _, rt := range net.RelationTypes {
		if rt.ID == id {
			return rt
		}
	}
	return nil
}

func TestLoadV3_ReadsRelationDeclarationsAndLifecycle(t *testing.T) {
	net, err := loadV3(t, withRelations()...)
	require.NoError(t, err)

	peer := relationByID(net, "pod_peer")
	assert.Equal(t, "同组于", peer.ReverseName)
	assert.Equal(t, []string{"同伴"}, peer.Aliases)
	assert.Equal(t, VerificationConfirmed, peer.Verification)
	assert.Empty(t, peer.Importance, "primary is the default")
	assert.Equal(t, CardinalityManyToOne, peer.Endpoint.Cardinality)
	assert.Equal(t, []string{EndSource}, peer.RequiredEnds)

	guess := relationByID(net, "pod_guess")
	assert.Equal(t, ImportanceSecondary, guess.Importance)
	assert.Equal(t, "pod_peer", guess.FallbackFor)
	assert.Equal(t, VerificationUnconfirmed, guess.Verification)

	link := relationByID(net, "pod_link")
	require.Len(t, link.DataProperties, 2)
	assert.Equal(t, SemanticValidFrom, link.DataProperties[0].Semantic)
	assert.Equal(t, &PropertyConstraints{Range: &ValueRange{Min: int64(0)}}, link.DataProperties[1].Constraints)

	assert.Equal(t, &Lifecycle{Status: LifecycleDeprecated, Reason: "改用滚动更新"}, net.ActionTypes[0].Lifecycle)
}

// Under 2.x the new fields are unknown and ignored, and the new sections go into the description,
// exactly as before 3.0 existed.
func TestLoadLegacy_IgnoresRelationDeclarations(t *testing.T) {
	net, err := loadV3(t, withRelations(v3Edit{fNet, "bkn_spec: \"3.0\"\n", ""})...)
	require.NoError(t, err)
	peer := relationByID(net, "pod_peer")
	assert.Empty(t, peer.ReverseName)
	assert.Empty(t, peer.Endpoint.Cardinality)
	assert.Empty(t, peer.RequiredEnds)
	assert.Contains(t, peer.Description, "### Relation Constraints")
	link := relationByID(net, "pod_link")
	assert.Empty(t, link.DataProperties)
	assert.Contains(t, link.Description, "### Data Properties")
	assert.Nil(t, net.ActionTypes[0].Lifecycle)
}

func TestLoadV3_RefusesWrongRelationDeclarations(t *testing.T) {
	node := v3Edit{file: fNode, new: definitionFile(kindObjectType, "node", "节点")}
	stopAction := v3Edit{file: "action_types/stop_pod.bkn", new: definitionFile(kindActionType, "stop_pod", "停止Pod")}
	deprecatePod := v3Edit{fObj, "tags: [k8s]\n", "tags: [k8s]\nlifecycle: {status: deprecated, reason: 改用容器组}\n"}
	symmetric := func(edits ...v3Edit) []v3Edit {
		return append([]v3Edit{{fRel, "required: [source]", "characteristics: [symmetric]"}}, edits...)
	}
	cases := []struct {
		name  string
		edits []v3Edit
		want  string
	}{
		// lifecycle
		{"lifecycle that is not a mapping", []v3Edit{{fAct, "lifecycle:\n  status: deprecated\n  reason: 改用滚动更新\n", "lifecycle: deprecated\n"}}, "frontmatter lifecycle must be a mapping with status"},
		{"lifecycle key", []v3Edit{{fAct, "  reason: 改用滚动更新\n", "  reason: 改用滚动更新\n  note: x\n"}}, `lifecycle key "note" is unknown; the keys are status, reason, replaced_by`},
		{"lifecycle without status", []v3Edit{{fAct, "  status: deprecated\n", ""}}, "lifecycle needs status: active or deprecated"},
		{"lifecycle status", []v3Edit{{fAct, "status: deprecated", "status: retired"}}, `status must be active or deprecated, got "retired"`},
		{"deprecated without reason", []v3Edit{{fAct, "  reason: 改用滚动更新\n", ""}}, "status deprecated needs a reason"},
		{"active with a reason", []v3Edit{{fAct, "status: deprecated", "status: active"}}, "reason and replaced_by go with status deprecated only"},
		{"replaced_by itself", []v3Edit{{fAct, "  reason: 改用滚动更新\n", "  reason: 改用滚动更新\n  replaced_by: restart_pod\n"}}, `replaced_by "restart_pod" is the definition itself`},
		{"replaced_by unknown", []v3Edit{{fAct, "  reason: 改用滚动更新\n", "  reason: 改用滚动更新\n  replaced_by: roll_pod\n"}}, `action type "restart_pod" lifecycle replaced_by "roll_pod": no action type has this id`},
		{"replaced_by of another kind", []v3Edit{node, {fAct, "  reason: 改用滚动更新\n", "  reason: 改用滚动更新\n  replaced_by: node\n"}}, `replaced_by "node": no action type has this id`},
		{"replaced_by deprecated", []v3Edit{stopAction,
			{"action_types/stop_pod.bkn", "tags: []\n", "tags: []\nlifecycle: {status: deprecated, reason: x}\n"},
			{fAct, "  reason: 改用滚动更新\n", "  reason: 改用滚动更新\n  replaced_by: stop_pod\n"}}, `replaced_by "stop_pod": that action type is deprecated too`},
		{"deprecated endpoint", []v3Edit{deprecatePod}, `: endpoint object type "pod" is deprecated, so the relation is deprecated too`},
		{"deprecated primary", []v3Edit{{fRel, "verification: confirmed\n", "verification: confirmed\nlifecycle: {status: deprecated, reason: 改用推测}\n"}}, `relation type "pod_guess" fallback_for "pod_peer": the relation it stands in for is deprecated`},
		// relation frontmatter
		{"reverse_name that is a list", []v3Edit{{fRel, "reverse_name: 同组于", "reverse_name: [同组于]"}}, "frontmatter reverse_name must be a single value"},
		{"reverse_name too long", []v3Edit{{fRel, "reverse_name: 同组于", "reverse_name: " + strings.Repeat("组", 41)}}, "relation type reverse_name: name length exceeds 40 characters"},
		{"alias is the reverse name", []v3Edit{{fRel, "aliases: [同伴]", "aliases: [同组于]"}}, `relation type aliases: alias "同组于" is the same as its own name`},
		{"importance", []v3Edit{{fGuess, "importance: secondary", "importance: minor"}}, `importance must be primary or secondary, got "minor"`},
		{"verification", []v3Edit{{fRel, "verification: confirmed", "verification: likely"}}, `verification must be confirmed or unconfirmed, got "likely"`},
		// fallback paths
		{"fallback_for on a primary relation", []v3Edit{{fGuess, "importance: secondary\n", ""}}, "fallback_for goes with importance: secondary"},
		{"fallback_for itself", []v3Edit{{fGuess, "fallback_for: pod_peer", "fallback_for: pod_guess"}}, `fallback_for "pod_guess" is the relation itself`},
		{"fallback_for unknown", []v3Edit{{fGuess, "fallback_for: pod_peer", "fallback_for: pod_same"}}, `relation type "pod_guess" fallback_for "pod_same": no relation type has this id`},
		{"fallback_for a secondary relation", []v3Edit{{fRel, "verification: confirmed\n", "verification: confirmed\nimportance: secondary\n"}}, "the relation it stands in for is secondary"},
		{"fallback ends differ", []v3Edit{node, {fGuess, "| pod | pod | direct |", "| pod | node | direct |"}}, "the ends differ (pod -> node and pod -> pod)"},
		{"primary without Cardinality", []v3Edit{{fRel, "| pod | pod | direct | many_to_one |", "| pod | pod | direct | |"}}, `the primary relation declares Cardinality ""; it must be many_to_one or one_to_one`},
		{"primary one_to_many", []v3Edit{{fRel, "| pod | pod | direct | many_to_one |", "| pod | pod | direct | one_to_many |"}}, `declares Cardinality "one_to_many"`},
		{"primary filtered_cross_join", []v3Edit{{file: "relation_types/pod_cross.bkn", new: strings.Replace(crossFile, "| pod | pod | filtered_cross_join |", "| pod | pod | filtered_cross_join | many_to_many |", 1)},
			{"relation_types/pod_cross.bkn", "| Source | Target | Type |\n|--------|--------|------|", "| Source | Target | Type | Cardinality |\n|--------|--------|------|-------------|"},
			{fGuess, "fallback_for: pod_peer", "fallback_for: pod_cross"}}, `declares Cardinality "many_to_many"`},
		{"fallback path filtered_cross_join", []v3Edit{{file: "relation_types/pod_cross.bkn", new: strings.Replace(crossFile, "tags: []\n", "tags: []\nimportance: secondary\nfallback_for: pod_peer\n", 1)}, {file: fGuess, new: ""}},
			`relation type fallback path "pod_cross" is a filtered_cross_join relation; fallback paths are direct or indirect`},
		{"fallback path with characteristics", []v3Edit{{fGuess, "| pod_name | pod_name |\n", "| pod_name | pod_name |\n\n### Relation Constraints\n\n```yaml\ncharacteristics: [transitive]\n```\n"}},
			`relation type fallback path "pod_guess" declares characteristics; fallback paths do not`},
		{"primary with characteristics", []v3Edit{{fRel, "required: [source]", "characteristics: [transitive]"}}, `primary relation "pod_peer" declares characteristics`},
		{"two fallback paths", []v3Edit{{file: "relation_types/pod_guess2.bkn", new: strings.NewReplacer("id: pod_guess", "id: pod_guess2", "推测同组", "再推测同组").Replace(guessFile)}},
			`is already its fallback path; a primary relation has one`},
		// Endpoint and Relation Constraints
		{"Cardinality", []v3Edit{{fRel, "| pod | pod | direct | many_to_one |", "| pod | pod | direct | n_to_1 |"}}, `Endpoint Cardinality must be one of one_to_one, one_to_many, many_to_one, many_to_many, got "n_to_1"`},
		{"Cardinality of a filtered_cross_join", []v3Edit{{file: "relation_types/pod_cross.bkn", new: strings.Replace(strings.Replace(crossFile, "| pod | pod | filtered_cross_join |", "| pod | pod | filtered_cross_join | one_to_many |", 1),
			"| Source | Target | Type |\n|--------|--------|------|", "| Source | Target | Type | Cardinality |\n|--------|--------|------|-------------|", 1)}}, "a filtered_cross_join relation can only be many_to_many"},
		{"required end", []v3Edit{{fRel, "required: [source]", "required: [both]"}}, `relation constraints required: "both" is not one of source, target`},
		{"required twice", []v3Edit{{fRel, "required: [source]", "required: [source, source]"}}, `relation constraints required: "source" appears twice`},
		{"required as a string", []v3Edit{{fRel, "required: [source]", "required: source"}}, "relation constraints required must be a list such as [source]"},
		{"relation constraints key", []v3Edit{{fRel, "required: [source]", "optional: [target]"}}, `relation constraints key "optional" is unknown; the keys are required, characteristics`},
		{"characteristic", []v3Edit{{fRel, "required: [source]", "characteristics: [reflexive]"}}, `characteristics: "reflexive" is not one of symmetric, transitive`},
		{"characteristics with different ends", []v3Edit{node, {file: fGuess, new: ""}, {fRel, "| pod | pod | direct | many_to_one |", "| pod | node | direct | many_to_one |"}, {fRel, "required: [source]", "characteristics: [transitive]"}},
			`characteristics apply to a relation whose Source and Target are the same object type, got "pod" and "node"`},
		{"symmetric many_to_one", symmetric(v3Edit{file: fGuess, new: ""}), "a symmetric relation has the same count both ways"},
		{"symmetric with reverse_name", symmetric(v3Edit{file: fGuess, new: ""}, v3Edit{fRel, "| pod | pod | direct | many_to_one |", "| pod | pod | direct | one_to_one |"}), "a symmetric relation reads the same both ways, so it has no reverse_name"},
		{"symmetric with one required end", symmetric(v3Edit{file: fGuess, new: ""}, v3Edit{fRel, "| pod | pod | direct | many_to_one |", "| pod | pod | direct | one_to_one |"},
			v3Edit{fRel, "reverse_name: 同组于\n", ""}, v3Edit{fRel, "characteristics: [symmetric]", "characteristics: [symmetric]\nrequired: [target]"}), "a symmetric relation has no direction"},
		// relation properties
		{"properties on a direct relation", []v3Edit{{fRel, "| pod_name | pod_name |\n", "| pod_name | pod_name |\n\n### Data Properties\n\n| Name | Display Name | Type | Mapped Field |\n|------|--------------|------|--------------|\n| x | X | string | x |\n"}},
			`section "### Data Properties": only indirect relations have relation properties, but Endpoint Type is direct`},
		{"unique relation property", []v3Edit{{fLink, "  range: {min: 0}\n", "  range: {min: 0}\n  unique: true\n"}}, `relation property "weight": unique does not apply, an edge has no primary key`},
		{"relation property on a join column", []v3Edit{{fLink, "| weight | 权重 | float | | | weight |", "| weight | 权重 | float | | | src_id |"}}, `relation property "weight": Mapped Field "src_id" joins the ends`},
		{"relation property Semantic", []v3Edit{{fLink, "| since | 起始 | date | valid_from |", "| since | 起始 | date | end_time |"}}, "Semantic end_time needs an event_time property alongside"},
		{"relation property constraint on an unknown property", []v3Edit{{fLink, "weight:\n", "score:\n"}}, `property constraints: "score" is not a data property of this definition`},
		{"relation property value type", []v3Edit{{fLink, "  range: {min: 0}\n", "  value_type: ratio\n"}}, `relation type "pod_link" data property "weight": value type "ratio" is not defined in this network`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadV3(t, withRelations(c.edits...)...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
			var specErr *SpecError
			assert.ErrorAs(t, err, &specErr)
		})
	}
}

// Writing a default is not another spelling: importance: primary and status: active are accepted
// and left out on export.
func TestLoadV3_AcceptsExplicitDefaults(t *testing.T) {
	net, err := loadV3(t, withRelations(
		v3Edit{fRel, "verification: confirmed\n", "verification: confirmed\nimportance: primary\nlifecycle:\n  status: active\n"},
		v3Edit{fObj, "tags: [k8s]\n", "tags: [k8s]\nlifecycle: {status: active}\n"},
	)...)
	require.NoError(t, err)
	peer := relationByID(net, "pod_peer")
	assert.Empty(t, peer.Importance)
	assert.Nil(t, peer.Lifecycle)
	assert.Nil(t, net.ObjectTypes[0].Lifecycle)

	files := exportFiles(t, net)
	assert.NotContains(t, files[fRel], "importance")
	assert.NotContains(t, files[fRel], "lifecycle")
	assert.NotContains(t, files[fObj], "lifecycle")
}

func TestWriteV3_RelationDeclarationsRoundTrip(t *testing.T) {
	net, err := loadV3(t, withRelations()...)
	require.NoError(t, err)
	first := exportFiles(t, net)

	again, err := loadFiles(first)
	require.NoError(t, err, "the exporter's own output is valid 3.0")
	assert.Equal(t, first, exportFiles(t, again))
}

func TestWriteV3_WritesRelationDeclarations(t *testing.T) {
	net, err := loadV3(t, withRelations()...)
	require.NoError(t, err)
	files := exportFiles(t, net)

	peer := files[fRel]
	assert.Contains(t, peer, "tags: []\naliases: [同伴]\nreverse_name: 同组于\nverification: confirmed\n---\n")
	assert.Contains(t, peer, "### Endpoint\n\n| Source | Target | Type | Cardinality |\n|--------|--------|------|-------------|\n| pod | pod | direct | many_to_one |\n\n"+
		"### Relation Constraints\n\n```yaml\nrequired: [source]\n```\n\n### Mapping Rules")
	assert.Contains(t, files[fGuess], "tags: []\nimportance: secondary\nfallback_for: pod_peer\nverification: unconfirmed\n---\n")
	assert.Contains(t, files[fGuess], "| Source | Target | Type |\n", "Cardinality is written only when declared")

	link := files[fLink]
	assert.Contains(t, link, "### Target Mapping\n\n| Resource Property | Target Property |\n|-------------------|-----------------|\n| dst_id | id |\n\n"+
		"### Data Properties\n\n| Name | Display Name | Type | Semantic | Description | Mapped Field |\n")
	assert.Contains(t, link, "### Property Constraints\n\n```yaml\nweight:\n  range: {min: 0}\n```\n")

	assert.Contains(t, files[fAct], "action_intent: modify\nlifecycle:\n  status: deprecated\n  reason: 改用滚动更新\n---\n")
}

// The checks import runs are the ones a save through the API runs.
func TestValidateRelationTypeDeclarations(t *testing.T) {
	rt := &BknRelationType{
		BknRelationTypeFrontmatter: BknRelationTypeFrontmatter{ID: "dept_parent", Name: "隶属于", Importance: ImportancePrimary},
		Endpoint:                   Endpoint{Source: "dept", Target: "dept", Type: RELATION_MAPPING_TYPE_DIRECT, Cardinality: CardinalityManyToOne},
		Characteristics:            []string{CharacteristicTransitive},
	}
	require.NoError(t, ValidateRelationTypeDeclarations(rt))
	rt.Characteristics = []string{CharacteristicSymmetric}
	assert.ErrorContains(t, ValidateRelationTypeDeclarations(rt), "a symmetric relation has the same count both ways")

	ot := &BknObjectType{BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{ID: "dept", Lifecycle: &Lifecycle{Status: LifecycleDeprecated}}}
	assert.ErrorContains(t, ValidateObjectTypeDeclarations(ot), "status deprecated needs a reason")
}

// Relation properties get the per-property checks of object type properties: name, display name,
// type and mask rule. Loading a 3.0 package runs them, as a save through the API does, and
// ValidateNetwork reports each one.
func TestValidateNetwork_ChecksRelationProperties(t *testing.T) {
	valid, err := loadV3(t, withRelations()...)
	require.NoError(t, err)
	res := ValidateNetwork(valid)
	assert.True(t, res.OK(), "valid relation properties pass: %+v", res.Errors)

	_, err = loadV3(t, withRelations(
		v3Edit{fLink, "| weight | 权重 | float |", "| weight | 权重 | flaot |"},
		v3Edit{fLink, "  range: {min: 0}\n", "  unit: kg\n"},
	)...)
	require.Error(t, err, "a constraint check on the misspelt type refuses it at load")

	edits := []v3Edit{
		{fLink, "| weight | 权重 | float |", "| weight | 权重 | flaot |"},
		{fLink, "### Property Constraints\n\n```yaml\nweight:\n  range: {min: 0}\n```\n", ""},
		{fLink, "| since | 起始 |", "| Since! | 起始 |"},
	}
	_, err = loadV3(t, withRelations(edits...)...)
	assert.ErrorContains(t, err, `relation property: property name "Since!" must match`)

	net, err := loadV3(t, withRelations()...)
	require.NoError(t, err)
	for _, rt := range net.RelationTypes {
		if rt.ID != "pod_link" {
			continue
		}
		for _, p := range rt.DataProperties {
			switch p.Name {
			case "weight":
				p.Type, p.Constraints = "flaot", nil
			case "since":
				p.Name = "Since!"
			}
		}
		assert.ErrorContains(t, ValidateRelationTypeDeclarations(rt), "relation property: ")
	}
	errs := map[string]string{}
	for _, e := range ValidateNetwork(net).Errors {
		if e.Table == "relation_type:pod_link" {
			errs[e.Code] = e.Message
		}
	}
	assert.Equal(t, `data property "weight" has invalid type "flaot"`, errs["invalid_property_type"])
	assert.Contains(t, errs, "invalid_property_name")
}

// NetworkRelationIssues lists every cross-definition issue, each with the definitions it concerns,
// so a save can report the ones its definitions take part in; the import check stops at the first.
func TestNetworkRelationIssues_ListsEveryIssueWithItsDefinitions(t *testing.T) {
	deprecated := &Lifecycle{Status: LifecycleDeprecated, Reason: "replaced"}
	rel := func(id, src, tgt string) *BknRelationType {
		return &BknRelationType{
			BknRelationTypeFrontmatter: BknRelationTypeFrontmatter{ID: id},
			Endpoint:                   Endpoint{Source: src, Target: tgt, Type: RELATION_MAPPING_TYPE_DIRECT},
		}
	}
	primary := rel("primary", "report", "order")
	primary.Endpoint.Cardinality = CardinalityManyToOne
	first := rel("first", "report", "order")
	first.Importance, first.FallbackFor = ImportanceSecondary, "primary"
	second := rel("second", "report", "order")
	second.Importance, second.FallbackFor = ImportanceSecondary, "primary"
	link1, link2 := rel("link1", "dept", "order"), rel("link2", "report", "dept")
	doc := &BknNetwork{
		ObjectTypes: []*BknObjectType{
			{BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{ID: "dept", Lifecycle: deprecated}},
			{BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{ID: "report"}},
			{BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{ID: "order"}},
		},
		RelationTypes: []*BknRelationType{primary, first, second, link1, link2},
	}

	issues := NetworkRelationIssues(doc)
	require.Len(t, issues, 3)
	assert.ErrorContains(t, issues[0].Err, `relation type "second" fallback_for "primary": relation type "first" is already its fallback path`)
	assert.True(t, issues[0].Involves(kindRelationType, "first"), "the fallback path that holds the place is involved too")
	assert.True(t, issues[0].Involves(kindRelationType, "primary"))
	assert.ErrorContains(t, issues[1].Err, `relation type "link1": endpoint object type "dept" is deprecated`)
	assert.ErrorContains(t, issues[2].Err, `relation type "link2": endpoint object type "dept" is deprecated`)
	for _, issue := range issues[1:] {
		assert.True(t, issue.Involves(kindObjectType, "dept"), "deprecating the object type is what each relation conflicts with")
		assert.False(t, issue.Involves(kindRelationType, "primary"))
	}
	assert.Equal(t, issues[0].Err, validateNetworkRelations(doc), "the import check refuses on the first issue")
	assert.Empty(t, NetworkRelationIssues(&BknNetwork{RelationTypes: []*BknRelationType{primary, first}}))
}
