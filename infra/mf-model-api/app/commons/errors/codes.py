class ParamValidationErrors(object):
    """Parameter-validation error codes."""
    ParamMissing = "ParamMissing"
    ParamTypeError = "ParamTypeError"


class PermissionErrors(object):
    """Authorization error codes."""
    Unauthorized = "Unauthorized"
    Forbidden = "Forbidden"


class BusinessLogicErrors(object):
    """Business-logic error codes."""
    InvalidOperation = "InvalidOperation"
    ResourceNotFound = "ResourceNotFound"
