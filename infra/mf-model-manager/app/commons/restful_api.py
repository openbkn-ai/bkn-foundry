def get_model_restful_api_document(llm_id):
    api = {
        "openapi": "3.0.2",
        "info": {
            "title": "Large Language Model Service",
            "version": "1.0.0",
            "description": "Large language model RESTful API"
        },
        "paths": {
            "/api/model-factory/v1/llm-used/{}".format(llm_id): {
                "post": {
                    "requestBody": {
                        "content": {
                            "application/json": {
                                "schema": {
                                    "$ref": "#/components/schemas/serviceReq"
                                },
                                "examples": {
                                    "request": {
                                        "value": {
                                            "ai_system": "",
                                            "ai_user": "",
                                            "ai_assistant": "",
                                            "ai_history": [
                                                {
                                                    "role": "ai",
                                                    "message": ""
                                                },
                                                {
                                                    "role": "human",
                                                    "message": ""
                                                }
                                            ],
                                            "top_p": 1,
                                            "temperature": 1,
                                            "max_token": 16,
                                            "frequency_penalty": 1,
                                            "presence_penalty": 1
                                        }
                                    }
                                }
                            }
                        },
                        "required": False
                    },
                    "tags": [
                        "LLM Service"
                    ],
                    "parameters": [
                        {
                            "$ref": "#/components/parameters/ServiceUserTokenColumn"
                        },
                        {
                            "$ref": "#/components/parameters/ServiceUserTimeStampColumn"
                        },
                        {
                            "$ref": "#/components/parameters/ServiceUserAppKeyColumn"
                        }
                    ],
                    "responses": {
                        "200": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "$ref": "#/components/schemas/LLMResp"
                                    },
                                    "examples": {
                                        "resp": {
                                            "value": {
                                                "res": "This is the large model response"
                                            }
                                        }
                                    }
                                }
                            },
                            "description": "ok"
                        },
                        "500": {
                            "$ref": "#/components/responses/RespStaSE500"
                        }
                    },
                    "summary": "Large language model endpoint"
                }
            }
        },
        "components": {
            "schemas": {
                "serviceReq": {
                    "description": "Service request body",
                    "type": "object",
                    "properties": {
                        "ai_system": {
                            "description": "System role",
                            "type": "string"
                        },
                        "ai_user": {
                            "description": "User role",
                            "type": "string"
                        },
                        "ai_assistant": {
                            "description": "Assistant role",
                            "type": "string"
                        },
                        "ai_history": {
                            "description": "Conversation history",
                            "type": "array",
                            "items": {}
                        },
                        "top_p": {
                            "description": "Nucleus sampling",
                            "type": "number"
                        },
                        "temperature": {
                            "description": "Sampling randomness",
                            "type": "number"
                        },
                        "max_token": {
                            "description": "Maximum tokens per response",
                            "type": "integer"
                        },
                        "frequency_penalty": {
                            "description": "Frequency penalty",
                            "type": "number"
                        },
                        "presence_penalty": {
                            "description": "Presence penalty",
                            "type": "number"
                        }
                    }
                },
                "Error": {
                    "description": "Base API error envelope; see ErrorDetails for the specific error",
                    "required": [
                        "code",
                        "solution",
                        "description",
                        "detail",
                        "link"
                    ],
                    "type": "object",
                    "properties": {
                        "description": {
                            "description": "Reason for the error",
                            "type": "string"
                        },
                        "code": {
                            "description": "Stable business error code",
                            "type": "string"
                        },
                        "solution": {
                            "description": "Suggested resolution",
                            "type": "string"
                        },
                        "link": {
                            "description": "Error reference link",
                            "type": "string"
                        },
                        "detail": {
                            "description": "Error details",
                            "type": "array",
                            "items": {
                                "type": "object",
                                "properties": {
                                    "details": {
                                        "type": "string"
                                    }
                                }
                            }
                        }
                    }
                },
                "LLMResp": {
                    "description": "",
                    "required": [
                        "res"
                    ],
                    "type": "object",
                    "properties": {
                        "res": {
                            "description": "Large model invocation result",
                            "type": "object",
                            "properties": {
                                "time": {
                                    "description": "Large model generation time",
                                    "type": "string"
                                },
                                "token_len": {
                                    "description": "Total tokens returned by the large model",
                                    "type": "integer"
                                },
                                "data": {
                                    "description": "Prompt invocation result",
                                    "type": "string"
                                }
                            }
                        }
                    }
                }
            },
            "responses": {
                "RespStaSE500": {
                    "content": {
                        "application/json": {
                            "schema": {
                                "$ref": "#/components/schemas/Error"
                            },
                            "examples": {
                                "Err500": {
                                    "$ref": "#/components/examples/Status500"
                                }
                            }
                        }
                    },
                    "description": "Bad Request"
                }
            },
            "parameters": {
                "ServiceUserTokenColumn": {
                    "name": "appid",
                    "description": "appid: AnyDATA account application ID used to identify service callers",
                    "schema": {
                        "type": "string"
                    },
                    "in": "header",
                    "required": True
                },
                "ServiceUserTimeStampColumn": {
                    "name": "timestamp",
                    "description": "timestamp: Client timestamp accepted within the platform validation window",
                    "schema": {
                        "type": "string"
                    },
                    "in": "header",
                    "required": False
                },
                "ServiceUserAppKeyColumn": {
                    "name": "appkey",
                    "description": "appkey: Signature generated from appid, timestamp, and request parameters",
                    "schema": {
                        "type": "string"
                    },
                    "in": "header",
                    "required": False
                }
            },
            "examples": {
                "Status500": {
                    "summary": "Request parameter is invalid",
                    "description": "The model does not exist or the request parameters are invalid.\n",
                    "value": {
                        "description": "Parma Error",
                        "code": "LLMUsed.ParameterError",
                        "detail": "",
                        "solution": "",
                        "link": ""
                    }
                }
            }
        },
        "tags": [
            {
                "name": "LLM Service",
                "description": "Large Language Model Service API"
            }
        ]
    }
    return api


