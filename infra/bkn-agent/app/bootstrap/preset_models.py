# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Installation provenance is separate from editable Agent/Prompt rows.

Deleting an editable resource deliberately leaves its installation record behind,
so a subsequent deployment cannot silently recreate it.
"""

from sqlalchemy import BigInteger, Integer, String
from sqlalchemy.dialects.mysql import JSON
from sqlalchemy.orm import Mapped, mapped_column

from app.models import Base


class PresetVersionRow(Base):
    __tablename__ = "t_agent_preset_version"

    f_package_id: Mapped[str] = mapped_column(String(100), primary_key=True)
    f_version: Mapped[str] = mapped_column(String(50), primary_key=True)
    f_digest: Mapped[str] = mapped_column(String(64))
    f_package: Mapped[dict] = mapped_column(JSON)
    f_create_user: Mapped[str] = mapped_column(String(50))
    f_create_time: Mapped[int] = mapped_column(BigInteger)


class PresetInstallationRow(Base):
    __tablename__ = "t_agent_preset_installation"

    f_scenario: Mapped[str] = mapped_column(String(100), primary_key=True)
    f_package_id: Mapped[str] = mapped_column(String(100), unique=True)
    f_agent_id: Mapped[str] = mapped_column(String(50), unique=True)
    f_prompt_id: Mapped[str] = mapped_column(String(50), unique=True)
    f_owner_id: Mapped[str] = mapped_column(String(50))
    f_applied_version: Mapped[str] = mapped_column(String(50))
    f_applied_digest: Mapped[str] = mapped_column(String(64))
    f_revision: Mapped[int] = mapped_column(Integer, default=1)
    f_update_user: Mapped[str] = mapped_column(String(50))
    f_update_time: Mapped[int] = mapped_column(BigInteger)


class ScenarioBindingRow(Base):
    __tablename__ = "t_agent_scenario_binding"

    # First delivery supports platform defaults; network overrides are separate work.
    f_scenario: Mapped[str] = mapped_column(String(100), primary_key=True)
    f_agent_id: Mapped[str] = mapped_column(String(50))
    f_enabled: Mapped[int] = mapped_column(Integer, default=1)
    f_revision: Mapped[int] = mapped_column(Integer, default=1)
    f_update_user: Mapped[str] = mapped_column(String(50))
    f_update_time: Mapped[int] = mapped_column(BigInteger)
