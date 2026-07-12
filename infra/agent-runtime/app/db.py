from sqlalchemy.ext.asyncio import async_sessionmaker, create_async_engine

from app.config import config

engine = create_async_engine(config.db_url, pool_pre_ping=True, pool_recycle=3600)
SessionLocal = async_sessionmaker(engine, expire_on_commit=False)


async def get_session():
    async with SessionLocal() as session:
        yield session