def get_prompt_restful_api_document(prompt_id, var_dict, prompt):
    api = {
        "openapi": "3.0.2",
        "info": {
            "title": "Prompt Service",
            "version": "1.0.0",
            "description": "Prompt service RESTful API"
        },
        "paths": {
            "/api/model-factory/v1/prompt/{}/used".format(prompt_id): {
                "post": {
                    "requestBody": {
                        "content": {
                            "application/json": {
                                "schema": {
                                    "$ref": "#/components/schemas/serviceReq"
                                },
                                "examples": {
                                    "request": {
                                        "value": {
                                            "inputs": var_dict,
                                            "history_dia": [
                                                {
                                                    "role": "ai",
                                                    "message": ""
                                                },
                                                {
                                                    "role": "human",
                                                    "message": ""
                                                }
                                            ]
                                        }
                                    }
                                }
                            }
                        },
                        "required": True
                    },
                    "tags": [
                        "Prompt Service"
                    ],
                    "parameters": [
                        {
                            "$ref": "#/components/parameters/ServiceUserTokenColumn"
                        },
                        {
                            "$ref": "#/components/parameters/ServiceUserTimeStampColumn"
                        },
                        {
                            "$ref": "#/components/parameters/ServiceUserAppKeyColumn"
                        }
                    ],
                    "responses": {
                        "200": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "$ref": "#/components/schemas/PromptResp"
                                    },
                                    "examples": {
                                        "resp": {
                                            "value": {
                                                "res": {
                                                    "time": 1.4746403948576325,
                                                    "token_len": 1,
                                                    "data": "hello"
                                                }
                                            }
                                        }
                                    }
                                }
                            },
                            "description": "ok"
                        },
                        "500": {
                            "$ref": "#/components/responses/RespStaSE500"
                        }
                    },
                    "summary": "Prompt engineering endpoint",
                    "description": "Current prompt: {}".format(prompt)
                }
            }
        },
        "components": {
            "schemas": {
                "serviceReq": {
                    "description": "Service request body",
                    "type": "object",
                    "properties": {
                        "inputs": {
                            "description": "Variable value",
                            "type": "object",
                            "items": {}
                        },
                        "history_dia": {
                            "description": "Conversation history",
                            "type": "array",
                            "items": {}
                        }
                    }
                },
                "Error": {
                    "description": "Base API error envelope; see ErrorDetails for the specific error",
                    "required": [
                        "code",
                        "solution",
                        "description",
                        "detail",
                        "link"
                    ],
                    "type": "object",
                    "properties": {
                        "description": {
                            "description": "Reason for the error",
                            "type": "string"
                        },
                        "code": {
                            "description": "Stable business error code",
                            "type": "string"
                        },
                        "solution": {
                            "description": "Suggested resolution",
                            "type": "string"
                        },
                        "link": {
                            "description": "Error reference link",
                            "type": "string"
                        },
                        "detail": {
                            "description": "Error details",
                            "type": "array",
                            "items": {
                                "type": "object",
                                "properties": {
                                    "details": {
                                        "type": "string"
                                    }
                                }
                            }
                        }
                    }
                },
                "PromptResp": {
                    "description": "Prompt service response",
                    "required": [
                        "res"
                    ],
                    "type": "object",
                    "properties": {
                        "res": {
                            "description": "Result",
                            "type": "object",
                            "properties": {
                                "time": {
                                    "description": "Large model generation time",
                                    "type": "string"
                                },
                                "token_len": {
                                    "description": "Total tokens returned by the large model",
                                    "type": "integer"
                                },
                                "data": {
                                    "description": "Prompt invocation result",
                                    "type": "string"
                                }
                            }
                        }
                    }
                }
            },
            "responses": {
                "RespStaSE500": {
                    "content": {
                        "application/json": {
                            "schema": {
                                "$ref": "#/components/schemas/Error"
                            },
                            "examples": {
                                "Err400": {
                                    "$ref": "#/components/examples/Status500"
                                }
                            }
                        }
                    },
                    "description": "Bad Request"
                }
            },
            "parameters": {
                "ServiceUserTokenColumn": {
                    "name": "appid",
                    "description": "appid: AnyDATA account application ID used to identify service callers",
                    "schema": {
                        "type": "string"
                    },
                    "in": "header",
                    "required": True
                },
                "ServiceUserTimeStampColumn": {
                    "name": "timestamp",
                    "description": "timestamp: Client timestamp accepted within the platform validation window",
                    "schema": {
                        "type": "string"
                    },
                    "in": "header",
                    "required": False
                },
                "ServiceUserAppKeyColumn": {
                    "name": "appkey",
                    "description": "appkey: Signature generated from appid, timestamp, and request parameters",
                    "schema": {
                        "type": "string"
                    },
                    "in": "header",
                    "required": False
                }
            },
            "examples": {
                "Status500": {
                    "summary": "Request parameter is invalid",
                    "description": "Prompt variables are invalid, the conversation history is too long, or the prompt is not published.\n",
                    "value": {
                        "description": "Parma Error",
                        "code": "PromptUsed.ParameterError",
                        "detail": "",
                        "solution": "",
                        "link": ""
                    }
                }
            }
        },
        "tags": [
            {
                "name": "Prompt Service",
                "description": "Prompt Service API"
            }
        ]
    }
    return api


