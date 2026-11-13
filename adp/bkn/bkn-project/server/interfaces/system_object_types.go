// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

// A system object type is one BKN-project writes on a project's branch for agents to read, beside
// the model a person drafts: it is published with every release but is no business content, so
// the lists a person reviews (what the network already has, its indexes, a release's changes)
// leave it out. Its id is the same in every network of a lineage and drafting never gives it.
//
// The term library of the professional edition is one: it writes its confirmed spellings for
// agents through the domain term object type. Branches already carry it under this id, with no
// other mark, so the id is what tells it.
const SYSTEM_OBJECT_TYPE_DOMAIN_TERMS = "bkn_domain_terms"

var systemObjectTypes = map[string]bool{SYSTEM_OBJECT_TYPE_DOMAIN_TERMS: true}

// IsSystemObjectType tells whether id is the id of a system object type.
func IsSystemObjectType(id string) bool { return systemObjectTypes[id] }

// SystemObjectTypeIDs lists the ids of the system object types.
func SystemObjectTypeIDs() []string { return []string{SYSTEM_OBJECT_TYPE_DOMAIN_TERMS} }
