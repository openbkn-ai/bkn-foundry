<!-- Generator: Widdershins v4.0.1 -->

<h1 id="pipeline">pipeline v0.1.0</h1>


Base URLs:

* <a href="https://{host}:{port}/api">https://{host}:{port}/api</a>

    * **host** -  Default: 服务器ip

    * **port** -  Default: 默认端口

<h1 id="pipeline-default">Default</h1>

## 获取单个管道

`GET /sdp-pipeline/v1/pipelines/{pipeline_id}`

<h3 id="获取单个管道-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|pipeline_id|path|string|true|管道 id|

> Example responses

> OK

```json
{
  "id": "pipeline1",
  "name": "管道1",
  "tags": [
    "tag1",
    "tag2"
  ],
  "comment": "",
  "builtin": false,
  "output_type": "index_base",
  "index_base": "base1",
  "input_topic": "aaa",
  "output_topic": "bbb",
  "error_topic": "ccc",
  "create_time": 1735660800000,
  "update_time": 1735660800000,
  "deployment_config": {
    "cpu_limit": 1,
    "memory_limit": 2048
  },
  "status": "running",
  "status_details": ""
}
```

> 管道不存在

```json
{
  "error_code": "DataView.DataViewNotFound",
  "description": "DataView Not Found",
  "error_detail": "some text",
  "error_link": "some text",
  "solution": "some text"
}
```

> 内部错误

```json
{
  "description": "internel server error",
  "error_code": "DataView.InternalError",
  "error_detail": "some text",
  "error_link": "some text",
  "solution": "some text"
}
```

<h3 id="获取单个管道-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|OK|[GetAPipeline](#schemagetapipeline)|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|管道不存在|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|内部错误|None|

<h3 id="获取单个管道-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 修改单个管道

`PUT /sdp-pipeline/v1/pipelines/{pipeline_id}`

> Body parameter

```json
{
  "name": "管道1",
  "tags": [
    "tag1",
    "tag2"
  ],
  "comment": "",
  "index_base": "base1",
  "deployment_config": {
    "cpu_limit": 1,
    "memory_limit": 2048
  }
}
```

<h3 id="修改单个管道-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|body|body|[UpdatePipeline](#schemaupdatepipeline)|true|none|
|pipeline_id|path|string|true|管道 id|

> Example responses

> 参数错误

```json
{
  "description": "Invalid Parameter",
  "error_code": "DataView.InvalidParameter",
  "error_detail": "some text",
  "error_link": "some text",
  "solution": "some text"
}
```

> 管道不存在

```json
{
  "error_code": "DataView.DataViewNotFound",
  "description": "DataView Not Found",
  "error_detail": "some text",
  "error_link": "some text",
  "solution": "some text"
}
```

> 内部错误

```json
{
  "description": "internel server error",
  "error_code": "DataView.InternalError",
  "error_detail": "some text",
  "error_link": "some text",
  "solution": "some text"
}
```

<h3 id="修改单个管道-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|修改成功|None|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|参数错误|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|管道不存在|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|内部错误|None|

<h3 id="修改单个管道-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 删除单个管道

`DELETE /sdp-pipeline/v1/pipelines/{pipeline_id}`

<h3 id="删除单个管道-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|pipeline_id|path|string|true|管道 id|

> Example responses

> 管道不存在

```json
{
  "error_code": "DataView.DataViewNotFound",
  "description": "DataView Not Found",
  "error_detail": "some text",
  "error_link": "some text",
  "solution": "some text"
}
```

> 内部错误

```json
{
  "description": "internel server error",
  "error_code": "DataView.InternalError",
  "error_detail": "some text",
  "error_link": "some text",
  "solution": "some text"
}
```

<h3 id="删除单个管道-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|删除成功|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|管道不存在|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|内部错误|None|

<h3 id="删除单个管道-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 修改管道属性

`PUT /sdp-pipeline/v1/pipelines/{pipeline_id}/attrs/{fields}`

> Body parameter

```json
{
  "status": "running"
}
```