def get_embedding_restful_api_document(model_name):
    api = {
        "openapi": "3.0.2",
        "info": {
            "title": "Embedding model",
            "version": "1.0.0",
            "description": "Embedding model RESTful API"
        },
        "paths": {
            "/api/model-factory/v1/small_model_run": {
                "post": {
                    "requestBody": {
                        "content": {
                            "application/json": {
                                "schema": {
                                    "$ref": "#/components/schemas/serviceReq"
                                },
                                "examples": {
                                    "request": {
                                        "value": {
                                            "model_name": model_name,
                                            "param_data": {
                                                "texts": ["test"]
                                            }
                                        }
                                    }
                                }
                            }
                        },
                        "required": True
                    },
                    "parameters": [
                        {
                            "$ref": "#/components/parameters/ServiceUserTokenColumn"
                        },
                        {
                            "$ref": "#/components/parameters/ServiceUserTimeStampColumn"
                        },
                        {
                            "$ref": "#/components/parameters/ServiceUserAppKeyColumn"
                        }
                    ],
                    "responses": {
                        "200": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "$ref": "#/components/schemas/EmbeddingResp"
                                    },
                                    "examples": {
                                        "resp": {
                                            "value": [
                                                [
                                                    1.1843433380126953,
                                                    0.7108592987060547,
                                                    -0.11932545900344849,
                                                    0.15900762379169464
                                                ]
                                            ]
                                        }
                                    }
                                }
                            },
                            "description": "ok"
                        },
                        "500": {
                            "$ref": "#/components/responses/RespStaSE500"
                        }
                    },
                    "summary": "Embedding inference endpoint"
                }
            }
        },
        "components": {
            "schemas": {
                "serviceReq": {
                    "description": "Service request body",
                    "type": "object",
                    "properties": {
                        "model_name": {
                            "description": "Model name",
                            "type": "string"
                        },
                        "param_data": {
                            "description": "Inference parameters",
                            "type": "object",
                            "properties": {
                                "texts": {
                                    "description": "Text to embed",
                                    "type": "array",
                                    "items": {}
                                }
                            }
                        }
                    }
                },
                "Error": {
                    "description": "Base API error envelope; see detail for the specific error",
                    "required": [
                        "code",
                        "solution",
                        "description",
                        "detail",
                        "link"
                    ],
                    "type": "object",
                    "properties": {
                        "description": {
                            "description": "Reason for the error",
                            "type": "string"
                        },
                        "code": {
                            "description": "Stable business error code",
                            "type": "string"
                        },
                        "solution": {
                            "description": "Suggested resolution",
                            "type": "string"
                        },
                        "link": {
                            "description": "Error reference link",
                            "type": "string"
                        },
                        "detail": {
                            "description": "Error details",
                            "type": "array",
                            "items": {
                                "type": "object",
                                "properties": {
                                    "details": {
                                        "type": "string"
                                    }
                                }
                            }
                        }
                    }
                },
                "EmbeddingResp": {
                    "description": "Embedding response",
                    "required": [
                        "res"
                    ],
                    "type": "array",
                    "items": {
                        "type": "array",
                        "items": {}
                    }
                }
            },
            "responses": {
                "RespStaSE500": {
                    "content": {
                        "application/json": {
                            "schema": {
                                "$ref": "#/components/schemas/Error"
                            },
                            "examples": {
                                "Err400": {
                                    "$ref": "#/components/examples/Status500"
                                }
                            }
                        }
                    },
                    "description": "Bad Request"
                }
            },
            "parameters": {
                "ServiceUserTokenColumn": {
                    "name": "appid",
                    "description": "appid: AnyDATA account application ID used to identify service callers",
                    "schema": {
                        "type": "string"
                    },
                    "in": "header",
                    "required": True
                },
                "ServiceUserTimeStampColumn": {
                    "name": "timestamp",
                    "description": "timestamp: Client timestamp accepted within the platform validation window",
                    "schema": {
                        "type": "string"
                    },
                    "in": "header",
                    "required": False
                },
                "ServiceUserAppKeyColumn": {
                    "name": "appkey",
                    "description": "appkey: Signature generated from appid, timestamp, and request parameters",
                    "schema": {
                        "type": "string"
                    },
                    "in": "header",
                    "required": False
                }
            },
            "examples": {
                "Status500": {
                    "summary": "Request parameter is invalid",
                    "value": {
                        "code": "ModelFactory.SmallModelRouter.SmallModelRun.ParameterError",
                        "description": "model_name is required",
                        "detail": "model_name is required",
                        "solution": "Check the request parameters and try again.",
                        "link": ""
                    }
                }
            }
        }
    }
    return api


