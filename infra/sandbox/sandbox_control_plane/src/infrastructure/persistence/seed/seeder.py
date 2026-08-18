"""
Database seed data

The shared seeding logic, callable at application start-up or from a standalone script.
"""

from sqlalchemy import select
from structlog import get_logger

from src.infrastructure.persistence.database import db_manager
from src.infrastructure.persistence.models.runtime_node_model import RuntimeNodeModel
from src.infrastructure.persistence.models.template_model import TemplateModel
from src.infrastructure.persistence.seed.default_data import (
    get_default_runtime_nodes,
    get_default_templates,
)

logger = get_logger(__name__)


async def seed_runtime_nodes(force: bool = False) -> int:
    """
    Create the default runtime nodes

    Args:
        force: when True, recreate the nodes even if they exist

    Returns:
        How many nodes were created
    """
    async with db_manager.get_session() as session:
        # Check whether nodes already exist
        result = await session.execute(select(RuntimeNodeModel))
        existing_nodes = result.scalars().all()

        if existing_nodes and not force:
            logger.info(
                "Runtime nodes already exist, skipping initialization", count=len(existing_nodes)
            )
            return 0

        # With force=True, delete the existing nodes
        if existing_nodes and force:
            for node in existing_nodes:
                await session.delete(node)
            await session.flush()
            logger.info("Deleted existing runtime nodes", count=len(existing_nodes))

        # Create the default nodes
        default_nodes = get_default_runtime_nodes()
        for node in default_nodes:
            session.add(node)

        await session.flush()
        logger.info("Created default runtime nodes", count=len(default_nodes))
        return len(default_nodes)


async def seed_templates(force: bool = False) -> int:
    """
    Create the default templates

    Args:
        force: when True, recreate the templates even if they exist

    Returns:
        How many templates were created or updated
    """
    async with db_manager.get_session() as session:
        # Read the default template definitions
        default_templates = get_default_templates()
        default_template_map = {t.f_id: t for t in default_templates}

        # Check which templates already exist
        result = await session.execute(select(TemplateModel))
        existing_templates = result.scalars().all()
        existing_template_map = {t.f_id: t for t in existing_templates}

        if existing_templates and not force:
            # Update the image of an existing template
            updated_count = 0
            for template_id, default_template in default_template_map.items():
                if template_id in existing_template_map:
                    existing_template = existing_template_map[template_id]
                    # Update the image and the other fields that can change
                    if existing_template.f_image_url != default_template.f_image_url:
                        old_image_url = existing_template.f_image_url
                        existing_template.f_image_url = default_template.f_image_url
                        updated_count += 1
                        logger.info(
                            "Updated template image URL",
                            template_id=template_id,
                            old_image=old_image_url,
                            new_image=default_template.f_image_url,
                        )

            # Create the templates that are in the defaults but not in the database
            created_count = 0
            for template_id, default_template in default_template_map.items():
                if template_id not in existing_template_map:
                    session.add(default_template)
                    created_count += 1

            await session.flush()
            logger.info("Templates synced", updated=updated_count, created=created_count)
            return updated_count + created_count

        # With force=True, delete the existing templates and recreate them
        if existing_templates and force:
            for template in existing_templates:
                await session.delete(template)
            await session.flush()
            logger.info("Deleted existing templates", count=len(existing_templates))

        # Create the default templates
        for template in default_templates:
            logger.info(
                "Creating template with image URL",
                template_id=template.f_id,
                f_image_url=template.f_image_url,
            )
            session.add(template)

        await session.flush()
        logger.info("Created default templates", count=len(default_templates))
        return len(default_templates)


async def seed_default_data(force: bool = False) -> dict:
    """
    Create all the default data

    Args:
        force: when True, recreate the data even if it exists

    Returns:
        A dict holding how many of each were created
    """
    logger.info("Starting default data seeding", force=force)

    node_count = await seed_runtime_nodes(force=force)
    template_count = await seed_templates(force=force)

    result = {
        "runtime_nodes": node_count,
        "templates": template_count,
        "total": node_count + template_count,
    }

    logger.info("Completed default data seeding", **result)
    return result