<h3 id="修改管道属性-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|body|body|[UpdatePipelineStatus](#schemaupdatepipelinestatus)|true|none|
|pipeline_id|path|string|true|管道 id|
|fields|path|string|true|管道的属性，当前仅支持 status 字段，用于开启或者关闭管道|

> Example responses

> 参数错误

```json
{
  "description": "Invalid Parameter",
  "error_code": "DataView.InvalidParameter",
  "error_detail": "some text",
  "error_link": "some text",
  "solution": "some text"
}
```

> 管道不存在

```json
{
  "error_code": "DataView.DataViewNotFound",
  "description": "DataView Not Found",
  "error_detail": "some text",
  "error_link": "some text",
  "solution": "some text"
}
```

> 内部错误

```json
{
  "description": "internel server error",
  "error_code": "DataView.InternalError",
  "error_detail": "some text",
  "error_link": "some text",
  "solution": "some text"
}
```

<h3 id="修改管道属性-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|开启或暂停成功|None|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|参数错误|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|管道不存在|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|内部错误|None|

<h3 id="修改管道属性-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 查询管道列表

`GET /sdp-pipeline/v1/pipelines`

<h3 id="查询管道列表-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|name_pattern|query|string|false|根据管道名称模糊查询，默认为空. 与 name 不能同时存在|
|sort|query|string|false|排序类型，可根据名称和更新时间排序，默认是update_time|
|direction|query|string|false|排序结果方向，可选asc、desc。|
|offset|query|integer(int64)|false|分页偏移量，范围需大于等于0，默认值0|
|limit|query|integer(int64)|false|每页最多可返回的项目数；|
|tag|query|string|false|根据标签名称精确过滤|
|builtin|query|boolean|false|根据是否为内置管道过滤，值可以为true或false。如果需要传多个值，写法为 builtin=true&builtin=false|
|status|query|array[string]|false|管道状态|

#### Detailed descriptions

**direction**: 排序结果方向，可选asc、desc。
默认desc

**limit**: 每页最多可返回的项目数；
分页可选1-1000，-1表示不分页；
默认值10

**status**: 管道状态

error：失败

running：运行中

close：关闭

#### Enumerated Values

|Parameter|Value|
|---|---|
|sort|update_time|
|sort|name|
|direction|asc|
|direction|desc|

> Example responses

> OK

```json
{
  "entries": [
    {
      "id": "pipeline1",
      "name": "管道1",
      "tags": [
        "tag1",
        "tag2"
      ],
      "comment": "",
      "builtin": false,
      "output_type": "index_base",
      "index_base": "base1",
      "create_time": 1735660800000,
      "update_time": 1735660800000,
      "status": "running"
    }
  ],
  "total_count": 1
}
```

> 参数错误

```json
{
  "description": "Invalid Parameter",
  "error_code": "DataView.InvalidParameter",
  "error_detail": "some text",
  "error_link": "some text",
  "solution": "some text"
}
```

> 内部错误

```json
{
  "description": "internel server error",
  "error_code": "DataView.InternalError",
  "error_detail": "some text",
  "error_link": "some text",
  "solution": "some text"
}
```

<h3 id="查询管道列表-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|OK|[ListPipelines](#schemalistpipelines)|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|参数错误|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|内部错误|None|

<h3 id="查询管道列表-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 创建单个管道

`POST /sdp-pipeline/v1/pipelines`

> Body parameter

```json
{
  "id": "pipeline1",
  "name": "管道1",
  "tags": [
    "tag1",
    "tag2"
  ],
  "comment": "",
  "output_type": "index_base",
  "index_base": "base1",
  "deployment_config": {
    "cpu_limit": 1,
    "memory_limit": 2048
  }
}
```

