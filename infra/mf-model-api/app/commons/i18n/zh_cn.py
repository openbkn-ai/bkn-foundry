# 简体中文
from app.commons.errors.codes import ParamValidationErrors

error_messages = {
    "ModelFactory.Stream.InvalidMessageRole": {
        "description": "对话消息不合法",
        "detail": "最后一条对话消息的角色必须为 user。",
        "solution": "请将最后一条消息的角色设置为 user 后重试。",
    },
    "ModelFactory.Stream.ModelConnectionFailed": {
        "description": "模型服务连接失败",
        "detail": "无法连接到模型服务。",
        "solution": "请检查模型服务配置和可用性。",
    },
    "ModelFactory.Stream.InternalError": {
        "description": "模型服务内部错误",
        "detail": "模型服务无法完成该请求。",
        "solution": "请稍后重试或联系管理员。",
    },
    "ModelFactory.Router.ParamError.ParamMissing": {
        "description": "参数缺失",
        "detail": "",
        "detail_template": "缺少必填参数：{parameter}",
        "solution": "请阅读 API 文档并提供必填参数。",
    },
    "ModelFactory.Router.ParamError.FormatError": {
        "description": "请求参数错误",
        "detail": "输入参数格式不符合要求。",
        "solution": "请检查输入格式是否符合 API 文档要求。",
    },
    ParamValidationErrors.ParamMissing: {
        "code": ParamValidationErrors.ParamMissing,
        "description": "参数缺失",
        "detail": "",
        "detail_template": "缺少必填参数：{parameter}",
        "solution": "请阅读API文档填写正确的参数",
        "link": ""
    },
    ParamValidationErrors.ParamTypeError: {
        "code": ParamValidationErrors.ParamTypeError,
        "description": "参数类型错误",
        "solution": "请阅读API文档填写正确的参数",
        "detail": "输入参数格式不符合要求。",
        "detail_template": "参数类型错误：{parameter}",
        "link": ""

    }
}
