// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

// The words below are vocabulary the service reads business names by, in both languages it names
// ontology items in. They are data about names, not copy shown to readers.

// RelationCodeWords are the words that end the name of a field holding another record's code
// rather than naming a role: 物料编码, 客户ID, Supplier No., CustomerID. Longer words come first, so
// 编号 is taken before 号.
var RelationCodeWords = []string{"编码", "编号", "代码", "号码", "number", "code", "no.", "key", "id", "号"}
