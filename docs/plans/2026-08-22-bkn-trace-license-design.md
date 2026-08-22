# BKN Trace License and README Design

## Goal

Make the OpenBKN ownership and licensing of the Foundry Community BKN Trace
module explicit, machine-readable, and verifiable; retain the existing Studio
license convention; and describe BKN Trace only through its public Community
responsibilities.

## Scope and boundaries

- `bkn-foundry/bkn-trace/` is OpenBKN-authored Community code and uses the
  OpenBKN License.
- All OpenBKN-authored, commentable source and deployment files in that module
  receive a file-level copyright notice and
  `SPDX-License-Identifier: LicenseRef-OpenBKN`.
- JSON, lock files, generated outputs, and audited third-party material are
  excluded by an explicit, tested allowlist; they are never modified merely to
  add a comment.
- `bkn-studio/src/modules/bkn-trace/` retains the existing Studio header and
  checker. The work verifies its coverage and changes it only if the latest
  main branch contains a genuine omission.
- Public Foundry documentation must not identify, link to, or describe any
  non-public repository or implementation. It only states the public
  Community BKN Trace responsibilities and contracts.

## License header decision

Studio's established markers are the common machine-readable convention:

```text
Copyright (c) 2026 OpenBKN
SPDX-License-Identifier: LicenseRef-OpenBKN
Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
Conditions.
```

Foundry uses the same three markers. Its final line points to
`LICENSE-OPENBKN.txt`, because that file contains the complete OpenBKN license
text whereas Foundry's root `LICENSE` is a multi-license overview.

## README decision

The module README is rewritten around the 0.1.4 architecture: it introduces
the Community Trace Core, its recorded execution facts, the public Trace API,
technical trace analysis, access control, and log correlation. It removes the
legacy `Tracing AI` product name, aspirational features that are not current
contracts, the old Apache wording, and references to an absent `LICENSE.txt`.

## Verification

A module-local checker must fail for missing or mismatched required markers,
missing exclusions, or unintended legacy branding in the two root README
files. It must not alter files. The implementation additionally runs Go tests,
Helm rendering checks, and Studio's existing license checker in the Studio
worktree.
