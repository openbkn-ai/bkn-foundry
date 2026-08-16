# Simplified Chinese locale resources.
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
        "max_tokens_detail_template": "max_tokens 超过最大值 {limit}。",
        "solution": "请检查输入格式是否符合 API 文档要求。",
    },
    "ModelFactory.Router.ParamError.TypeError": {
        "description": "参数类型错误",
        "detail": "输入参数类型不符合要求。",
        "detail_template": "参数类型错误：{parameter}",
        "solution": "请检查参数类型是否符合 API 文档要求。",
    },
    "ModelFactory.InternalError": {
        "description": "请求失败",
        "detail": "请求无法完成。",
        "solution": "请稍后重试或联系管理员。",
    },
    "ModelFactory.HTTP.NotFound": {
        "description": "资源不存在",
        "detail": "请求的资源不存在。",
        "solution": "请检查资源标识后重试。",
    },
    "ModelFactory.HTTP.MethodNotAllowed": {
        "description": "请求方法不被允许",
        "detail": "当前资源不支持该请求方法。",
        "solution": "请检查请求方法后重试。",
    },
    "Unauthorized": {
        "description": "身份认证失败",
        "detail": "访问令牌无效或已过期。",
        "solution": "请获取有效的访问令牌后重试。",
    },
    "HydraServiceError": {
        "description": "身份认证服务不可用",
        "detail": "无法完成访问令牌校验。",
        "solution": "请稍后重试或联系管理员。",
    },
    "BknSafeServiceError": {
        "description": "应用密钥服务不可用",
        "detail": "无法完成应用密钥校验。",
        "solution": "请稍后重试或联系管理员。",
    },
    "NotPermission": {
        "description": "没有操作权限",
        "detail": "当前身份没有执行此操作的权限。",
        "solution": "请联系管理员分配相应权限。",
    },
    "ModelFactory.Mydb.DataBase.ParameterError": {
        "description": "数据库访问失败",
        "detail": "模型工厂暂时无法访问数据库。",
        "solution": "请稍后重试或联系管理员。",
    },
    "ModelFactory.MyPymysqlPool.Connection.ConnectError": {
        "description": "数据库连接失败",
        "detail": "模型工厂暂时无法连接数据库。",
        "solution": "请稍后重试或联系管理员。",
    },
    "ModelFactory.ExternalSmallModel.Used.NameNotExist": {
        "description": "模型不存在",
        "detail": "指定的模型名称或模型 ID 不存在。",
        "solution": "请检查模型名称或模型 ID 后重试。",
    },
    "ModelFactory.ExternalSmallModel.Used.DefaultNotExist": {
        "description": "默认小模型未配置",
        "detail": "管理员尚未配置该类型的默认小模型。",
        "solution": "请在模型管理中配置默认小模型后重试。",
    },
    "ModelFactory.LLM.DefaultNotExist": {
        "description": "默认大模型未配置",
        "detail": "管理员尚未配置默认大模型。",
        "solution": "请联系管理员配置默认大模型。",
    },
    "ModelFactory.ExternalSmallModel.Used.ConnectError": {
        "description": "模型服务连接失败",
        "detail": "无法连接到指定的小模型服务。",
        "solution": "请检查模型配置和服务可用性。",
    },
    "ModelFactory.ModelController.Model.ConnectError": {
        "description": "模型服务连接失败",
        "detail": "无法连接到指定的大模型服务。",
        "solution": "请检查模型配置和服务可用性。",
    },
    "ModelFactory.ModelController.Model.Error": {
        "description": "模型调用失败",
        "detail": "模型服务无法完成该请求。",
        "solution": "请检查模型配置后重试。",
    },
    "ModelFactory.ConnectController.LLMUsed.ParameterError": {
        "description": "模型配置错误",
        "detail": "当前模型配置无法用于完成请求。",
        "solution": "请检查大模型配置。",
    },
    "ModelFactory.LLM.Error": {
        "description": "模型上下文超出限制",
        "detail": "请求内容和最大输出令牌数超出模型上下文限制。",
        "solution": "请缩短请求内容、减少最大输出令牌数或选择上下文更大的模型。",
    },
    "ModelFactory.LLM.ModelTimeoutError": {
        "description": "模型请求超时",
        "detail": "模型服务未在规定时间内完成请求。",
        "solution": "请稍后重试。",
    },
    "ModelNager.ModelQuotaController.UserModelConfig.NoLeftSpace": {
        "description": "模型额度不足",
        "detail": "本月模型使用额度已达上限。",
        "solution": "请联系管理员增加额度后重试。",
    },
    "ModelFactory.SmallModelController.ModelApiDoc.ModelNotFoundError": {
        "description": "模型不存在",
        "detail": "指定的模型 ID 不存在。",
        "solution": "请检查模型 ID 后重试。",
    },
    "ModelFactory.ModelController.TestModel.Error": {
        "description": "模型连接测试失败",
        "detail": "无法使用当前配置连接模型服务。",
        "solution": "请检查模型配置和服务可用性。",
    },
    "ModelFactory.ConnectController.LLMTest.ParameterError": {
        "description": "模型连接测试失败",
        "detail": "无法使用当前配置连接大模型服务。",
        "solution": "请检查大模型配置和服务可用性。",
    },
    "ModelFactory.ExternalSmallModel.UnknownError": {
        "description": "小模型请求失败",
        "detail": "小模型请求无法完成。",
        "solution": "请稍后重试或联系管理员。",
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
