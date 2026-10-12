# Standard System Audit installation repair

**Goal:** Complete platform installation and upgrades with both Audit publishing and consumption configured; Audit is independent of Trace/Evidence capture state.

**Approved scope:** Installer and focused deployment regressions only. Reuse the current runtime, registry, Chart, MariaDB, Kafka client Secret and Helm merge. No new accounts, ACL changes, runtime framework, offset reset or historical migration.

**Design:** Generate non-sensitive defaults below saved connection configuration and current config/CLI. Repair historical disabled deployment settings, but reject a current explicit disable in complete platform installation. Preserve existing valid connection fields. Require an explicit registered publisher environment and production write identity when no trustworthy existing configuration exists. Render once before validating/deciding whether an equal-version release can skip. Prepare only the configured Audit topic, requiring LogAppendTime; never alter an incompatible existing topic. Keep standalone Chart defaults unchanged.

**Tech stack:** Existing Bash installer, Python stdlib helper, Helm and bundled Kafka tools.

- [x] Inspect main and live 8081; verify the existing regression baseline.
- [x] Add failing regressions for default completion, publisher preservation, disabled historical values, explicit disable rejection, Secret/environment validation and equal-version reconciliation.
- [x] Extend audit_consumer.py to handle the small shared System Audit profile; avoid a redundant publisher helper.
- [x] Integrate defaults, rendered preflight and topic preparation in openbkn.sh, with configuration-sensitive version skipping.
- [x] Run installer/Chart/helper tests, syntax and lint checks; update operator documentation and existing CI.
- [x] Review working-tree diff. Live rollout and real event mutation remain separate Owner-approved operations; do not modify the working 8081 environment during this repair.

## Verification and handoff

Installer, configuration, prerequisites, standalone Audit profile and Agent Evidence shell regressions passed. Python helper tests (15) and audit verifier tests (6), targeted Go boot/HTTP handler/validator/consumer tests, Helm lint, Python lint, shell syntax and diff whitespace checks passed. Independent read-only review found no blocking issues.

The complete installation driver now propagates release failure even when invoked in a shell conditional; its regression first reproduced false success and then passed after the fix.

PR review follow-up: Helm user values also contain old installer-generated defaults. Complete-install preservation now excludes MQ-derived brokers and SASL mechanism while retaining environment, group and Secret references. Focused regressions first reproduced stale connections, then verified changed shared MQ configuration causes equal-version reconciliation and current component-specific inputs still win.

No live deployment, Kafka mutation or real management action was performed during this repair. Fresh-install and upgrade acceptance must use the changed installer and verify actual event publication, ledger persistence and Studio results. A successful topic preflight alone does not prove producer/group ACLs.
