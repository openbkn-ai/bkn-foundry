from app.routers import llm_router, small_model_router, private_route
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from fastapi import Request

from app.logs.stand_log import StandLogger
from app.utils import openai_error

api_version_public_v1 = "/api/mf-model-api/v1"
api_version_private_v1 = "/api/private/mf-model-api/v1"
api_version_health = "/api/v1"

# 声明为 OpenAI 兼容的路由。这些路径上的失败必须是 {"error": {...}}，其余端点
# （小模型、模型管理等）不是兼容面，继续用模型工厂自家的 envelope——别一刀切，
# 那会连带破掉它们的对外契约（#637）。
_OPENAI_COMPAT_SUFFIXES = ("/chat/completions",)


def _is_openai_compat(path):
    return any(path.endswith(suffix) for suffix in _OPENAI_COMPAT_SUFFIXES)


def router_init(app):
    app.include_router(
        llm_router.health_route,
        prefix=api_version_health,
        tags=["Factory"],
        responses={404: {"description": "Not found"}},
    )
    app.include_router(
        llm_router.llm_route,
        prefix=api_version_public_v1,
        tags=["Factory"],
        responses={404: {"description": "Not found"}},
    )

    app.include_router(
        small_model_router.small_model_router,
        prefix=api_version_public_v1,
        tags=["Factory"],
        responses={404: {"description": "Not found"}},
    )
    app.include_router(
        private_route.private_route,
        prefix=api_version_private_v1,
        tags=["Factory"],
        responses={404: {"description": "Not found"}},
    )

    @app.exception_handler(RequestValidationError)
    async def exception_handler(request: Request, exc: RequestValidationError):
        # 只投影 loc/type/msg：pydantic v1 的 errors() 不回显入参，但 v2 会带
        # input，届时整份 errors 落日志就等于把用户请求体写进去了（见 #636）。
        StandLogger.warn("request validation failed: %s" % [
            {k: e.get(k) for k in ("loc", "type", "msg")} for e in exc.errors()])
        # 只报第一条：detail 是字符串不是列表，聚合会改变既有响应契约。
        for error in exc.errors():
            paramName = ' '.join(map(str, error["loc"][1:]))
            if error["type"] == "value_error.missing":
                content = {"code": "ModelFactory.Router.ParamError.ParamMissing",
                           "description": "参数缺失",
                           "detail": "{0} 参数缺失".format(paramName),
                           "solution": "请检查填写的参数是否正确。",
                           "link": ""}
            else:
                content = {"code": "ModelFactory.Router.ParamError.FormatError",
                           "description": "参数错误",
                           "detail": f"{error.get('msg', '')}",
                           "solution": "请检查输入内容格式是否符合要求",
                           "link": ""}
            # 兼容面上换成 OpenAI 错误体：同一端点不能有两种错误契约，否则对接方
            # 得写两套解析。原业务码挪进 error.code，机器可读的身份不丢。
            if _is_openai_compat(request.url.path):
                return JSONResponse(
                    status_code=400,
                    content=openai_error.from_envelope(content, 400))
            return JSONResponse(status_code=400, content=content)

    app.add_exception_handler(RequestValidationError, exception_handler)