def get_reranker_restful_api_document(model_name):
    api = {
        "openapi": "3.0.2",
        "info": {
            "title": "Reranker model",
            "version": "1.0.0",
            "description": "Reranker model RESTful API"
        },
        "paths": {
            "/api/model-factory/v1/small_model_run": {
                "post": {
                    "requestBody": {
                        "content": {
                            "application/json": {
                                "schema": {
                                    "$ref": "#/components/schemas/serviceReq"
                                },
                                "examples": {
                                    "request": {
                                        "value": {
                                            "model_name": model_name,
                                            "param_data": {
                                                "slices": ["中国有56个民族", "中国", "美国"],
                                                "query": "中国有多少民族"
                                            }
                                        }
                                    }
                                }
                            }
                        },
                        "required": True
                    },
                    "parameters": [
                        {
                            "$ref": "#/components/parameters/ServiceUserTokenColumn"
                        },
                        {
                            "$ref": "#/components/parameters/ServiceUserTimeStampColumn"
                        },
                        {
                            "$ref": "#/components/parameters/ServiceUserAppKeyColumn"
                        }
                    ],
                    "responses": {
                        "200": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "$ref": "#/components/schemas/RerankerResp"
                                    },
                                    "examples": {
                                        "resp": {
                                            "value": [
                                                1.1843433380126953,
                                                0.7108592987060547,
                                                -0.11932545900344849,
                                                0.15900762379169464
                                            ]
                                        }
                                    }
                                }
                            },
                            "description": "ok"
                        },
                        "500": {
                            "$ref": "#/components/responses/RespStaSE500"
                        }
                    },
                    "summary": "Reranker inference endpoint"
                }
            }
        },
        "components": {
            "schemas": {
                "serviceReq": {
                    "description": "Service request body",
                    "type": "object",
                    "properties": {
                        "model_name": {
                            "description": "Model name",
                            "type": "string"
                        },
                        "param_data": {
                            "description": "Inference parameters",
                            "type": "object",
                            "properties": {
                                "slices": {
                                    "description": "Documents to score",
                                    "type": "array",
                                    "items": {}
                                },
                                "query": {
                                    "description": "Query used for similarity scoring",
                                    "type": "string"
                                }
                            }
                        }
                    }
                },
                "Error": {
                    "description": "Base API error envelope; see detail for the specific error",
                    "required": [
                        "code",
                        "solution",
                        "description",
                        "detail",
                        "link"
                    ],
                    "type": "object",
                    "properties": {
                        "description": {
                            "description": "Reason for the error",
                            "type": "string"
                        },
                        "code": {
                            "description": "Stable business error code",
                            "type": "string"
                        },
                        "solution": {
                            "description": "Suggested resolution",
                            "type": "string"
                        },
                        "link": {
                            "description": "Error reference link",
                            "type": "string"
                        },
                        "detail": {
                            "description": "Error details",
                            "type": "array",
                            "items": {
                                "type": "object",
                                "properties": {
                                    "details": {
                                        "type": "string"
                                    }
                                }
                            }
                        }
                    }
                },
                "RerankerResp": {
                    "description": "Reranker response",
                    "required": [
                        "res"
                    ],
                    "type": "array",
                    "items": {
                        "type": "number"
                    }
                }
            },
            "responses": {
                "RespStaSE500": {
                    "content": {
                        "application/json": {
                            "schema": {
                                "$ref": "#/components/schemas/Error"
                            },
                            "examples": {
                                "Err400": {
                                    "$ref": "#/components/examples/Status500"
                                }
                            }
                        }
                    },
                    "description": "Bad Request"
                }
            },
            "parameters": {
                "ServiceUserTokenColumn": {
                    "name": "appid",
                    "description": "appid: AnyDATA account application ID used to identify service callers",
                    "schema": {
                        "type": "string"
                    },
                    "in": "header",
                    "required": True
                },
                "ServiceUserTimeStampColumn": {
                    "name": "timestamp",
                    "description": "timestamp: Client timestamp accepted within the platform validation window",
                    "schema": {
                        "type": "string"
                    },
                    "in": "header",
                    "required": False
                },
                "ServiceUserAppKeyColumn": {
                    "name": "appkey",
                    "description": "appkey: Signature generated from appid, timestamp, and request parameters",
                    "schema": {
                        "type": "string"
                    },
                    "in": "header",
                    "required": False
                }
            },
            "examples": {
                "Status500": {
                    "summary": "Request parameter is invalid",
                    "value": {
                        "code": "ModelFactory.SmallModelRouter.SmallModelRun.ParameterError",
                        "description": "model_name is required",
                        "detail": "model_name is required",
                        "solution": "Check the request parameters and try again.",
                        "link": ""
                    }
                }
            }
        }
    }
    return api


