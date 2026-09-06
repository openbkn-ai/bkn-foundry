#!/usr/bin/env python3
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Run the resumable OpenBKN 0.1.4 to 0.1.5 knowledge-network migration."""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Mapping, Optional, Sequence


MIGRATION_ID = "openbkn-0.1.4-to-0.1.5"
ROOT = Path(__file__).resolve().parents[4]
AUTHZ_SCRIPT = ROOT / "adp/bkn/bkn-backend/script/migrate_kn_authz/script.py"
SCHEMA_SCRIPT = ROOT / "migrations/bkn-backend/mariadb/0.1.5/03-kn-proxy-account.sql"
API_PREFIX = "/api/bkn-backend/in/v1"
EXPECTED_SCHEMA_COLUMNS = {
    "f_kn_id",
    "f_proxy_account_id",
    "f_proxy_account_type",
    "f_lifecycle_status",
    "f_version",
    "f_sync_status",
    "f_published_model_version",
    "f_synced_model_version",
    "f_last_sync_error",
    "f_last_grantor_id",
    "f_lock_owner",
    "f_lock_until",
    "f_created_at",
    "f_updated_at",
}
PHASE_STAGE = {
    "plan": "plan",
    "schema": "schema",
    "caller-authz": "caller_authorization",
    "proxy-backfill": "proxy_backfill",
    "verify": "verify",
    "rollback": "rollback",
}


class MigrationError(RuntimeError):
    """Raised when a phase cannot proceed safely."""


def now() -> str:
    return datetime.now(timezone.utc).isoformat()


def new_state() -> dict[str, Any]:
    return {
        "migration": MIGRATION_ID,
        "source_version": "0.1.4",
        "target_version": "0.1.5",
        "created_at": now(),
        "updated_at": now(),
        "safe_to_enable_proxy": False,
        "stages": {},
        "caller_authorization": {},
        "networks": {},
        "verification": {},
        "rollback": {},
        "conflicts": [],
        "failures": [],
    }


def load_state(path: Path) -> dict[str, Any]:
    if not path.exists():
        return new_state()
    state = json.loads(path.read_text(encoding="utf-8"))
    if state.get("migration") != MIGRATION_ID:
        raise MigrationError(f"state file is not for {MIGRATION_ID}")
    return state


def save_state(path: Path, state: dict[str, Any]) -> None:
    state["updated_at"] = now()
    path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(
        "w", encoding="utf-8", dir=path.parent, delete=False
    ) as handle:
        json.dump(state, handle, ensure_ascii=False, indent=2, sort_keys=True)
        handle.write("\n")
        temporary = Path(handle.name)
    temporary.replace(path)


def record_failure(
    state: dict[str, Any], phase: str, detail: str, kn_id: str = ""
) -> None:
    state["failures"].append(
        {"phase": phase, "kn_id": kn_id, "detail": detail, "at": now()}
    )


def request_json(
    base_url: str,
    path: str,
    user_id: str,
    method: str = "GET",
) -> Any:
    request = urllib.request.Request(
        urllib.parse.urljoin(base_url.rstrip("/") + "/", path.lstrip("/")),
        method=method,
        headers={"x-account-id": user_id, "x-account-type": "user"},
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            payload = response.read()
    except urllib.error.HTTPError as error:
        body = error.read().decode("utf-8", errors="replace")[:500]
        raise MigrationError(
            f"{method} {path} returned HTTP {error.code}: {body}"
        ) from error
    except urllib.error.URLError as error:
        raise MigrationError(f"{method} {path} failed: {error.reason}") from error
    return json.loads(payload) if payload else None


def require_file(path: Optional[Path], description: str) -> None:
    if path is None or not path.is_file() or path.stat().st_size == 0:
        raise MigrationError(f"{description} must reference a non-empty file")


def require_offline(args: argparse.Namespace, caller_phase: bool = False) -> None:
    if not args.workloads_stopped:
        raise MigrationError("offline phase requires --workloads-stopped")
    require_file(args.database_backup, "--database-backup")
    if caller_phase:
        require_file(args.caller_export, "--caller-export")


def require_online(args: argparse.Namespace) -> None:
    if not args.runtime_ready:
        raise MigrationError("online phase requires --runtime-ready")
    if args.proxy_mode != "off":
        raise MigrationError("proxy mode must remain off until verify completes")


def database_options(args: argparse.Namespace) -> list[str]:
    options: list[str] = []
    for prefix in ("bkn_db", "safe_db"):
        option = prefix.replace("_", "-")
        for name in ("host", "port", "user", "name"):
            options.extend(
                [f"--{option}-{name}", str(getattr(args, f"{prefix}_{name}"))]
            )
        password = getattr(args, f"{prefix}_password")
        if password:
            options.extend([f"--{option}-password", password])
    return options


def run_caller_migration(args: argparse.Namespace, apply: bool) -> Mapping[str, Any]:
    with tempfile.NamedTemporaryFile(suffix=".json", delete=False) as handle:
        report_path = Path(handle.name)
    command = [
        sys.executable,
        str(AUTHZ_SCRIPT),
        "--apply" if apply else "--dry-run",
        "--report",
        str(report_path),
        *database_options(args),
    ]
    try:
        completed = subprocess.run(command, text=True, capture_output=True, check=False)
        if not report_path.exists() or report_path.stat().st_size == 0:
            detail = completed.stderr.strip() or completed.stdout.strip()
            raise MigrationError(f"caller authorization migration failed: {detail}")
        report = json.loads(report_path.read_text(encoding="utf-8"))
        if completed.returncode != 0:
            raise MigrationError(
                f"caller authorization migration returned {completed.returncode}: {report}"
            )
        return report
    finally:
        report_path.unlink(missing_ok=True)


def connect_bkn_database(args: argparse.Namespace):
    try:
        import pymysql
    except ImportError as error:
        raise MigrationError("PyMySQL 1.1.0 is required for schema phases") from error
    return pymysql.connect(
        host=args.bkn_db_host,
        port=args.bkn_db_port,
        user=args.bkn_db_user,
        password=args.bkn_db_password or os.getenv("BKN_DB_PASSWORD", ""),
        database=args.bkn_db_name,
        charset="utf8mb4",
        autocommit=False,
    )


def inspect_schema(connection: Any) -> dict[str, Any]:
    with connection.cursor() as cursor:
        cursor.execute("SHOW TABLES LIKE 't_kn_proxy_account'")
        exists = cursor.fetchone() is not None
        columns: set[str] = set()
        indexes: set[str] = set()
        if exists:
            cursor.execute("SHOW COLUMNS FROM t_kn_proxy_account")
            columns = {str(row[0]) for row in cursor.fetchall()}
            cursor.execute("SHOW INDEX FROM t_kn_proxy_account")
            indexes = {str(row[2]) for row in cursor.fetchall()}
    missing_columns = sorted(EXPECTED_SCHEMA_COLUMNS - columns) if exists else sorted(EXPECTED_SCHEMA_COLUMNS)
    return {
        "table": "t_kn_proxy_account",
        "exists": exists,
        "missing_columns": missing_columns,
        "unique_proxy_index": "uk_kn_proxy_account_proxy" in indexes,
        "ready": exists and not missing_columns and "uk_kn_proxy_account_proxy" in indexes,
    }


def schema_phase(args: argparse.Namespace, state: dict[str, Any]) -> None:
    connection = connect_bkn_database(args)
    try:
        before = inspect_schema(connection)
        if args.apply:
            require_offline(args)
            with connection.cursor() as cursor:
                cursor.execute(SCHEMA_SCRIPT.read_text(encoding="utf-8"))
            connection.commit()
        after = inspect_schema(connection)
    except Exception:
        connection.rollback()
        raise
    finally:
        connection.close()
    status = "completed" if after["ready"] else "planned"
    if args.apply and not after["ready"]:
        raise MigrationError(f"schema verification failed: {after}")
    state["stages"]["schema"] = {"status": status, "before": before, "after": after}


def caller_phase(args: argparse.Namespace, state: dict[str, Any]) -> None:
    if args.apply:
        require_offline(args, caller_phase=True)
        if state["stages"].get("schema", {}).get("status") != "completed":
            raise MigrationError("schema apply phase must complete before caller authorization")
    report = run_caller_migration(args, args.apply)
    state["caller_authorization"] = report
    state["stages"]["caller_authorization"] = {
        "status": "completed" if args.apply else "planned",
        "database_backup": str(args.database_backup) if args.database_backup else "",
        "caller_export": str(args.caller_export) if args.caller_export else "",
    }


def list_networks(args: argparse.Namespace) -> list[str]:
    listing = request_json(
        args.backend_url,
        f"{API_PREFIX}/knowledge-networks?limit=-1&offset=0",
        args.user_id,
    )
    return sorted(str(entry["id"]) for entry in listing.get("entries", []))


def proxy_plan(args: argparse.Namespace, kn_id: str) -> Mapping[str, Any]:
    quoted = urllib.parse.quote(kn_id, safe="")
    return request_json(
        args.backend_url,
        f"{API_PREFIX}/knowledge-networks/{quoted}/proxy-account/plan",
        args.user_id,
    )


def plan_phase(args: argparse.Namespace, state: dict[str, Any]) -> None:
    require_online(args)
    state["caller_authorization"] = run_caller_migration(args, False)
    ownership_conflicts: list[dict[str, str]] = []
    for kn_id in list_networks(args):
        plan = proxy_plan(args, kn_id)
        existing = state["networks"].get(kn_id, {})
        if plan.get("proxy_account_id") and "created_by_migration" not in existing:
            ownership_conflicts.append(
                {
                    "type": "existing_proxy_without_checkpoint",
                    "kn_id": kn_id,
                    "proxy_account_id": str(plan["proxy_account_id"]),
                }
            )
        state["networks"][kn_id] = {
            **existing,
            "kn_id": kn_id,
            "proxy_account_id": plan.get("proxy_account_id", ""),
            "created_by_migration": existing.get(
                "created_by_migration", not bool(plan.get("proxy_account_id"))
            ),
            "model_version": plan.get("model_version", ""),
            "authorization_sources": plan.get("sources", []),
            "backfill_status": existing.get("backfill_status", "planned"),
        }
    state["conflicts"] = ownership_conflicts
    state["stages"]["plan"] = {
        "status": "failed" if ownership_conflicts else "completed",
        "networks": len(state["networks"]),
        "conflicts": len(ownership_conflicts),
    }
    if ownership_conflicts:
        raise MigrationError(
            "existing proxy mappings lack migration ownership checkpoints; rollback would be unsafe"
        )


def proxy_backfill_phase(args: argparse.Namespace, state: dict[str, Any]) -> None:
    require_online(args)
    if state["stages"].get("caller_authorization", {}).get("status") != "completed":
        raise MigrationError("caller-authorization apply phase must complete before proxy backfill")
    if state["stages"].get("plan", {}).get("status") != "completed":
        raise MigrationError("a conflict-free plan phase must complete before proxy backfill")
    failures = 0
    for kn_id in sorted(state["networks"]):
        item = state["networks"][kn_id]
        if item.get("backfill_status") == "ready":
            continue
        quoted = urllib.parse.quote(kn_id, safe="")
        try:
            mapping = request_json(
                args.backend_url,
                f"{API_PREFIX}/knowledge-networks/{quoted}/proxy-account/sync",
                args.user_id,
                method="POST",
            )
            item.update(
                {
                    "proxy_account_id": mapping.get("proxy_account_id", ""),
                    "lifecycle_status": mapping.get("lifecycle_status", ""),
                    "backfill_status": mapping.get("sync_status", "failed"),
                    "published_model_version": mapping.get("published_model_version", ""),
                    "synced_model_version": mapping.get("synced_model_version", ""),
                }
            )
            if item["backfill_status"] != "ready":
                raise MigrationError(f"sync status is {item['backfill_status']}")
        except Exception as error:
            failures += 1
            item["backfill_status"] = "failed"
            item["failure"] = str(error)
            record_failure(state, "proxy_backfill", str(error), kn_id)
        save_state(args.state, state)
    state["stages"]["proxy_backfill"] = {
        "status": "completed" if failures == 0 else "failed",
        "failures": failures,
    }
    if failures:
        raise MigrationError(f"proxy backfill failed for {failures} knowledge network(s)")


def verify_managed_account(args: argparse.Namespace, proxy_id: str) -> Mapping[str, Any]:
    quoted = urllib.parse.quote(proxy_id, safe="")
    return request_json(
        args.safe_url,
        f"/api/safe/in/v1/managed-proxy-accounts/{quoted}",
        args.user_id,
    )


def verify_phase(args: argparse.Namespace, state: dict[str, Any]) -> None:
    require_online(args)
    if state["stages"].get("proxy_backfill", {}).get("status") != "completed":
        raise MigrationError("proxy backfill must complete before verify")
    if not args.safe_url:
        raise MigrationError("verify requires --safe-url")
    if sorted(args.pep_ready) != ["execution-factory", "vega"]:
        raise MigrationError("verify requires --pep-ready vega --pep-ready execution-factory")

    reconcile = request_json(
        args.backend_url,
        f"{API_PREFIX}/proxy-accounts/reconcile",
        args.user_id,
        method="POST",
    )
    defects = {
        key: reconcile.get(key)
        for key in (
            "missing_mappings",
            "orphan_mappings",
            "conflicting_proxy_accounts",
            "authorization_drift",
            "errors",
        )
        if reconcile.get(key)
    }
    failures: list[dict[str, str]] = []
    for kn_id in sorted(state["networks"]):
        item = state["networks"][kn_id]
        try:
            quoted = urllib.parse.quote(kn_id, safe="")
            mapping = request_json(
                args.backend_url,
                f"{API_PREFIX}/knowledge-networks/{quoted}/proxy-account",
                args.user_id,
            )
            latest_plan = proxy_plan(args, kn_id)
            account = verify_managed_account(args, mapping["proxy_account_id"])
            checks = {
                "unique_proxy": mapping["proxy_account_id"] == item["proxy_account_id"],
                "mapping_active": mapping.get("lifecycle_status") == "active",
                "sync_ready": mapping.get("sync_status") == "ready",
                "latest_model_synced": mapping.get("published_model_version")
                == mapping.get("synced_model_version")
                == latest_plan.get("model_version"),
                "source_plan_unchanged": latest_plan.get("sources", [])
                == item.get("authorization_sources", []),
                "managed_no_login": not account.get("login_enabled", True),
                "managed_no_credentials": not account.get(
                    "credential_issuance_enabled", True
                ),
                "managed_enabled": account.get("enabled") is True,
            }
            item["verification"] = checks
            failed_checks = sorted(key for key, value in checks.items() if not value)
            if failed_checks:
                failures.append({"kn_id": kn_id, "detail": ", ".join(failed_checks)})
        except Exception as error:
            failures.append({"kn_id": kn_id, "detail": str(error)})
        save_state(args.state, state)
    if defects:
        failures.append({"kn_id": "", "detail": f"reconcile defects: {defects}"})
    state["verification"] = {
        "reconcile": reconcile,
        "pep_ready": sorted(args.pep_ready),
        "failures": failures,
    }
    state["stages"]["verify"] = {
        "status": "completed" if not failures else "failed"
    }
    state["safe_to_enable_proxy"] = not failures
    if failures:
        raise MigrationError(f"verification failed: {failures}")


def rollback_phase(args: argparse.Namespace, state: dict[str, Any]) -> None:
    require_online(args)
    if not args.safe_url:
        raise MigrationError("rollback requires --safe-url to verify archived accounts")
    failures: list[dict[str, str]] = []
    archived: list[str] = []
    for kn_id in sorted(state["networks"]):
        item = state["networks"][kn_id]
        if not item.get("created_by_migration") or item.get("rollback_status") == "archived":
            continue
        quoted = urllib.parse.quote(kn_id, safe="")
        try:
            request_json(
                args.backend_url,
                f"{API_PREFIX}/knowledge-networks/{quoted}/proxy-account/rollback",
                args.user_id,
                method="POST",
            )
            mapping = request_json(
                args.backend_url,
                f"{API_PREFIX}/knowledge-networks/{quoted}/proxy-account",
                args.user_id,
            )
            account = verify_managed_account(args, mapping["proxy_account_id"])
            if mapping.get("lifecycle_status") != "archived" or any(
                account.get(field, True)
                for field in ("enabled", "login_enabled", "credential_issuance_enabled")
            ):
                raise MigrationError("rollback verification found an enabled managed proxy")
            item["rollback_status"] = "archived"
            archived.append(kn_id)
        except Exception as error:
            item["rollback_status"] = "failed"
            failures.append({"kn_id": kn_id, "detail": str(error)})
            record_failure(state, "rollback", str(error), kn_id)
        save_state(args.state, state)
    state["safe_to_enable_proxy"] = False
    state["rollback"] = {
        "archived_migration_created_proxies": archived,
        "caller_authorization_restored": False,
        "failures": failures,
    }
    state["stages"]["rollback"] = {
        "status": "completed" if not failures else "failed"
    }
    if failures:
        raise MigrationError(f"rollback failed for {len(failures)} knowledge network(s)")


def add_database_arguments(parser: argparse.ArgumentParser, prefix: str, default_name: str) -> None:
    option = prefix.replace("_", "-")
    env_prefix = prefix.upper()
    parser.add_argument(f"--{option}-host", default=os.getenv(f"{env_prefix}_HOST", "localhost"))
    parser.add_argument(f"--{option}-port", type=int, default=int(os.getenv(f"{env_prefix}_PORT", "3306")))
    parser.add_argument(f"--{option}-user", default=os.getenv(f"{env_prefix}_USER", "root"))
    parser.add_argument(f"--{option}-password", default="")
    parser.add_argument(f"--{option}-name", default=os.getenv(f"{env_prefix}_NAME", default_name))


def parse_args(argv: Optional[Sequence[str]] = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("phase", choices=("plan", "schema", "caller-authz", "proxy-backfill", "verify", "rollback"))
    parser.add_argument("--state", type=Path, required=True, help="shared JSON report and checkpoint")
    parser.add_argument("--force", action="store_true", help="rerun a phase already marked completed")
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--dry-run", action="store_true")
    mode.add_argument("--apply", action="store_true")
    parser.add_argument("--backend-url", default="")
    parser.add_argument("--safe-url", default="")
    parser.add_argument("--user-id", default="")
    parser.add_argument("--proxy-mode", choices=("off", "allowlist", "all"), default="off")
    parser.add_argument("--runtime-ready", action="store_true")
    parser.add_argument("--workloads-stopped", action="store_true")
    parser.add_argument("--database-backup", type=Path)
    parser.add_argument("--caller-export", type=Path)
    parser.add_argument("--pep-ready", action="append", choices=("vega", "execution-factory"), default=[])
    add_database_arguments(parser, "bkn_db", "openbkn")
    add_database_arguments(parser, "safe_db", "safe")
    args = parser.parse_args(argv)
    if args.phase in {"schema", "caller-authz"} and not (args.dry_run or args.apply):
        parser.error(f"{args.phase} requires --dry-run or --apply")
    if args.phase in {"plan", "proxy-backfill", "verify", "rollback"}:
        if not args.backend_url or not args.user_id:
            parser.error(f"{args.phase} requires --backend-url and --user-id")
    return args


def run(args: argparse.Namespace) -> int:
    state = load_state(args.state)
    stage_name = PHASE_STAGE[args.phase]
    if state["stages"].get(stage_name, {}).get("status") == "completed" and not args.force:
        print(json.dumps(state, ensure_ascii=False, indent=2, sort_keys=True))
        return 0
    try:
        if args.phase == "plan":
            plan_phase(args, state)
        elif args.phase == "schema":
            schema_phase(args, state)
        elif args.phase == "caller-authz":
            caller_phase(args, state)
        elif args.phase == "proxy-backfill":
            proxy_backfill_phase(args, state)
        elif args.phase == "verify":
            verify_phase(args, state)
        else:
            rollback_phase(args, state)
    except Exception as error:
        record_failure(state, args.phase, str(error))
        state["stages"][stage_name] = {
            **state["stages"].get(stage_name, {}),
            "status": "failed",
            "detail": str(error),
        }
        save_state(args.state, state)
        print(json.dumps({"status": "failed", "phase": args.phase, "error": str(error)}, indent=2), file=sys.stderr)
        return 1
    save_state(args.state, state)
    print(json.dumps(state, ensure_ascii=False, indent=2, sort_keys=True))
    return 0


def main(argv: Optional[Sequence[str]] = None) -> int:
    try:
        return run(parse_args(argv))
    except KeyboardInterrupt:
        return 130


if __name__ == "__main__":
    raise SystemExit(main())
