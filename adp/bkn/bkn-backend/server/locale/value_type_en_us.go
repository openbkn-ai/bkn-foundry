// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var valueTypeEnUS = i18n.I18nMessages{Locale: "en-US", Messages: map[string]string{
	"BknBackend.ValueType.InvalidParameter":     "Invalid Value Type Please check the value type against the BKN specification, section 16.",
	"BknBackend.ValueType.Duplicated.IDInFile":  "Same Value Type ID Existed Please check whether the parameter is correct.",
	"BknBackend.ValueType.Duplicated.Name":      "Same Value Type Name Existed Please check whether the parameter is correct.",
	"BknBackend.ValueType.ValueTypeIDExisted":   "Same Value Type ID Existed Please check whether the parameter is correct.",
	"BknBackend.ValueType.ValueTypeNameExisted": "Same Value Type Name Existed Please check whether the parameter is correct.",
	"BknBackend.ValueType.NullParameter.Name":   "Value Type Name Is Empty Please check whether the parameter is correct.",
	"BknBackend.ValueType.LengthExceeded.Name":  "The Length of Value Type Name Out of Limit Please check whether the parameter is correct.",
	"BknBackend.ValueType.ValueTypeNotFound":    "Value Type Not Found Please check whether the value type ID is correct.",
	"BknBackend.ValueType.Referenced":           "Value Type Is Referenced Data properties listed in the error details use this value type. Change their constraints first, then delete the value type or change its base_type.",
	"BknBackend.ValueType.InternalError":        "Internal Error Please contact the administrator.",
}}