def get_spr_restful_api_document(model_name):
    api = {
        "openapi": "3.0.2",
        "info": {
            "title": "SPR model",
            "version": "1.0.0",
            "description": "SPR model RESTful API"
        },
        "paths": {
            "/api/model-factory/v1/small_model_run": {
                "post": {
                    "requestBody": {
                        "content": {
                            "application/json": {
                                "schema": {
                                    "$ref": "#/components/schemas/serviceReq"
                                },
                                "examples": {
                                    "request": {
                                        "value": {
                                            "model_name": model_name,
                                            "param_data": [
                                                {
                                                    "query_text": "中国国石油天然气股份有限公司天津销售分公司中生加油站的简介是什么",
                                                    "entity_dict_list": [{"企业": "中国国石油天然气股份有限公司天津销售分公司中生加油站"}],
                                                    "property_list": ["简介"],
                                                    "relation_list": [""]
                                                },
                                                {
                                                    "query_text": "同辉佳视（北京）信息技术股份有限公司的股东教育程度是什么",
                                                    "entity_dict_list": [{"企业": "同辉佳视（北京）信息技术股份有限公司"}],
                                                    "property_list": ["教育程度"],
                                                    "relation_list": ["股东"]
                                                }
                                            ]
                                        }
                                    }
                                }
                            }
                        },
                        "required": True
                    },
                    "parameters": [
                        {
                            "$ref": "#/components/parameters/ServiceUserTokenColumn"
                        },
                        {
                            "$ref": "#/components/parameters/ServiceUserTimeStampColumn"
                        },
                        {
                            "$ref": "#/components/parameters/ServiceUserAppKeyColumn"
                        }
                    ],
                    "responses": {
                        "200": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "$ref": "#/components/schemas/SprResp"
                                    },
                                    "examples": {
                                        "resp": {
                                            "value": [
                                                "entity_property",
                                                "entity_relation_pro"
                                            ]
                                        }
                                    }
                                }
                            },
                            "description": "ok"
                        },
                        "500": {
                            "$ref": "#/components/responses/RespStaSE500"
                        }
                    },
                    "summary": "SPR inference endpoint"
                }
            }
        },
        "components": {
            "schemas": {
                "serviceReq": {
                    "description": "Service request body",
                    "type": "object",
                    "properties": {
                        "model_name": {
                            "description": "Model name",
                            "type": "string"
                        },
                        "param_data": {
                            "description": "Inference parameters",
                            "type": "array",
                            "items": {
                                "type": "object",
                                "properties": {
                                    "query_text": {
                                        "description": "Query text",
                                        "type": "string"
                                    },
                                    "entity_dict_list": {
                                        "description": "Entity dictionary list",
                                        "type": "array",
                                        "items": {
                                            "type": "object"
                                        }
                                    },
                                    "property_list": {
                                        "description": "Property list",
                                        "type": "array",
                                        "items": {
                                            "type": "string"
                                        }
                                    },
                                    "relation_list": {
                                        "description": "Relation list",
                                        "type": "array",
                                        "items": {
                                            "type": "string"
                                        }
                                    }
                                }
                            }
                        }
                    }
                },
                "Error": {
                    "description": "Base API error envelope; see detail for the specific error",
                    "required": [
                        "code",
                        "solution",
                        "description",
                        "detail",
                        "link"
                    ],
                    "type": "object",
                    "properties": {
                        "description": {
                            "description": "Reason for the error",
                            "type": "string"
                        },
                        "code": {
                            "description": "Stable business error code",
                            "type": "string"
                        },
                        "solution": {
                            "description": "Suggested resolution",
                            "type": "string"
                        },
                        "link": {
                            "description": "Error reference link",
                            "type": "string"
                        },
                        "detail": {
                            "description": "Error details",
                            "type": "array",
                            "items": {
                                "type": "object",
                                "properties": {
                                    "details": {
                                        "type": "string"
                                    }
                                }
                            }
                        }
                    }
                },
                "SprResp": {
                    "description": "SPR response",
                    "required": [
                        "res"
                    ],
                    "type": "array",
                    "items": {
                        "type": "string"
                    }
                }
            },
            "responses": {
                "RespStaSE500": {
                    "content": {
                        "application/json": {
                            "schema": {
                                "$ref": "#/components/schemas/Error"
                            },
                            "examples": {
                                "Err400": {
                                    "$ref": "#/components/examples/Status500"
                                }
                            }
                        }
                    },
                    "description": "Bad Request"
                }
            },
            "parameters": {
                "ServiceUserTokenColumn": {
                    "name": "appid",
                    "description": "appid: AnyDATA account application ID used to identify service callers",
                    "schema": {
                        "type": "string"
                    },
                    "in": "header",
                    "required": True
                },
                "ServiceUserTimeStampColumn": {
                    "name": "timestamp",
                    "description": "timestamp: Client timestamp accepted within the platform validation window",
                    "schema": {
                        "type": "string"
                    },
                    "in": "header",
                    "required": False
                },
                "ServiceUserAppKeyColumn": {
                    "name": "appkey",
                    "description": "appkey: Signature generated from appid, timestamp, and request parameters",
                    "schema": {
                        "type": "string"
                    },
                    "in": "header",
                    "required": False
                }
            },
            "examples": {
                "Status500": {
                    "summary": "Request parameter is invalid",
                    "value": {
                        "code": "ModelFactory.SmallModelRouter.SmallModelRun.ParameterError",
                        "description": "model_name is required",
                        "detail": "model_name is required",
                        "solution": "Check the request parameters and try again.",
                        "link": ""
                    }
                }
            }
        }
    }
    return api


