# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

# Simplified Chinese message catalog.
#
# Identifiers embedded in a message (session_id, template_id, path, status) are
# machine values and stay verbatim; only the surrounding wording is translated.
# See en_us.py for why these entries carry no error code.

error_messages = {
    "Sandbox.Session.NotFound": {
        "message_template": "会话不存在：{session_id}",
        "message": "会话不存在",
    },
    "Sandbox.Session.NotActive": {
        "message_template": "会话不处于活动状态：{session_id}",
        "message": "会话不处于活动状态",
    },
    "Sandbox.Session.NoContainer": {
        "message_template": "会话没有关联容器：{session_id}",
        "message": "会话没有关联容器",
    },
    "Sandbox.Session.ContainerCreateFailed": {
        "message_template": "创建容器失败：{error}",
        "message": "创建容器失败",
    },
    "Sandbox.Template.NotFound": {
        "message_template": "模板不存在：{template_id}",
        "message": "模板不存在",
    },
    "Sandbox.Template.IdExists": {
        "message_template": "模板 ID 已存在：{template_id}",
        "message": "模板 ID 已存在",
    },
    "Sandbox.Template.NameExists": {
        "message_template": "模板名称已存在：{name}",
        "message": "模板名称已存在",
    },
    "Sandbox.File.NotFound": {
        "message_template": "文件不存在：{path}",
        "message": "文件不存在",
    },
    "Sandbox.File.InvalidPath": {
        "message_template": "文件路径非法",
        "message": "文件路径非法",
    },
    "Sandbox.File.InvalidZipEntryPath": {
        "message_template": "ZIP 条目路径非法",
        "message": "ZIP 条目路径非法",
    },
    "Sandbox.File.InvalidZipArchive": {
        "message_template": "ZIP 包无效",
        "message": "ZIP 包无效",
    },
    "Sandbox.File.ZipTooManyFiles": {
        "message_template": "ZIP 包内文件数超过上限",
        "message": "ZIP 包内文件数超过上限",
    },
    "Sandbox.File.ZipUncompressedTooLarge": {
        "message_template": "ZIP 包解压后体积超过上限",
        "message": "ZIP 包解压后体积超过上限",
    },
    "Sandbox.File.SizeExceeded": {
        "message_template": "文件大小超过 {limit_mb}MB 上限",
        "message": "文件大小超过上传上限",
    },
    "Sandbox.File.ZipOnly": {
        "message_template": "extract=true 时仅支持 ZIP 包",
        "message": "extract=true 时仅支持 ZIP 包",
    },
    "Sandbox.Execution.NotFound": {
        "message_template": "执行记录不存在：{execution_id}",
        "message": "执行记录不存在",
    },
    "Sandbox.Execution.SyncTimeout": {
        "message_template": "同步执行超时，已等待 {timeout}s",
        "message": "同步执行超时",
    },
    "Sandbox.Execution.InvalidStatus": {
        "message_template": "状态非法：{status}",
        "message": "状态非法",
    },
    "Sandbox.Scheduler.NoExecutorUrlDiscovery": {
        "message_template": "该调度器不支持执行器 URL 发现",
        "message": "该调度器不支持执行器 URL 发现",
    },
    "Sandbox.State.Conflict": {
        "message_template": "状态冲突：{error}",
        "message": "状态冲突",
    },
    "Sandbox.Internal.Unexpected": {
        "message_template": "服务内部错误",
        "message": "服务内部错误",
    },
    "Sandbox.Internal.Error": {
        "message_template": "服务内部错误：{error}",
        "message": "服务内部错误",
    },
}
