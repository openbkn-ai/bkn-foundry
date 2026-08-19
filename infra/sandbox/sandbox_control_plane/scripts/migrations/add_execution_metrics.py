"""
Alembic migration script template

Add return_value and metrics fields to the execution table

Usage:
1. Ensure Alembic is initialized：`alembic init alembic`
2. Copy this file to the Alembic versions directory
3. Rename the file according to the generated revision number
4. Run the migration:`alembic upgrade head`
"""
from alembic import op
import sqlalchemy as sa

# revision identifiers, used by Alembic
revision = '002_add_execution_metrics'
down_revision = '001_initial_schema'
branch_labels = None
depends_on = None


def upgrade():
    """Add return_value and metrics fields."""
    op.add_column(
        'executions',
        sa.Column('return_value', sa.JSON(), nullable=True)
    )
    op.add_column(
        'executions',
        sa.Column('metrics', sa.JSON(), nullable=True)
    )


def downgrade():
    """Remove return_value and metrics fields"""
    op.drop_column('executions', 'metrics')
    op.drop_column('executions', 'return_value')
