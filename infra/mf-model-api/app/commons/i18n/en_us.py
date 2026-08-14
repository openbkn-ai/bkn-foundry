# 简体中文
from app.commons.errors.codes import ParamValidationErrors

error_messages = {
    "ModelFactory.Stream.InvalidMessageRole": {
        "description": "Invalid conversation messages.",
        "detail": "The last conversation message must have the user role.",
        "solution": "Set the last message role to user and retry.",
    },
    "ModelFactory.Stream.ModelConnectionFailed": {
        "description": "Model service connection failed.",
        "detail": "The model service could not be reached.",
        "solution": "Check the model service configuration and availability.",
    },
    "ModelFactory.Stream.InternalError": {
        "description": "Model service internal error.",
        "detail": "The model service could not complete the request.",
        "solution": "Retry later or contact an administrator.",
    },
    "ModelFactory.Router.ParamError.ParamMissing": {
        "description": "Required parameter is missing.",
        "detail": "",
        "detail_template": "Missing required parameter: {parameter}",
        "solution": "Read the API documentation and provide the required parameters.",
    },
    "ModelFactory.Router.ParamError.FormatError": {
        "description": "Request parameter is invalid.",
        "detail": "The request parameter format is invalid.",
        "solution": "Check that the input format matches the API documentation.",
    },
    ParamValidationErrors.ParamMissing: {
        "code": ParamValidationErrors.ParamMissing,
        "description": "Required parameter is missing.",
        "detail": "",
        "detail_template": "Missing required parameter: {parameter}",
        "solution": "Read the API documentation and provide the required parameters.",
        "link": ""
    },
    ParamValidationErrors.ParamTypeError: {
        "code": ParamValidationErrors.ParamTypeError,
        "description": "Parameter type is invalid.",
        "solution": "Read the API documentation and provide the correct parameter type.",
        "detail": "The request parameter format is invalid.",
        "detail_template": "Parameter type is invalid: {parameter}",
        "link": ""

    }
}