def get_info_extract_restful_api_document(model_name):
    api = {
        "openapi": "3.0.2",
        "info": {
            "title": "Information extraction model",
            "version": "1.0.0",
            "description": "Information extraction model RESTful API"
        },
        "paths": {
            "/api/model-factory/v1/small_model_run": {
                "post": {
                    "requestBody": {
                        "content": {
                            "application/json": {
                                "schema": {
                                    "$ref": "#/components/schemas/serviceReq"
                                },
                                "examples": {
                                    "request": {
                                        "value": {
                                            "model_name": model_name,
                                            "param_data": {
                                                "schema": ["人物", "time"],
                                                "texts": [
                                                    "2月8日上午北京冬奥会自由式滑雪女子大跳台决赛中中国选手谷爱凌以188.25分获得金牌！",
                                                    "In 1997, Steve was excited to become the CEO of Apple."
                                                ]
                                            }
                                        }
                                    }
                                }
                            }
                        },
                        "required": True
                    },
                    "parameters": [
                        {
                            "$ref": "#/components/parameters/ServiceUserTokenColumn"
                        },
                        {
                            "$ref": "#/components/parameters/ServiceUserTimeStampColumn"
                        },
                        {
                            "$ref": "#/components/parameters/ServiceUserAppKeyColumn"
                        }
                    ],
                    "responses": {
                        "200": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "$ref": "#/components/schemas/InfoExtractResp"
                                    },
                                    "examples": {
                                        "resp": {
                                            "value": {
                                                "res": [
                                                    {
                                                        "人物": [
                                                            {
                                                                "text": "谷爱凌",
                                                                "start": 28,
                                                                "end": 31,
                                                                "probability": 0.9972447470570458
                                                            }
                                                        ],
                                                        "time": [
                                                            {
                                                                "text": "2月8日上午",
                                                                "start": 0,
                                                                "end": 6,
                                                                "probability": 0.9955962371222924
                                                            }
                                                        ]
                                                    },
                                                    {
                                                        "人物": [
                                                            {
                                                                "text": "Steve",
                                                                "start": 9,
                                                                "end": 14,
                                                                "probability": 0.9998301338845295
                                                            }
                                                        ]
                                                    }
                                                ]
                                            }
                                        }
                                    }
                                }
                            },
                            "description": "ok"
                        },
                        "500": {
                            "$ref": "#/components/responses/RespStaSE500"
                        }
                    },
                    "summary": "Information extraction endpoint"
                }
            }
        },
        "components": {
            "schemas": {
                "serviceReq": {
                    "description": "Service request body",
                    "type": "object",
                    "properties": {
                        "model_name": {
                            "description": "Model name",
                            "type": "string"
                        },
                        "param_data": {
                            "description": "Inference parameters",
                            "type": "object",
                            "properties": {
                                "schema": {
                                    "description": "Extraction schema",
                                    "type": "array",
                                    "items": {
                                        "type": "string"
                                    }
                                },
                                "texts": {
                                    "description": "Text to extract from",
                                    "type": "array",
                                    "items": {
                                        "type": "string"
                                    }
                                }
                            }
                        }
                    }
                },
                "Error": {
                    "description": "Base API error envelope; see detail for the specific error",
                    "required": [
                        "code",
                        "solution",
                        "description",
                        "detail",
                        "link"
                    ],
                    "type": "object",
                    "properties": {
                        "description": {
                            "description": "Reason for the error",
                            "type": "string"
                        },
                        "code": {
                            "description": "Stable business error code",
                            "type": "string"
                        },
                        "solution": {
                            "description": "Suggested resolution",
                            "type": "string"
                        },
                        "link": {
                            "description": "Error reference link",
                            "type": "string"
                        },
                        "detail": {
                            "description": "Error details",
                            "type": "array",
                            "items": {
                                "type": "object",
                                "properties": {
                                    "details": {
                                        "type": "string"
                                    }
                                }
                            }
                        }
                    }
                },
                "InfoExtractResp": {
                    "description": "Information extraction response",
                    "type": "object",
                    "properties": {
                        "res": {
                            "type": "array",
                            "items": {
                                "type": "object"
                            }
                        }
                    }
                }
            },
            "responses": {
                "RespStaSE500": {
                    "content": {
                        "application/json": {
                            "schema": {
                                "$ref": "#/components/schemas/Error"
                            },
                            "examples": {
                                "Err400": {
                                    "$ref": "#/components/examples/Status500"
                                }
                            }
                        }
                    },
                    "description": "Bad Request"
                }
            },
            "parameters": {
                "ServiceUserTokenColumn": {
                    "name": "appid",
                    "description": "appid: AnyDATA account application ID used to identify service callers",
                    "schema": {
                        "type": "string"
                    },
                    "in": "header",
                    "required": True
                },
                "ServiceUserTimeStampColumn": {
                    "name": "timestamp",
                    "description": "timestamp: Client timestamp accepted within the platform validation window",
                    "schema": {
                        "type": "string"
                    },
                    "in": "header",
                    "required": False
                },
                "ServiceUserAppKeyColumn": {
                    "name": "appkey",
                    "description": "appkey: Signature generated from appid, timestamp, and request parameters",
                    "schema": {
                        "type": "string"
                    },
                    "in": "header",
                    "required": False
                }
            },
            "examples": {
                "Status500": {
                    "summary": "Request parameter is invalid",
                    "value": {
                        "code": "ModelFactory.SmallModelRouter.SmallModelRun.ParameterError",
                        "description": "model_name is required",
                        "detail": "model_name is required",
                        "solution": "Check the request parameters and try again.",
                        "link": ""
                    }
                }
            }
        }
    }
    return api


