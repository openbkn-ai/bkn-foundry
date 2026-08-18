# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

# Simplified Chinese error catalog.
#
# Keyed by message key, not by public error code: several call sites share one
# machine-readable code while saying different things (ToolRef, Conflict, Mode,
# PromptVars, Toolbox.Upstream, Skill.FetchFailed). The public code lives in the
# entry so the code stays stable while the text varies.
#
# Only *_template fields are formatted with call-site parameters. Plain
# description and solution text is emitted verbatim, so literal braces in it
# stay literal.

error_messages = {
    "BknAgent.Auth.AccountRequired": {
        "code": "BknAgent.Auth.AccountRequired",
        "description": "缺少调用方身份",
        "detail": "x-account-id / x-account-type 请求头缺失或非法（anonymous 不被接受）",
        "solution": "bkn-agent 仅面向平台内部：平台模块以服务身份调用，内部工程师经网关携带 token / bak_ AppKey。",
    },
    "BknAgent.NotFound": {
        "code": "BknAgent.NotFound",
        "description_template": "{resource}不存在",
        "description": "资源不存在",
        "detail_template": "{resource} {ident} 不存在",
        "detail": "资源不存在",
        "solution": "请检查 id 是否正确。",
    },
    "BknAgent.Thread.Busy": {
        "code": "BknAgent.Thread.Busy",
        "description": "会话正在处理中",
        "detail_template": "thread {thread_id} 有未完成的 /chat 请求",
        "detail": "该 thread 有未完成的 /chat 请求",
        "solution": "等待当前轮结束后重试。",
    },
    "BknAgent.Thread.AgentMismatch": {
        "code": "BknAgent.Thread.AgentMismatch",
        "description": "thread 归属其他 agent",
        "detail_template": "thread {thread_id} 建立于 agent {owner_agent_id}",
        "detail": "该 thread 由其他 agent 建立",
        "solution": "同一 thread 只能续接创建它的 agent；换 agent 请开新 thread。",
    },
    "BknAgent.Prompt.VarsMissing": {
        "code": "BknAgent.ParamError.PromptVars",
        "description": "提示词变量缺失",
        "detail_template": "缺少变量: {variables}",
        "detail": "提示词变量缺失",
        "solution": "按 prompt_vars_schema 补齐 prompt_vars。",
    },
    "BknAgent.Prompt.VarsUndeclared": {
        "code": "BknAgent.ParamError.PromptVars",
        "description": "提示词变量缺失",
        "detail_template": "模板引用了未提供的变量 {variable}",
        "detail": "模板引用了未提供的变量",
        "solution": "",
    },
    "BknAgent.Prompt.TemplateSyntax": {
        "code": "BknAgent.ParamError.PromptTemplate",
        "description": "提示词模板语法错误",
        "detail_template": "{error}",
        "detail": "提示词模板语法错误",
        "solution": "带变量的提示词里，字面大括号需要双写转义。",
    },
    "BknAgent.Prompt.Unbound": {
        "code": "BknAgent.Prompt.Unbound",
        "description": "agent 未绑定提示词",
        "detail_template": "agent {agent_id} 无 prompt_id 且本次调用未提供覆写",
        "detail": "该 agent 无 prompt_id 且本次调用未提供覆写",
        "solution": "为 agent 绑定 prompt_id，或在请求中携带 prompt_override。",
    },
    "BknAgent.Prompt.Missing": {
        "code": "BknAgent.Prompt.Missing",
        "description": "提示词不存在",
        "detail_template": "prompt {prompt_id} 或其当前版本不存在",
        "detail": "该提示词或其当前版本不存在",
        "solution": "检查提示词是否被删除；不会回退到内置默认词。",
    },
    "BknAgent.Prompt.VersionMissing": {
        "code": "BknAgent.ParamError.Version",
        "description": "目标版本不存在",
        "detail_template": "prompt {prompt_id} 无版本 {version}",
        "detail": "目标版本不存在",
        "solution": "",
    },
    "BknAgent.Prompt.Conflict": {
        "code": "BknAgent.ParamError.Conflict",
        "description": "提示词名称或 id 已存在",
        "detail_template": "name={name} id={prompt_id}",
        "detail": "提示词名称或 id 已存在",
        "solution": "换一个 name，或换/去掉预设 prompt_id。",
    },
    "BknAgent.Task.DepthExceeded": {
        "code": "BknAgent.Task.DepthExceeded",
        "description": "agent 互调层级超限",
        "detail_template": "执行栈深度超过 {limit}（疑似循环互调）",
        "detail": "执行栈深度超限（疑似循环互调）",
        "solution": "检查 agent-as-tool 引用链是否成环。",
    },
    "BknAgent.Skill.NotFound": {
        "code": "BknAgent.Skill.NotFound",
        "description": "技能不存在",
        "detail_template": "技能 {skill_id} 在执行工厂中不存在或未发布",
        "detail": "该技能在执行工厂中不存在或未发布",
        "solution": "请检查 skill_id，技能失效时不会被静默跳过。",
    },
    "BknAgent.Skill.FetchFailed": {
        "code": "BknAgent.Skill.FetchFailed",
        "description": "技能拉取失败",
        "detail_template": "执行工厂返回 {status}（技能 {skill_id}）",
        "detail": "执行工厂拉取技能失败",
        "solution": "",
    },
    "BknAgent.Skill.ContentFetchFailed": {
        "code": "BknAgent.Skill.FetchFailed",
        "description": "技能正文拉取失败",
        "detail_template": "对象存储返回 {status}（技能 {skill_id}）",
        "detail": "对象存储拉取技能正文失败",
        "solution": "",
    },
    "BknAgent.Toolbox.Upstream": {
        "code": "BknAgent.Toolbox.Upstream",
        "description": "算子工厂不可用",
        "detail_template": "toolbox {box_id} list failed: {error_type}: {error}",
        "detail": "算子工厂不可用",
        "solution": "稍后重试；持续失败检查 operator-integration 与网络。",
    },
    "BknAgent.Toolbox.ListFailed": {
        "code": "BknAgent.Toolbox.Upstream",
        "description": "算子工厂不可用",
        "detail_template": "toolbox {box_id} list failed: HTTP {status} {body}",
        "detail": "算子工厂不可用",
        "solution": "稍后重试；持续失败检查 operator-integration。",
    },
    "BknAgent.ToolRef.BoxUnavailable": {
        "code": "BknAgent.ParamError.ToolRef.BoxUnavailable",
        "description": "引用的工具箱不可用",
        "detail_template": "toolbox {box_id}: HTTP {status} {body}",
        "detail": "引用的工具箱不可用",
        "solution": "检查 agent.tools 里的 box_id 是否存在、当前账户是否有权访问。",
    },
    "BknAgent.ToolRef.McpUrlMissing": {
        "code": "BknAgent.ParamError.ToolRef",
        "description": "mcp 工具缺 url",
        "detail_template": "{ref}",
        "detail": "mcp 工具引用缺少 url",
        "solution": "",
    },
    "BknAgent.ToolRef.UnknownType": {
        "code": "BknAgent.ParamError.ToolRef",
        "description": "未知工具类型",
        "detail_template": "{ref}",
        "detail": "未知的工具引用类型",
        "solution": "",
    },
    "BknAgent.ToolRef.BoxIdMissing": {
        "code": "BknAgent.ParamError.ToolRef",
        "description": "toolbox 工具缺 box_id",
        "detail_template": "{ref}",
        "detail": "toolbox 工具引用缺少 box_id",
        "solution": "",
    },
    "BknAgent.ToolRef.AgentIdMissing": {
        "code": "BknAgent.ParamError.ToolRef",
        "description": "agent 工具缺 agent_id",
        "detail_template": "{ref}",
        "detail": "agent 工具引用缺少 agent_id",
        "solution": "",
    },
    "BknAgent.ToolRef.AgentUnavailable": {
        "code": "BknAgent.ParamError.ToolRef",
        "description": "agent 工具引用不可用",
        "detail_template": "agent {agent_id} 不存在或未发布",
        "detail": "被引用的 agent 不存在或未发布",
        "solution": "先发布被引用的 agent。",
    },
    "BknAgent.ToolRef.AgentNotTask": {
        "code": "BknAgent.ParamError.ToolRef",
        "description": "agent 工具只能引用一次性 agent",
        "detail_template": "agent {agent_id} mode={mode}",
        "detail": "被引用的 agent 不是一次性 agent",
        "solution": "agent-as-tool 走 /run 同款一次性执行路径，被引用方须 mode=task。",
    },
    "BknAgent.Agent.Forbidden": {
        "code": "BknAgent.Forbidden",
        "description": "无权操作他人 agent",
        "detail_template": "agent {agent_id} 属于 {owner}，调用方为 {caller}",
        "detail": "该 agent 属于其他账户",
        "solution": "只有创建者可修改或删除该 agent。",
    },
    "BknAgent.Agent.Conflict": {
        "code": "BknAgent.ParamError.Conflict",
        "description": "agent 名称或 id 已存在",
        "detail_template": "name={name} id={agent_id}",
        "detail": "agent 名称或 id 已存在",
        "solution": "换一个 name，或换/去掉预设 agent_id。",
    },
    "BknAgent.Agent.NameConflict": {
        "code": "BknAgent.ParamError.Conflict",
        "description": "agent 名称已存在",
        "detail_template": "name={name}",
        "detail": "agent 名称已存在",
        "solution": "换一个 name。",
    },
    "BknAgent.Chat.ModeMismatch": {
        "code": "BknAgent.ParamError.Mode",
        "description": "该 agent 不是对话模式",
        "detail_template": "agent {agent_id} mode={mode}",
        "detail": "该 agent 不是对话模式",
        "solution": "task agent 走 /run（M3）。",
    },
    "BknAgent.Task.ModeMismatch": {
        "code": "BknAgent.ParamError.Mode",
        "description": "该 agent 不是一次性模式",
        "detail_template": "agent {agent_id} mode={mode}",
        "detail": "该 agent 不是一次性模式",
        "solution": "对话 agent 走 /chat。",
    },
    "BknAgent.Impex.DirtyAgent": {
        "code": "BknAgent.ParamError.DirtyAgent",
        "description": "agent 数据不符合当前校验规则，无法导出",
        "detail_template": "agent {agent_id}: {error}",
        "detail": "agent 数据不符合当前校验规则",
        "solution": "先修复该 agent（PUT /agents/{id} 更新为合法配置）再导出。",
    },
    "BknAgent.Impex.OwnedByAnotherAccount": {
        "detail_template": "agent {agent_id} 属于 {owner}，不能通过导入覆盖他人 agent",
        "detail": "该 agent 属于其他账户，不能通过导入覆盖",
    },
    "BknAgent.Impex.AgentNameTaken": {
        "detail_template": "agent 名「{agent_name}」已被 {holder_id} 占用",
        "detail": "该 agent 名已被占用",
    },
    "BknAgent.Impex.PromptNameTaken": {
        "detail_template": "prompt 名「{prompt_name}」已被 {holder_id} 占用",
        "detail": "该 prompt 名已被占用",
    },
    "BknAgent.Tool.EnumHint": {
        "detail_template": "（可选值：{values}）",
        "detail": "",
    },
    "BknAgent.Impex.MissingAgentReference": {
        "detail_template": "agent {agent_name} 引用的子 agent {ref_id} 不在包内也不在目标环境",
        "detail": "被引用的子 agent 不在包内也不在目标环境",
    },
    "BknAgent.ParamError.FormatError": {
        "code": "BknAgent.ParamError.FormatError",
        "description": "参数错误",
        "detail": "请求体不符合接口定义",
        "solution": "请检查请求体格式。",
    },
    "BknAgent.Http.Unexpected": {
        "code": "BknAgent.Http.Unexpected",
        "description": "请求错误",
        "detail": "请求错误",
        "solution": "",
    },
    "BknAgent.Internal.Unexpected": {
        "code": "BknAgent.Internal.Unexpected",
        "description": "服务内部错误",
        "detail": "服务内部错误",
        "solution": "查看 bkn-agent 日志与 trace_id 定位；下游不可用时稍后重试。",
    },
    "BknAgent.Chat.Timeout": {
        "code": "BknAgent.Chat.Timeout",
        "description": "对话超时",
        "detail_template": "超过 {timeout}s",
        "detail": "对话超时",
        "solution": "",
    },
    "BknAgent.Chat.Failed": {
        "code": "BknAgent.Chat.Failed",
        "description": "对话失败",
        "detail": "对话失败",
        "solution": "",
    },
}

# Resource labels used by the shared not_found() envelope.
resource_names = {
    "agent": "agent",
    "task": "task",
    "prompt": "prompt",
    "thread": "thread",
    "agent_default_prompt": "agent 默认提示词",
    "prompt_override": "覆写",
}
