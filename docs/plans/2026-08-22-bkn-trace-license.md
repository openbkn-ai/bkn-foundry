# BKN Trace License and README Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make the Foundry Community BKN Trace module's OpenBKN licensing and
public README accurate, machine-readable, and continuously verified while
confirming Studio's BKN Trace module already meets the same header convention.

**Architecture:** Add a module-scoped, read-only license-header checker in
Foundry and apply one OpenBKN header format appropriate to each file syntax.
Rewrite only the two public module README files from the approved 0.1.4
Community architecture. Studio uses its existing repository-wide checker and
is changed only if its BKN Trace files fail that check.

**Tech Stack:** Go, Python, Shell, Dockerfile, Helm/YAML, Node.js, existing Go
test and Helm tooling.

---

### Task 1: Classify every tracked Foundry BKN Trace artifact

**Files:**
- Create: `bkn-trace/license-header-exclusions.json`
- Create: `bkn-trace/agent-observability/scripts/check_license_headers.py`

**Step 1:** Enumerate tracked `bkn-trace/**` files from `origin/main` and
classify each as OpenBKN-commentable, generated, third-party, or
non-commentable.

**Step 2:** Record every generated, third-party, JSON, and lock-file exclusion
with its reason in `license-header-exclusions.json`; do not use broad,
unexplained directory exclusions.

**Step 3:** Implement a read-only checker that discovers only tracked
commentable files, applies the explicit exclusion list, and requires the
copyright, SPDX, and OpenBKN license markers.

**Step 4:** Run the checker before adding headers; it must report the exact
missing or mismatched files.

### Task 2: Apply syntax-correct OpenBKN headers in Foundry

**Files:**
- Modify: OpenBKN-authored commentable files under `bkn-trace/**`

**Step 1:** Add this semantic header to Go files:

```go
// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.
```

**Step 2:** Use `#` for Dockerfile, Python, Shell, Makefile and YAML; insert
after a shebang when present.

**Step 3:** Use the non-rendering Go-template comment form for Helm template
files, so rendered manifests are unchanged.

**Step 4:** Preserve existing file content, generated-file exclusions, and
third-party attribution. Do not add comments to JSON or lock files.

**Step 5:** Rerun the checker and require a zero-missing result.

### Task 3: Rewrite the public BKN Trace README pair

**Files:**
- Modify: `bkn-trace/README.md`
- Modify: `bkn-trace/README.zh.md`

**Step 1:** Replace the legacy product name and Apache references with `BKN
Trace` and an OpenBKN License badge/link.

**Step 2:** Describe only the Community BKN Trace scope from the approved
0.1.4 architecture: first-hand execution facts, Trace Core APIs, technical
trace analysis, access enforcement, and log correlation.

**Step 3:** Link only to public local module documentation and Foundry's
`LICENSE-OPENBKN.txt`. Remove dead `LICENSE.txt` references and all references
to non-public repositories, products, images, or implementations.

**Step 4:** Check the English and Chinese documents for equivalent public
claims, no `Tracing AI` legacy name, no upstream-brand text, and valid relative
links.

### Task 4: Verify Studio without broadening its scope

**Files:**
- Verify: `src/modules/bkn-trace/**` in the Studio worktree

**Step 1:** Run Studio's existing `node scripts/check-license-headers.mjs` from
the Studio repository root.

**Step 2:** If BKN Trace is complete, make no Studio source change. If a
module-local file is missing the required marker, apply the existing Studio
header exactly and rerun the checker.

### Task 5: Run module verification and review the change

**Files:**
- Verify: all modified Foundry and Studio files

**Step 1:** Run the Foundry license checker, `go test ./...` from
`bkn-trace/agent-observability`, and Helm `lint`/`template` for both charts.

**Step 2:** Run Studio's license checker and the path-relevant quality checks
required by its repository instructions.

**Step 3:** Run `git diff --check`, inspect every changed file for accidental
non-public references, and present the uncommitted diff for requester review.

**Step 4:** Do not commit, push, or open a pull request until the requester
approves that diff.
