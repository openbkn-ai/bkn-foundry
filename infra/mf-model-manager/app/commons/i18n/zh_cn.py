# 简体中文
from app.commons.errors.codes import ParamValidationErrors

error_messages = {
    ParamValidationErrors.ParamMissing: {
        "code": ParamValidationErrors.ParamMissing,
        "description": "参数缺失",
        "detail": "",
        "detail_template": "缺少必填参数：{parameters}",
        "solution": "请阅读API文档填写正确的参数",
        "link": ""
    },
    ParamValidationErrors.ParamTypeError: {
        "code": ParamValidationErrors.ParamTypeError,
        "description": "参数类型错误",
        "solution": "请阅读API文档填写正确的参数",
        "detail": "",
        "detail_template": "参数类型错误：{parameters}",
        "link": ""

    },
    "ModelFactory.Router.ParamError.ParamMissing": {
        "code": "ModelFactory.Router.ParamError.ParamMissing",
        "description": "参数缺失",
        "detail": "",
        "detail_template": "缺少必填参数：{parameters}",
        "solution": "请阅读API文档填写正确的参数",
        "link": ""
    },
    "ModelFactory.OperationAudit.AccessDenied": {
        "code": "ModelFactory.OperationAudit.AccessDenied",
        "description": "访问被拒绝。",
        "detail": "当前身份没有访问操作审计记录的权限。",
        "solution": "请联系管理员申请权限。",
        "link": ""
    },
    "ModelFactory.OperationAudit.InvalidTimestamp": {
        "code": "ModelFactory.OperationAudit.InvalidTimestamp",
        "description": "时间格式无效。",
        "detail": "from 或 to 必须使用 RFC3339 时间格式。",
        "solution": "请使用 RFC3339 时间格式后重试。",
        "link": ""
    },
    "ModelFactory.OperationAudit.InvalidTimeRange": {
        "code": "ModelFactory.OperationAudit.InvalidTimeRange",
        "description": "时间范围无效。",
        "detail": "结束时间必须晚于开始时间，且查询范围不能超过 30 天。",
        "solution": "请调整时间范围后重试。",
        "link": ""
    },
    "ModelFactory.OperationAudit.EventNotFound": {
        "code": "ModelFactory.OperationAudit.EventNotFound",
        "description": "审计事件不存在。",
        "detail": "未找到请求的操作审计事件。",
        "solution": "请检查事件标识后重试。",
        "link": ""
    }
}
