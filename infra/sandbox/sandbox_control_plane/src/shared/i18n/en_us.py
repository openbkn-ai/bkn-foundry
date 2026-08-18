# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

# English message catalog.
#
# Keys and template placeholders must match zh_cn.py exactly;
# tools/check_i18n_catalogs.py enforces that in CI.
#
# These entries carry no error code: this service's envelope is
# {"error", "message", "detail"} with no code field, and adding one would be an
# API contract change rather than a localization change. "error" stays the
# stable machine field.

error_messages = {
    "Sandbox.Session.NotFound": {
        "message_template": "Session not found: {session_id}",
        "message": "Session not found",
    },
    "Sandbox.Session.NotActive": {
        "message_template": "Session is not active: {session_id}",
        "message": "Session is not active",
    },
    "Sandbox.Session.NoContainer": {
        "message_template": "Session has no container: {session_id}",
        "message": "Session has no container",
    },
    "Sandbox.Session.ContainerCreateFailed": {
        "message_template": "Failed to create container: {error}",
        "message": "Failed to create container",
    },
    "Sandbox.Template.NotFound": {
        "message_template": "Template not found: {template_id}",
        "message": "Template not found",
    },
    "Sandbox.Template.IdExists": {
        "message_template": "Template ID already exists: {template_id}",
        "message": "Template ID already exists",
    },
    "Sandbox.Template.NameExists": {
        "message_template": "Template name already exists: {name}",
        "message": "Template name already exists",
    },
    "Sandbox.File.NotFound": {
        "message_template": "File not found: {path}",
        "message": "File not found",
    },
    "Sandbox.File.InvalidPath": {
        "message_template": "Invalid file path",
        "message": "Invalid file path",
    },
    "Sandbox.File.InvalidZipEntryPath": {
        "message_template": "Invalid ZIP entry path",
        "message": "Invalid ZIP entry path",
    },
    "Sandbox.File.InvalidZipArchive": {
        "message_template": "Invalid ZIP archive",
        "message": "Invalid ZIP archive",
    },
    "Sandbox.File.ZipTooManyFiles": {
        "message_template": "ZIP archive contains too many files",
        "message": "ZIP archive contains too many files",
    },
    "Sandbox.File.ZipUncompressedTooLarge": {
        "message_template": "ZIP archive uncompressed size exceeds limit",
        "message": "ZIP archive uncompressed size exceeds limit",
    },
    "Sandbox.File.SizeExceeded": {
        "message_template": "File size exceeds {limit_mb}MB limit",
        "message": "File size exceeds the upload limit",
    },
    "Sandbox.File.ZipOnly": {
        "message_template": "Only ZIP archives are supported when extract=true",
        "message": "Only ZIP archives are supported when extract=true",
    },
    "Sandbox.Execution.NotFound": {
        "message_template": "Execution not found: {execution_id}",
        "message": "Execution not found",
    },
    "Sandbox.Execution.SyncTimeout": {
        "message_template": "Synchronous execution timeout after {timeout}s",
        "message": "Synchronous execution timed out",
    },
    "Sandbox.Execution.InvalidStatus": {
        "message_template": "Invalid status: {status}",
        "message": "Invalid status",
    },
    "Sandbox.Scheduler.NoExecutorUrlDiscovery": {
        "message_template": "Scheduler does not support executor URL discovery",
        "message": "Scheduler does not support executor URL discovery",
    },
    "Sandbox.State.Conflict": {
        "message_template": "State conflict: {error}",
        "message": "State conflict",
    },
    "Sandbox.Internal.Unexpected": {
        "message_template": "An unexpected error occurred",
        "message": "An unexpected error occurred",
    },
    "Sandbox.Internal.Error": {
        "message_template": "Internal error: {error}",
        "message": "Internal error",
    },
}
