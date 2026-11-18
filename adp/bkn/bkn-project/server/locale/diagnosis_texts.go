// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

// DiagnosisTexts are the server's wording of network diagnosis suggestions, by the draft's naming
// language (the content language of the project, not the reader's), so they are kept by language
// code rather than registered as reader messages.
var DiagnosisTexts = map[string]map[string]string{
	"zh-CN": {
		"relation_description":   "每条%s记录通过「%[3]s」对应一个%[2]s。",
		"condition":              "仅当「%s」为 %s 时成立。",
		"condition_title":        "（%s = %s）",
		"reference_reason":       "「%s」的取值有 %d/%d（%s）落在%s的「%s」里。",
		"subtype_name":           "对应%s",
		"subtype_description":    "每个%s就是一个%s；平台支持继承前，用这条一对一关系表达这层包含关系。",
		"subtype_reason":         "%s的 %d 个标识全部是%s的标识：它是一种%[3]s。",
		"adopt":                  "采纳 %s",
		"adopt_relation_reason":  "已起草，只是未采纳。",
		"adopt_relation_counted": "已起草，全表 %d/%d（%s）找得到对应记录，只是未采纳。",
		"adopt_object_reason":    "采纳后带入 %d 条已起草的关系。",
		"identity_title":         "为%s选定唯一标识并采纳",
		"identity_reason":        "还没定唯一标识，所以进不了模型；定下后带入 %d 条已起草的关系。",
		"identity_candidates":    "全表校验唯一的候选：%s。",
		"or":                     "；",
		"exclude_title":          "%s不纳入",
		"exclude_reason":         "没有对象引用它，也没有找到它引用的对象；如果它不是业务对象，判为不纳入。",
		"quality_title":          "%d/%d 条%s没有填写「%s」",
		"quality_reason":         "关系照常建立；这些记录在追溯时会断链，建议在源系统补录或确认原因。",
	},
	"en-US": {
		"relation_description":   "Each %s record refers to one %s by %s.",
		"condition":              " Holds only when %s is %s.",
		"condition_title":        " (%s = %s)",
		"reference_reason":       "%[2]d of %[3]d values of %[1]s (%[4]s) are %[6]s values of %[5]s.",
		"subtype_name":           "%s",
		"subtype_description":    "Each %s is a %s; until inheritance is supported, this one-to-one relation states it.",
		"subtype_reason":         "All %[2]d identities of %[1]s are identities of %[3]s: it is a kind of %[3]s.",
		"adopt":                  "Adopt %s",
		"adopt_relation_reason":  "Drafted but not adopted yet.",
		"adopt_relation_counted": "Drafted but not adopted yet; %d of %d records (%s) find their counterpart over the full table.",
		"adopt_object_reason":    "Adopting it brings in %d drafted relations.",
		"identity_title":         "Choose the identity of %s and adopt it",
		"identity_reason":        "It has no identity yet, so it stays out of the model; with one it brings in %d drafted relations.",
		"identity_candidates":    " Candidates unique over the full table: %s.",
		"or":                     "; ",
		"exclude_title":          "Leave %s out",
		"exclude_reason":         "Nothing refers to it and it refers to nothing; if it is no business object, leave it out.",
		"quality_title":          "%d of %d %s records have no %s",
		"quality_reason":         "The relation still holds; these records break the trace. Fill them in at the source or confirm why they are empty.",
	},
}
