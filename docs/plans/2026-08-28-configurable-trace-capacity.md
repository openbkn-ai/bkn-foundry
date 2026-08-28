# Configurable BKN Trace Interaction Capacity Implementation Plan

> **For Codex:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make BKN Trace per-Interaction Operation, Claim, and evidence-reference capacity deployment-configurable, with safe defaults of 256, 32, and 4,096.

**Architecture:** Helm values render three Core environment variables. `conf.CoreConfig` parses and validates them once at startup; bootstrap supplies the resulting capacity object to `sessionsvc`, which applies it to the existing three enforcement points.

**Tech Stack:** Go, Helm, Go unit tests, Helm template/lint verification.

---

### Task 1: Define and parse capacity configuration

**Files:**
- Modify: `bkn-trace/agent-observability/src/conf/core.go`
- Test: `bkn-trace/agent-observability/src/conf/core_test.go`

**Step 1:** Add failing tests for default `256/32/4096`, valid environment overrides, and invalid relationship rejection.

**Step 2:** Run `go test ./src/conf` and confirm the new tests fail.

**Step 3:** Add a typed capacity config and strict integer/relationship validation to `CoreConfig` parsing.

**Step 4:** Run `go test ./src/conf` and confirm it passes.

### Task 2: Apply configuration to lifecycle enforcement

**Files:**
- Modify: `bkn-trace/agent-observability/src/domain/service/sessionsvc/service.go`
- Modify: `bkn-trace/agent-observability/src/boot/bootstrap.go`
- Test: `bkn-trace/agent-observability/src/domain/service/sessionsvc/service_test.go`

**Step 1:** Add failing service tests that prove custom Operation, Claim, and evidence-reference thresholds are enforced.

**Step 2:** Run the focused session-service tests and confirm they fail.

**Step 3:** Replace the constants with a validated `CapacityLimits` option, retain defaults for direct unit-test construction, and wire CoreConfig through bootstrap.

**Step 4:** Run focused session-service and bootstrap tests and confirm they pass.

### Task 3: Expose and document deployment configuration

**Files:**
- Modify: `bkn-trace/agent-observability/charts/agent-observability/values.yaml`
- Modify: `bkn-trace/agent-observability/charts/agent-observability/templates/deployment.yaml`
- Modify: `bkn-trace/agent-observability/README.md`
- Modify: `bkn-docs/docs/foundry/bkn-trace/archive/0.1.3/testing/BKN Trace 0.1.3 容量与恢复基线.md`

**Step 1:** Add a chart-render assertion for the three environment variables.

**Step 2:** Render/lint the chart and confirm the assertion initially fails.

**Step 3:** Add documented defaults to values, render the variables, and document the restart requirement and validation behavior.

**Step 4:** Run chart render/lint and confirm they pass.

### Task 4: Verify and prepare the isolated PR

**Files:**
- Verify: files above

**Step 1:** Run `GOCACHE=/tmp/openbkn-go-build-cache GOMODCACHE=/tmp/openbkn-go-mod-cache go test ./...` from `bkn-trace/agent-observability`.

**Step 2:** Run the chart tests and `helm lint charts/agent-observability`.

**Step 3:** Inspect `git diff` and `git status`; ensure only this feature's code, tests, documentation, and plans are staged.

**Step 4:** Commit with `feat(trace): configure interaction capacity limits`, push the isolated branch, and open a PR describing the defaults, configuration keys, tests, and the absence of API/migration changes.
