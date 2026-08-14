# English
from app.commons.errors.codes import ParamValidationErrors

error_messages = {
    ParamValidationErrors.ParamMissing: {
        "code": ParamValidationErrors.ParamMissing,
        "description": "Required parameter is missing.",
        "detail": "A required request parameter is missing.",
        "detail_template": "Missing required parameter: {parameters}",
        "detail_template_plural": "Missing required parameters: {parameters}",
        "solution": "Please read the API documentation and pass the correct parameters",
        "link": ""
    },
    ParamValidationErrors.ParamTypeError: {
        "code": ParamValidationErrors.ParamTypeError,
        "description": "Parameter type is invalid.",
        "solution": "Please read the API documentation and pass the correct parameters",
        "detail": "",
        "detail_template": "Parameter type error: {parameters}",
        "link": ""

    },
    "ModelFactory.Router.ParamError.ParamMissing": {
        "code": "ModelFactory.Router.ParamError.ParamMissing",
        "description": "Required parameter is missing.",
        "detail": "A required request parameter is missing.",
        "detail_template": "Missing required parameter: {parameters}",
        "detail_template_plural": "Missing required parameters: {parameters}",
        "solution": "Please read the API documentation and pass the correct parameters",
        "link": ""
    },
    "ModelFactory.OperationAudit.AccessDenied": {
        "code": "ModelFactory.OperationAudit.AccessDenied",
        "description": "Access denied.",
        "detail": "The current identity is not allowed to access operation audit records.",
        "solution": "Contact an administrator to request access.",
        "link": ""
    },
    "ModelFactory.OperationAudit.InvalidTimestamp": {
        "code": "ModelFactory.OperationAudit.InvalidTimestamp",
        "description": "Timestamp format is invalid.",
        "detail": "from or to must use the RFC3339 timestamp format.",
        "solution": "Use the RFC3339 timestamp format and retry the request.",
        "link": ""
    },
    "ModelFactory.OperationAudit.InvalidTimeRange": {
        "code": "ModelFactory.OperationAudit.InvalidTimeRange",
        "description": "Time range is invalid.",
        "detail": "The end time must be after the start time and the range cannot exceed 30 days.",
        "solution": "Adjust the time range and retry the request.",
        "link": ""
    },
    "ModelFactory.OperationAudit.EventNotFound": {
        "code": "ModelFactory.OperationAudit.EventNotFound",
        "description": "Operation audit event was not found.",
        "detail": "The requested operation audit event does not exist.",
        "solution": "Check the event identifier and retry the request.",
        "link": ""
    }
}
