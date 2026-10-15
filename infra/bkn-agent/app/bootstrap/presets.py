# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Create-only installation and explicit adoption of versioned Agent defaults.

This module has no public HTTP route. Its caller is the trusted deployment/admin
command using database credentials. Existing /import keeps its upsert semantic.
"""

import hashlib
import json
import time
from collections.abc import Mapping
from pathlib import Path
from types import SimpleNamespace
from typing import Literal

from pydantic import BaseModel, Field, model_validator
from sqlalchemy import select
from sqlalchemy.exc import IntegrityError, OperationalError
from sqlalchemy.ext.asyncio import async_sessionmaker

from app import dao
from app.bootstrap.preset_models import (
    PresetInstallationRow,
    PresetVersionRow,
    ScenarioBindingRow,
)
from app.models import AgentRow, AgentSpec, PromptRow, PromptSpec, PromptVersionRow


class PresetError(ValueError):
    pass


class PresetPackage(BaseModel):
    model_config = {"extra": "forbid"}

    format: Literal["bkn-agent-preset/v1"]
    package_id: str = Field(min_length=1, max_length=100, pattern=r"^[a-z0-9_.-]+$")
    version: str = Field(min_length=1, max_length=50, pattern=r"^[0-9A-Za-z_.-]+$")
    scenario: str = Field(min_length=1, max_length=100, pattern=r"^[a-z0-9_]+$")
    required_features: list[str] = Field(default_factory=list)
    required_dependencies: list[str] = Field(default_factory=list)
    # None preserves the serialized payload/digest of existing v1 packages.
    bind_scenario: bool | None = None
    expected_owner: str | None = Field(default=None, min_length=1, max_length=50)
    installation_policy: Literal["enroll_existing"] | None = None
    spec: AgentSpec
    prompt: PromptSpec

    @model_validator(mode="after")
    def check_references(self):
        if not self.spec.agent_id or not self.prompt.prompt_id:
            raise ValueError("preset Agent and Prompt require fixed IDs")
        if self.spec.prompt_id != self.prompt.prompt_id:
            raise ValueError("preset Prompt reference must match the packaged Prompt")
        return self


def load_package(path: Path) -> PresetPackage:
    return PresetPackage.model_validate_json(path.read_text(encoding="utf-8"))


def digest(value) -> str:
    return hashlib.sha256(
        json.dumps(
            value, sort_keys=True, ensure_ascii=False, separators=(",", ":")
        ).encode()
    ).hexdigest()


def _now() -> int:
    return int(time.time() * 1000)


def dependency_state(package: PresetPackage, statuses: Mapping[str, str]) -> dict:
    """Report deployment-provided observations, never pretend to health-probe.

    Definition installation is independent of these observations and licensing.
    Unknown means unchecked, not ready. The caller may re-run after dependencies
    recover without changing any definition.
    """
    states = {
        name: statuses.get(name, "unknown") for name in package.required_dependencies
    }
    if any(value not in {"ready", "missing", "unknown"} for value in states.values()):
        raise PresetError("dependency status must be ready, missing or unknown")
    state = (
        "missing"
        if "missing" in states.values()
        else "unknown"
        if "unknown" in states.values()
        else "ready"
    )
    return {"state": state, "dependencies": states, "source": "deployment_report"}


async def _catalog(session, package: PresetPackage, actor: str) -> PresetVersionRow:
    payload = package.model_dump(exclude_none=True)
    content_digest = digest(payload)
    row = await session.get(PresetVersionRow, (package.package_id, package.version))
    if row:
        if row.f_digest != content_digest:
            raise PresetError("a published preset version cannot change content")
        return row
    row = PresetVersionRow(
        f_package_id=package.package_id,
        f_version=package.version,
        f_digest=content_digest,
        f_package=payload,
        f_create_user=actor,
        f_create_time=_now(),
    )
    session.add(row)
    await session.flush()
    return row


def _check_installation(installation, package: PresetPackage, owner: str):
    if (
        installation.f_package_id != package.package_id
        or installation.f_agent_id != package.spec.agent_id
        or installation.f_prompt_id != package.prompt.prompt_id
        or installation.f_owner_id != owner
    ):
        raise PresetError(
            "preset provenance or owner conflicts with the existing installation"
        )


def _check_owner(package, owner):
    _identity(owner)
    if package.expected_owner is not None and package.expected_owner != owner:
        raise PresetError("management owner does not match the preset's existing owner")


def _bound(package):
    return package.bind_scenario is not False


async def _current(session, installation) -> dict:
    agent = await session.get(AgentRow, installation.f_agent_id)
    prompt = await session.get(PromptRow, installation.f_prompt_id)
    version = (
        await session.get(
            PromptVersionRow, (prompt.f_prompt_id, prompt.f_current_version)
        )
        if prompt
        else None
    )
    binding = await session.get(ScenarioBindingRow, installation.f_scenario)
    spec = (
        dao._to_out(agent).model_dump(
            include=AgentSpec.model_fields.keys(), exclude_none=True
        )
        if agent
        else None
    )
    return {
        "spec": spec,
        "prompt": {
            "prompt_id": prompt.f_prompt_id,
            "name": prompt.f_name,
            "content": version.f_content,
            "vars_schema": version.f_vars_schema,
        }
        if prompt and version
        else None,
        # Include metadata too: edit then revert still invalidates an adoption preview.
        "agent_update": [agent.f_update_user, agent.f_update_time] if agent else None,
        "prompt_version": prompt.f_current_version if prompt else None,
        "prompt_update": [prompt.f_update_user, prompt.f_update_time]
        if prompt
        else None,
        "binding": {
            "agent_id": binding.f_agent_id,
            "enabled": binding.f_enabled,
            "revision": binding.f_revision,
        }
        if binding
        else None,
        "installation_revision": installation.f_revision,
    }


async def _initialize_once(sessions, package, owner, actor):
    async with sessions() as session, session.begin():
        # Lock an existing installation. A unique insert arbitrates first-install races.
        installation = (
            await session.execute(
                select(PresetInstallationRow)
                .where(PresetInstallationRow.f_scenario == package.scenario)
                .with_for_update()
            )
        ).scalar_one_or_none()
        if installation:
            _check_installation(installation, package, owner)
            await _catalog(session, package, actor)
            current = await _current(session, installation)
            expected = (
                ("spec", "prompt", "binding") if _bound(package) else ("spec", "prompt")
            )
            missing = [key for key in expected if current[key] is None]
            return {
                "action": "preserved",
                "missing_resources": missing,
                "applied_version": installation.f_applied_version or None,
                "candidate_version": package.version,
                "update_available": installation.f_applied_digest
                != digest(package.model_dump(exclude_none=True)),
            }

        # Without provenance even an identical resource must not be taken over.
        agent = await session.get(AgentRow, package.spec.agent_id)
        prompt = await session.get(PromptRow, package.prompt.prompt_id)
        if agent or prompt:
            raise PresetError(
                "fixed Agent or Prompt ID exists without preset provenance"
            )
        if package.installation_policy == "enroll_existing":
            raise PresetError(
                "legacy preset requires explicit enrollment; resources are not recreated"
            )
        conflict = await dao.check_import_conflict(
            session,
            package.spec.agent_id,
            package.spec.name,
            package.prompt.prompt_id,
            package.prompt.name,
            owner,
        )
        if conflict:
            raise PresetError(conflict)
        catalog = await _catalog(session, package, actor)
        await dao.upsert_prompt_with_id(
            session,
            package.prompt.prompt_id,
            package.prompt.name,
            package.prompt.content,
            package.prompt.vars_schema,
            owner,
            commit=False,
        )
        await dao.upsert_agent_with_id(
            session, package.spec.agent_id, package.spec, owner, commit=False
        )
        now = _now()
        session.add(
            PresetInstallationRow(
                f_scenario=package.scenario,
                f_package_id=package.package_id,
                f_agent_id=package.spec.agent_id,
                f_prompt_id=package.prompt.prompt_id,
                f_owner_id=owner,
                f_applied_version=package.version,
                f_applied_digest=catalog.f_digest,
                f_revision=1,
                f_update_user=actor,
                f_update_time=now,
            )
        )
        binding = await session.get(ScenarioBindingRow, package.scenario)
        if _bound(package) and binding is None:
            session.add(
                ScenarioBindingRow(
                    f_scenario=package.scenario,
                    f_agent_id=package.spec.agent_id,
                    f_enabled=1,
                    f_revision=1,
                    f_update_user=actor,
                    f_update_time=now,
                )
            )
        await session.flush()
        return {
            "action": "created",
            "applied_version": package.version,
            "update_available": False,
        }


def _retryable(error) -> bool:
    # Only contention is retryable. Missing schema, denied DB access and name
    # conflicts must be reported without hiding them behind generic retries.
    original = error.orig
    code = original.args[0] if original.args else None
    return (
        isinstance(error, IntegrityError)
        or code in {1205, 1213}
        or "database is locked" in str(original).lower()
    )


def _identity(value: str):
    if not value or not value.strip() or len(value) > 50:
        raise PresetError(
            "a non-empty owner/actor ID of at most 50 characters is required"
        )


async def initialize(
    sessions: async_sessionmaker,
    package: PresetPackage,
    *,
    owner: str,
    actor: str,
    installed_features: set[str],
    dependencies: Mapping[str, str] | None = None,
) -> dict:
    _check_owner(package, owner)
    _identity(actor)
    missing_features = sorted(set(package.required_features) - installed_features)
    if missing_features:
        return {
            "scenario": package.scenario,
            "action": "skipped",
            "missing_features": missing_features,
        }
    readiness = dependency_state(package, dependencies or {})
    for attempt in range(3):
        try:
            result = await _initialize_once(sessions, package, owner, actor)
            return {
                "scenario": package.scenario,
                **result,
                "readiness": readiness,
                "executable": False,  # User entry, scope and permission gates are follow-up work.
            }
        except (IntegrityError, OperationalError) as error:
            if not _retryable(error) or attempt == 2:
                raise PresetError(
                    "preset transaction failed; no partial installation was committed"
                ) from error
    raise AssertionError("unreachable")


async def preview(sessions, package_id: str, version: str, *, owner: str) -> dict:
    _identity(owner)
    async with sessions() as session:
        candidate = await session.get(PresetVersionRow, (package_id, version))
        installation = (
            await session.execute(
                select(PresetInstallationRow).where(
                    PresetInstallationRow.f_package_id == package_id
                )
            )
        ).scalar_one_or_none()
        if not candidate or not installation:
            raise PresetError("preset candidate or installation not found")
        package = PresetPackage.model_validate(candidate.f_package)
        _check_installation(installation, package, owner)
        current = await _current(session, installation)
        return {
            "package_id": package_id,
            "version": version,
            "applied_version": installation.f_applied_version or None,
            "expected_digest": digest(current),
            "current": current,
            "candidate": {
                "spec": package.spec.model_dump(exclude_none=True),
                "prompt": package.prompt.model_dump(exclude_none=True),
            },
        }


async def _legacy_state(session, package, owner, *, lock=False):
    _check_owner(package, owner)
    agent = await session.get(AgentRow, package.spec.agent_id, with_for_update=lock)
    prompt = await session.get(
        PromptRow, package.prompt.prompt_id, with_for_update=lock
    )
    if agent is None or prompt is None:
        raise PresetError(
            "legacy resources missing; enrollment does not create or restore them"
        )
    if agent.f_create_user != owner or agent.f_prompt_id != prompt.f_prompt_id:
        raise PresetError("legacy owner or Prompt reference does not match")
    first_version = (
        (
            await session.execute(
                select(PromptVersionRow)
                .where(PromptVersionRow.f_prompt_id == prompt.f_prompt_id)
                .order_by(PromptVersionRow.f_version)
            )
        )
        .scalars()
        .first()
    )
    if first_version is None or first_version.f_create_user != owner:
        raise PresetError("legacy Prompt provenance cannot be confirmed")
    current = await _current(
        session,
        SimpleNamespace(
            f_agent_id=agent.f_agent_id,
            f_prompt_id=prompt.f_prompt_id,
            f_scenario=package.scenario,
            f_revision=0,
        ),
    )
    if current["prompt"] is None:
        raise PresetError("legacy current Prompt version missing")
    current["owner"] = agent.f_create_user
    exact = current["spec"] == package.spec.model_dump(
        exclude_none=True
    ) and PromptSpec.model_validate(current["prompt"]).model_dump(
        exclude_none=True
    ) == package.prompt.model_dump(exclude_none=True)
    return current, exact


async def inspect_legacy(sessions, package, *, owner):
    """Read-only comparison before explicitly accepting legacy provenance."""
    async with sessions() as session:
        current, exact = await _legacy_state(session, package, owner)
        return {
            "scenario": package.scenario,
            "baseline": "matched" if exact else "legacy_modified",
            "expected_digest": digest(current),
            "current": current,
            "candidate": package.model_dump(exclude_none=True),
        }


async def _enroll_once(sessions, package, *, owner, actor, expected_digest):
    """Attach provenance without writing Agent, Prompt, history or bindings.

    Blank applied version/digest means an unrecognized legacy baseline, not a
    fictitious official version. Existing NOT NULL columns already support this,
    so enrollment does not add or alter database columns.
    """
    _identity(actor)
    async with sessions() as session, session.begin():
        installation = (
            await session.execute(
                select(PresetInstallationRow)
                .where(PresetInstallationRow.f_scenario == package.scenario)
                .with_for_update()
            )
        ).scalar_one_or_none()
        current, exact = await _legacy_state(session, package, owner, lock=True)
        if digest(current) != expected_digest:
            raise PresetError(
                "configuration changed after preview; refresh the comparison"
            )
        if installation is not None:
            _check_installation(installation, package, owner)
            return {
                "action": "preserved",
                "scenario": package.scenario,
                "applied_version": installation.f_applied_version or None,
            }
        catalog = await _catalog(session, package, actor)
        session.add(
            PresetInstallationRow(
                f_scenario=package.scenario,
                f_package_id=package.package_id,
                f_agent_id=package.spec.agent_id,
                f_prompt_id=package.prompt.prompt_id,
                f_owner_id=owner,
                f_applied_version=package.version if exact else "",
                f_applied_digest=catalog.f_digest if exact else "",
                f_revision=1,
                f_update_user=actor,
                f_update_time=_now(),
            )
        )
        await session.flush()
        return {
            "action": "enrolled",
            "scenario": package.scenario,
            "baseline": "matched" if exact else "legacy_modified",
            "applied_version": package.version if exact else None,
        }


async def enroll(sessions, package, *, owner, actor, expected_digest):
    for attempt in range(3):
        try:
            return await _enroll_once(
                sessions,
                package,
                owner=owner,
                actor=actor,
                expected_digest=expected_digest,
            )
        except (IntegrityError, OperationalError) as error:
            if not _retryable(error) or attempt == 2:
                raise PresetError(
                    "enrollment transaction failed; no partial registration was committed"
                ) from error
    raise AssertionError("unreachable")


async def adopt(
    sessions,
    package_id: str,
    version: str,
    *,
    owner: str,
    actor: str,
    expected_digest: str,
    installed_features: set[str],
) -> dict:
    """Explicit admin operation, protected by row locks and a preview fingerprint.

    Ordinary initialization never calls this. It preserves binding, enablement
    and personal Prompt overrides, and refuses to resurrect deleted resources.
    """
    _identity(owner)
    _identity(actor)
    async with sessions() as session, session.begin():
        installation = (
            await session.execute(
                select(PresetInstallationRow)
                .where(PresetInstallationRow.f_package_id == package_id)
                .with_for_update()
            )
        ).scalar_one_or_none()
        candidate = await session.get(PresetVersionRow, (package_id, version))
        if not installation or not candidate:
            raise PresetError("preset candidate or installation not found")
        package = PresetPackage.model_validate(candidate.f_package)
        _check_installation(installation, package, owner)
        if set(package.required_features) - installed_features:
            raise PresetError("required preset feature is not installed")
        # Existing API writers update these rows; lock them before comparing the
        # preview so a concurrent PUT cannot be overwritten between check and write.
        for row_type, key in (
            (AgentRow, installation.f_agent_id),
            (PromptRow, installation.f_prompt_id),
            (ScenarioBindingRow, installation.f_scenario),
        ):
            await session.get(row_type, key, with_for_update=True)
        current = await _current(session, installation)
        if not current["spec"] or not current["prompt"]:
            raise PresetError(
                "removed preset resources require explicit recovery, not adoption"
            )
        if digest(current) != expected_digest:
            raise PresetError(
                "configuration changed after preview; refresh the comparison"
            )
        agent = await session.get(AgentRow, installation.f_agent_id)
        if agent.f_create_user != owner:
            raise PresetError("editable preset resource ownership changed")
        conflict = await dao.check_import_conflict(
            session,
            package.spec.agent_id,
            package.spec.name,
            package.prompt.prompt_id,
            package.prompt.name,
            owner,
        )
        if conflict:
            raise PresetError(conflict)
        # Adoption is not an enable/re-publish operation.
        spec = package.spec.model_copy(update={"status": agent.f_status})
        await dao.upsert_prompt_with_id(
            session,
            package.prompt.prompt_id,
            package.prompt.name,
            package.prompt.content,
            package.prompt.vars_schema,
            owner,
            commit=False,
        )
        prompt = await session.get(PromptRow, installation.f_prompt_id)
        prompt.f_name = package.prompt.name
        await dao.upsert_agent_with_id(
            session, package.spec.agent_id, spec, owner, commit=False
        )
        installation.f_applied_version = version
        installation.f_applied_digest = candidate.f_digest
        installation.f_revision += 1
        installation.f_update_user = actor
        installation.f_update_time = _now()
        return {
            "action": "adopted",
            "scenario": installation.f_scenario,
            "version": version,
        }