<h3 id="创建单个管道-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|x-http-method-override|header|string|true|重载请求头|
|body|body|[ReqPipeline](#schemareqpipeline)|true|none|

#### Enumerated Values

|Parameter|Value|
|---|---|
|x-http-method-override|POST|
|x-http-method-override|GET|
|x-http-method-override|DELETE|

> Example responses

> 创建成功

```json
{
  "id": "pipeline1"
}
```

> 参数错误

```json
{
  "description": "Invalid Parameter",
  "error_code": "DataView.InvalidParameter",
  "error_detail": "some text",
  "error_link": "some text",
  "solution": "some text"
}
```

> 内部错误

```json
{
  "description": "internel server error",
  "error_code": "DataView.InternalError",
  "error_detail": "some text",
  "error_link": "some text",
  "solution": "some text"
}
```

<h3 id="创建单个管道-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|201|[Created](https://tools.ietf.org/html/rfc7231#section-6.3.2)|创建成功|[pipelineID](#schemapipelineid)|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|参数错误|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|内部错误|None|

<h3 id="创建单个管道-responseschema">Response Schema</h3>

### Response Headers

|Status|Header|Type|Format|Description|
|---|---|---|---|---|
|201|Location|string|uri|`/api/sdp-pipeline/v1/pipelines/{id}`|

<aside class="success">
This operation does not require authentication
</aside>

# Schemas

<h2 id="tocS_ListPipelines">ListPipelines</h2>
<!-- backwards compatibility -->
<a id="schemalistpipelines"></a>
<a id="schema_ListPipelines"></a>
<a id="tocSlistpipelines"></a>
<a id="tocslistpipelines"></a>

```json
{
  "entries": [
    {
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "builtin": true,
      "id": "string",
      "output_type": "index_base",
      "index_base": "string",
      "create_time": 0,
      "update_time": 0,
      "status": "error"
    }
  ],
  "total_count": 0
}

```

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[ListAPipeline](#schemalistapipeline)]|false|none|条目列表|
|total_count|integer|false|none|总条数|

<h2 id="tocS_GetManyPipelines">GetManyPipelines</h2>
<!-- backwards compatibility -->
<a id="schemagetmanypipelines"></a>
<a id="schema_GetManyPipelines"></a>
<a id="tocSgetmanypipelines"></a>
<a id="tocsgetmanypipelines"></a>

```json
[
  {
    "name": "string",
    "tags": [
      "string"
    ],
    "comment": "string",
    "builtin": true,
    "id": "string",
    "output_type": "index_base",
    "index_base": "string",
    "deployment_config": {
      "cpu_limit": 0,
      "memory_limit": 0
    },
    "create_time": 0,
    "update_time": 0,
    "input_topic": "string",
    "output_topic": "string",
    "error_topic": "string",
    "status": "error",
    "status_details": "string"
  }
]

```

获取多个视图信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[[GetAPipeline](#schemagetapipeline)]|false|none|获取多个视图信息|

<h2 id="tocS_UpdatePipelineStatus">UpdatePipelineStatus</h2>
<!-- backwards compatibility -->
<a id="schemaupdatepipelinestatus"></a>
<a id="schema_UpdatePipelineStatus"></a>
<a id="tocSupdatepipelinestatus"></a>
<a id="tocsupdatepipelinestatus"></a>

```json
{
  "pipeline_status": "running"
}

```

更新视图属性

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|pipeline_status|string|true|none|开启或关闭管道|

#### Enumerated Values

|Property|Value|
|---|---|
|pipeline_status|running|
|pipeline_status|close|

<h2 id="tocS_ReqPipeline">ReqPipeline</h2>
<!-- backwards compatibility -->
<a id="schemareqpipeline"></a>
<a id="schema_ReqPipeline"></a>
<a id="tocSreqpipeline"></a>
<a id="tocsreqpipeline"></a>

```json
{
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "id": "string",
  "output_type": "index_base",
  "index_base": "string",
  "deployment_config": {
    "cpu_limit": 0,
    "memory_limit": 0
  }
}

```

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|管道名称|
|tags|[string]|false|none|标签，用于业务标识|
|comment|string|false|none|备注|
|id|string|false|none|管道 ID|
|output_type|string|true|none|数据输出类型|
|index_base|string|true|none|数据输出到的索引库|
|deployment_config|[deployment_config](#schemadeployment_config)|false|none|部署配置|

#### Enumerated Values

|Property|Value|
|---|---|
|output_type|index_base|

<h2 id="tocS_deployment_config">deployment_config</h2>
<!-- backwards compatibility -->
<a id="schemadeployment_config"></a>
<a id="schema_deployment_config"></a>
<a id="tocSdeployment_config"></a>
<a id="tocsdeployment_config"></a>

```json
{
  "cpu_limit": 0,
  "memory_limit": 0
}

```

部署配置

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|cpu_limit|integer|false|none|CPU 限制|
|memory_limit|integer|false|none|内存限制|

<h2 id="tocS_UpdatePipeline">UpdatePipeline</h2>
<!-- backwards compatibility -->
<a id="schemaupdatepipeline"></a>
<a id="schema_UpdatePipeline"></a>
<a id="tocSupdatepipeline"></a>
<a id="tocsupdatepipeline"></a>

```json
{
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "index_base": "string",
  "deployment_config": {
    "cpu_limit": 0,
    "memory_limit": 0
  }
}

```

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|管道名称|
|tags|[string]|false|none|标签，用于业务标识|
|comment|string|false|none|备注|
|index_base|string|true|none|数据输出到的索引库|
|deployment_config|[deployment_config](#schemadeployment_config)|false|none|部署配置|

<h2 id="tocS_ListAPipeline">ListAPipeline</h2>
<!-- backwards compatibility -->
<a id="schemalistapipeline"></a>
<a id="schema_ListAPipeline"></a>
<a id="tocSlistapipeline"></a>
<a id="tocslistapipeline"></a>

```json
{
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "builtin": true,
  "id": "string",
  "output_type": "index_base",
  "index_base": "string",
  "create_time": 0,
  "update_time": 0,
  "status": "error"
}

```

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|false|none|管道名称|
|tags|[string]|false|none|标签，用于业务标识|
|comment|string|false|none|备注|
|builtin|boolean|false|none|内置管道标识|
|id|string|false|none|管道 ID|
|output_type|string|false|none|数据输出类型|
|index_base|string|false|none|数据输出到的索引库|
|create_time|integer|false|none|创建时间|
|update_time|integer|false|none|更新时间|
|status|string|false|none|管道状态|

#### Enumerated Values

|Property|Value|
|---|---|
|output_type|index_base|
|status|error|
|status|running|
|status|close|

<h2 id="tocS_GetAPipeline">GetAPipeline</h2>
<!-- backwards compatibility -->
<a id="schemagetapipeline"></a>
<a id="schema_GetAPipeline"></a>
<a id="tocSgetapipeline"></a>
<a id="tocsgetapipeline"></a>

```json
{
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "builtin": true,
  "id": "string",
  "output_type": "index_base",
  "index_base": "string",
  "deployment_config": {
    "cpu_limit": 0,
    "memory_limit": 0
  },
  "create_time": 0,
  "update_time": 0,
  "input_topic": "string",
  "output_topic": "string",
  "error_topic": "string",
  "status": "error",
  "status_details": "string"
}

```

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|false|none|管道名称|
|tags|[string]|false|none|标签，用于业务标识|
|comment|string|false|none|备注|
|builtin|boolean|false|none|内置管道标识|
|id|string|false|none|管道 ID|
|output_type|string|false|none|数据输出类型|
|index_base|string|false|none|数据输出到的索引库|
|deployment_config|[deployment_config](#schemadeployment_config)|false|none|部署配置|
|create_time|integer|false|none|创建时间|
|update_time|integer|false|none|更新时间|
|input_topic|string|false|none|输入topic|
|output_topic|string|false|none|输出topic|
|error_topic|string|false|none|错误 topic|
|status|string|false|none|管道状态|
|status_details|string|false|none|管道状态详情|

#### Enumerated Values

|Property|Value|
|---|---|
|output_type|index_base|
|status|error|
|status|running|
|status|close|

<h2 id="tocS_pipelineID">pipelineID</h2>
<!-- backwards compatibility -->
<a id="schemapipelineid"></a>
<a id="schema_pipelineID"></a>
<a id="tocSpipelineid"></a>
<a id="tocspipelineid"></a>

```json
{
  "id": "string"
}

```

管道id

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|false|none|管道 id|



