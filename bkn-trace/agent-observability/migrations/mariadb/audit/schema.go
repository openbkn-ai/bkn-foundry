// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

// Package audit contains the versioned monthly Audit Ledger table templates.
package audit

import _ "embed"

//go:embed audit_event_template.sql
var monthlyTemplateV032 string

func TemplateSQL() string { return monthlyTemplateV032 }
