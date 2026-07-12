from contextlib import asynccontextmanager
from pathlib import Path

from fastapi import FastAPI, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse

from app.bootstrap import toolbox_sync
from app.models import ErrorEnvelope
from app.observability import setup_otel
from app.routers import agents, chat, prompts, tasks, threads

API_PREFIX = "/api/bkn-agent/v1"
VERSION = (Path(__file__).resolve().parent.parent / "VERSION").read_text().strip()

# 契约冻结在 docs/api/bkn-agent.yaml（#212）；改 API 先改 spec，再跑
# scripts/export_openapi.py 重新导出——test_contract.py 强制两者一致。
_ERRORS = {"4XX": {"model": ErrorEnvelope, "description": "平台错误封套（400 参数/401 身份/404 不存在/409 冲突）"}}


@asynccontextmanager
async def _lifespan(app: FastAPI):
    toolbox_sync.start_startup_sync()
    yield


app = FastAPI(title="bkn-agent", version=VERSION, docs_url=None, redoc_url=None, lifespan=_lifespan)
setup_otel(app)
app.include_router(agents.router, prefix=API_PREFIX, tags=["BknAgent"], responses=_ERRORS)
app.include_router(chat.router, prefix=API_PREFIX, tags=["BknAgent"], responses=_ERRORS)
app.include_router(tasks.router, prefix=API_PREFIX, tags=["BknAgent"], responses=_ERRORS)
app.include_router(prompts.router, prefix=API_PREFIX, tags=["BknAgent"], responses=_ERRORS)
app.include_router(threads.router, prefix=API_PREFIX, tags=["BknAgent"], responses=_ERRORS)


@app.get("/api/v1/health")
async def health():
    return {"status": "ok"}


@app.exception_handler(RequestValidationError)
async def validation_handler(request: Request, exc: RequestValidationError):
    detail = "; ".join(
        f"{'.'.join(str(p) for p in e['loc'][1:])}: {e['msg']}" for e in exc.errors()
    )
    return JSONResponse(
        status_code=400,
        content={
            "code": "BknAgent.ParamError.FormatError",
            "description": "参数错误",
            "detail": detail,
            "solution": "请检查请求体格式。",
            "link": "",
        },
    )