def get_audio_restful_api_document(model_name):
    api = {
        "openapi": "3.0.2",
        "info": {
            "title": "Audio model",
            "version": "1.0.0",
            "description": "Audio model RESTful API"
        },
        "paths": {
            "/api/model-factory/v1/small_model_run": {
                "post": {
                    "requestBody": {
                        "content": {
                            "application/json": {
                                "schema": {
                                    "$ref": "#/components/schemas/serviceReq"
                                },
                                "examples": {
                                    "request": {
                                        "value": {
                                            "model_name": model_name,
                                            "param_data": {
                                                "file_url": "https://test.com:443",
                                                "file_size": "方案运营策略会议.mp4",
                                                "file_name": "70337768"
                                            }
                                        }
                                    }
                                }
                            }
                        },
                        "required": True
                    },
                    "parameters": [
                        {
                            "$ref": "#/components/parameters/ServiceUserTokenColumn"
                        },
                        {
                            "$ref": "#/components/parameters/ServiceUserTimeStampColumn"
                        },
                        {
                            "$ref": "#/components/parameters/ServiceUserAppKeyColumn"
                        }
                    ],
                    "responses": {
                        "200": {
                            "content": {
                                "application/json": {
                                    "schema": {
                                        "$ref": "#/components/schemas/AudioResp"
                                    },
                                    "examples": {
                                        "resp": {
                                            "value": {
                                                "res": {
                                                    "id": "62d58c31bf034c1b96e26eaa3785dac9"
                                                }
                                            }
                                        }
                                    }
                                }
                            },
                            "description": "ok"
                        },
                        "500": {
                            "$ref": "#/components/responses/RespStaSE500"
                        }
                    },
                    "summary": "Information extraction endpoint"
                }
            }
        },
        "components": {
            "schemas": {
                "serviceReq": {
                    "description": "Service request body",
                    "type": "object",
                    "properties": {
                        "model_name": {
                            "description": "Model name",
                            "type": "string"
                        },
                        "param_data": {
                            "description": "Inference parameters",
                            "type": "object",
                            "properties": {
                                "file_url": {
                                    "description": "Audio file URL",
                                    "type": "string"
                                },
                                "file_size": {
                                    "description": "Audio file size",
                                    "type": "string"
                                },
                                "file_name": {
                                    "description": "Audio file name",
                                    "type": "string"
                                }
                            }
                        }
                    }
                },
                "Error": {
                    "description": "Base API error envelope; see detail for the specific error",
                    "required": [
                        "code",
                        "solution",
                        "description",
                        "detail",
                        "link"
                    ],
                    "type": "object",
                    "properties": {
                        "description": {
                            "description": "Reason for the error",
                            "type": "string"
                        },
                        "code": {
                            "description": "Stable business error code",
                            "type": "string"
                        },
                        "solution": {
                            "description": "Suggested resolution",
                            "type": "string"
                        },
                        "link": {
                            "description": "Error reference link",
                            "type": "string"
                        },
                        "detail": {
                            "description": "Error details",
                            "type": "array",
                            "items": {
                                "type": "object",
                                "properties": {
                                    "details": {
                                        "type": "string"
                                    }
                                }
                            }
                        }
                    }
                },
                "AudioResp": {
                    "description": "Information extraction response",
                    "type": "object",
                    "properties": {
                        "id": {
                            "type": "string"
                        }
                    }
                }
            },
            "responses": {
                "RespStaSE500": {
                    "content": {
                        "application/json": {
                            "schema": {
                                "$ref": "#/components/schemas/Error"
                            },
                            "examples": {
                                "Err400": {
                                    "$ref": "#/components/examples/Status500"
                                }
                            }
                        }
                    },
                    "description": "Bad Request"
                }
            },
            "parameters": {
                "ServiceUserTokenColumn": {
                    "name": "appid",
                    "description": "appid: AnyDATA account application ID used to identify service callers",
                    "schema": {
                        "type": "string"
                    },
                    "in": "header",
                    "required": True
                },
                "ServiceUserTimeStampColumn": {
                    "name": "timestamp",
                    "description": "timestamp: Client timestamp accepted within the platform validation window",
                    "schema": {
                        "type": "string"
                    },
                    "in": "header",
                    "required": False
                },
                "ServiceUserAppKeyColumn": {
                    "name": "appkey",
                    "description": "appkey: Signature generated from appid, timestamp, and request parameters",
                    "schema": {
                        "type": "string"
                    },
                    "in": "header",
                    "required": False
                }
            },
            "examples": {
                "Status500": {
                    "summary": "Request parameter is invalid",
                    "value": {
                        "code": "ModelFactory.SmallModelRouter.SmallModelRun.ParameterError",
                        "description": "model_name is required",
                        "detail": "model_name is required",
                        "solution": "Check the request parameters and try again.",
                        "link": ""
                    }
                }
            }
        }
    }
    return api
