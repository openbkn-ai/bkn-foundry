# United States English locale resources.
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
    "ModelFactory.Router.ParamError.TypeError": {
        "description": "Parameter type is invalid.",
        "detail": "The request parameter type is invalid.",
        "detail_template": "Parameter type is invalid: {parameter}",
        "solution": "Check that the parameter type matches the API documentation.",
    },
    "ModelFactory.InternalError": {
        "description": "Request failed.",
        "detail": "The request could not be completed.",
        "solution": "Retry later or contact an administrator.",
    },
    "ModelFactory.HTTP.NotFound": {
        "description": "Resource not found.",
        "detail": "The requested resource does not exist.",
        "solution": "Check the resource identifier and try again.",
    },
    "ModelFactory.HTTP.MethodNotAllowed": {
        "description": "Method not allowed.",
        "detail": "The requested resource does not support this HTTP method.",
        "solution": "Check the request method and try again.",
    },
    "Unauthorized": {
        "description": "Authentication failed.",
        "detail": "The access token is invalid or has expired.",
        "solution": "Obtain a valid access token and try again.",
    },
    "HydraServiceError": {
        "description": "Authentication service is unavailable.",
        "detail": "The access token could not be validated.",
        "solution": "Retry later or contact an administrator.",
    },
    "BknSafeServiceError": {
        "description": "AppKey service is unavailable.",
        "detail": "The AppKey could not be validated.",
        "solution": "Retry later or contact an administrator.",
    },
    "NotPermission": {
        "description": "Permission denied.",
        "detail": "The current identity cannot perform this operation.",
        "solution": "Ask an administrator to grant the required permission.",
    },
    "ModelFactory.Mydb.DataBase.ParameterError": {
        "description": "Database access failed.",
        "detail": "Model Factory cannot access the database right now.",
        "solution": "Retry later or contact an administrator.",
    },
    "ModelFactory.MyPymysqlPool.Connection.ConnectError": {
        "description": "Database connection failed.",
        "detail": "Model Factory cannot connect to the database right now.",
        "solution": "Retry later or contact an administrator.",
    },
    "ModelFactory.ExternalSmallModel.Used.NameNotExist": {
        "description": "Model not found.",
        "detail": "The specified model name or model ID does not exist.",
        "solution": "Check the model name or model ID and try again.",
    },
    "ModelFactory.ExternalSmallModel.Used.DefaultNotExist": {
        "description": "Default small model is not configured.",
        "detail": "An administrator has not configured a default small model for this type.",
        "solution": "Configure a default small model in model management and try again.",
    },
    "ModelFactory.ExternalSmallModel.Used.ConnectError": {
        "description": "Model service connection failed.",
        "detail": "The specified small model service could not be reached.",
        "solution": "Check the model configuration and service availability.",
    },
    "ModelFactory.ModelController.Model.ConnectError": {
        "description": "Model service connection failed.",
        "detail": "The specified large model service could not be reached.",
        "solution": "Check the model configuration and service availability.",
    },
    "ModelFactory.ModelController.Model.Error": {
        "description": "Model invocation failed.",
        "detail": "The model service could not complete the request.",
        "solution": "Check the model configuration and try again.",
    },
    "ModelFactory.ConnectController.LLMUsed.ParameterError": {
        "description": "Model configuration is invalid.",
        "detail": "The current model configuration cannot complete the request.",
        "solution": "Check the large model configuration.",
    },
    "ModelFactory.LLM.Error": {
        "description": "Model context limit exceeded.",
        "detail": "The request content and maximum output tokens exceed the model context limit.",
        "solution": "Shorten the request, reduce maximum output tokens, or select a model with a larger context.",
    },
    "ModelFactory.LLM.ModelTimeoutError": {
        "description": "Model request timed out.",
        "detail": "The model service did not complete the request in time.",
        "solution": "Try again later.",
    },
    "ModelNager.ModelQuotaController.UserModelConfig.NoLeftSpace": {
        "description": "Model quota exceeded.",
        "detail": "The monthly model usage quota has been reached.",
        "solution": "Ask an administrator to increase the quota and try again.",
    },
    "ModelFactory.SmallModelController.ModelApiDoc.ModelNotFoundError": {
        "description": "Model not found.",
        "detail": "The specified model ID does not exist.",
        "solution": "Check the model ID and try again.",
    },
    "ModelFactory.ModelController.TestModel.Error": {
        "description": "Model connection test failed.",
        "detail": "The current configuration could not connect to the model service.",
        "solution": "Check the model configuration and service availability.",
    },
    "ModelFactory.ConnectController.LLMTest.ParameterError": {
        "description": "Model connection test failed.",
        "detail": "The current configuration could not connect to the large model service.",
        "solution": "Check the large model configuration and service availability.",
    },
    "ModelFactory.ExternalSmallModel.UnknownError": {
        "description": "Small model request failed.",
        "detail": "The small model request could not be completed.",
        "solution": "Retry later or contact an administrator.",
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
