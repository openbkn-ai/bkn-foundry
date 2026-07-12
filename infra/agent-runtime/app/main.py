from fastapi import FastAPI, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse

from app.observability import setup_otel
from app.routers import agents, chat, prompts, tasks

API_PREFIX = "/api/agent-runtime/v1"

app = FastAPI(title="agent-runtime", docs_url=None, redoc_url=None)
setup_otel(app)
app.include_router(agents.router, prefix=API_PREFIX, tags=["AgentRuntime"])
app.include_router(chat.router, prefix=API_PREFIX, tags=["AgentRuntime"])
app.include_router(tasks.router, prefix=API_PREFIX, tags=["AgentRuntime"])
app.include_router(prompts.router, prefix=API_PREFIX, tags=["AgentRuntime"])


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
            "code": "AgentRuntime.ParamError.FormatError",
            "description": "参数错误",
            "detail": detail,
            "solution": "请检查请求体格式。",
            "link": "",
        },
    )
