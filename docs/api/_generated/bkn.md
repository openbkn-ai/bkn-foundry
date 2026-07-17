<!-- Generator: Widdershins v4.0.1 -->

<h1 id="actionschedule">ActionSchedule v0.1.0</h1>


行动调度（Action Schedule）管理。行动调度按 cron 表达式定时触发某个行动类对指定实例的
执行。所有接口在 `/api/bkn-backend/v1` 外网面下，需 OAuth2 认证；`branch` 查询参数默认
`main`。时间戳字段为 Unix 毫秒。

# Authentication

- oAuth2 authentication. OAuth2 认证，用于外网接口

    - Flow: clientCredentials

    - Token URL = [/oauth2/token](/oauth2/token)

|Scope|Scope Description|
|---|---|

<h1 id="actionschedule-actionschedule">ActionSchedule</h1>

## 创建行动调度

<a id="opIdcreateActionSchedule"></a>

`POST /api/bkn-backend/v1/knowledge-networks/{kn_id}/action-schedules`

在知识网络 / 分支下创建一个 cron 定时触发的行动调度。需 `Content-Type: application/json`。

> Body parameter

```json
{
  "name": "string",
  "action_type_id": "string",
  "cron_expression": "string",
  "_instance_identities": [
    {}
  ],
  "dynamic_params": {},
  "status": "active"
}
```

<h3 id="创建行动调度-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络 ID|
|branch|query|string|false|分支名称，默认 main|
|body|body|[ActionScheduleCreateRequest](#schemaactionschedulecreaterequest)|true|none|

> Example responses

> 201 Response

```json
{
  "id": "string"
}
```

<h3 id="创建行动调度-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|201|[Created](https://tools.ietf.org/html/rfc7231#section-6.3.2)|创建成功，返回新调度 id|Inline|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|参数错误|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|知识网络或行动类不存在|None|

<h3 id="创建行动调度-responseschema">Response Schema</h3>

Status Code **201**

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» id|string|false|none|none|

<aside class="warning">
To perform this operation, you must be authenticated by means of one of the following methods:
OAuth2
</aside>

## 列出行动调度

<a id="opIdlistActionSchedules"></a>

`GET /api/bkn-backend/v1/knowledge-networks/{kn_id}/action-schedules`

分页列出知识网络内的行动调度，支持按名称 / 行动类 / 状态过滤。

<h3 id="列出行动调度-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络 ID|
|branch|query|string|false|分支名称，默认 main|
|name_pattern|query|string|false|按名称模糊匹配，默认空|
|action_type_id|query|string|false|按绑定的行动类过滤，默认空|
|status|query|string|false|按状态过滤，默认全部|
|offset|query|integer(int64)|false|偏移量，>= 0，默认 0|
|limit|query|integer(int64)|false|每页条数，范围 [1,1000]；`-1` 表示不分页返回全部|
|sort|query|string|false|排序字段|
|direction|query|string|false|排序方向|

#### Enumerated Values

|Parameter|Value|
|---|---|
|status|active|
|status|inactive|
|sort|create_time|
|sort|update_time|
|sort|next_run_time|
|sort|last_run_time|
|sort|name|
|direction|asc|
|direction|desc|

> Example responses

> 200 Response

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "kn_id": "string",
      "branch": "string",
      "action_type_id": "string",
      "cron_expression": "string",
      "_instance_identities": [
        {}
      ],
      "dynamic_params": {},
      "status": "active",
      "last_run_time": 0,
      "next_run_time": 0,
      "lock_holder": "string",
      "lock_time": 0,
      "creator": {
        "id": "string",
        "type": "string",
        "name": "string"
      },
      "create_time": 0,
      "updater": {
        "id": "string",
        "type": "string",
        "name": "string"
      },
      "update_time": 0
    }
  ],
  "total_count": 0
}
```

<h3 id="列出行动调度-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|Inline|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|参数错误|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|知识网络不存在|None|

<h3 id="列出行动调度-responseschema">Response Schema</h3>

Status Code **200**

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» entries|[[ActionSchedule](#schemaactionschedule)]|false|none|[行动调度对象]|
|»» id|string|false|none|none|
|»» name|string|false|none|none|
|»» kn_id|string|false|none|none|
|»» branch|string|false|none|none|
|»» action_type_id|string|false|none|绑定的行动类 ID|
|»» cron_expression|string|false|none|5 段标准 cron（分 时 日 月 周）|
|»» _instance_identities|[object]|false|none|实例标识列表（注意 JSON key 带前导下划线）|
|»» dynamic_params|object|false|none|动态参数|
|»» status|string|false|none|none|
|»» last_run_time|integer(int64)|false|none|上次运行时间（Unix 毫秒）|
|»» next_run_time|integer(int64)|false|none|下次运行时间（Unix 毫秒）|
|»» lock_holder|string|false|none|none|
|»» lock_time|integer(int64)|false|none|none|
|»» creator|[AccountInfo](#schemaaccountinfo)|false|none|账户信息（创建者 / 更新者）|
|»»» id|string|false|none|none|
|»»» type|string|false|none|none|
|»»» name|string|false|none|none|
|»» create_time|integer(int64)|false|none|创建时间（Unix 毫秒）|
|»» updater|[AccountInfo](#schemaaccountinfo)|false|none|账户信息（创建者 / 更新者）|
|»» update_time|integer(int64)|false|none|更新时间（Unix 毫秒）|
|» total_count|integer(int64)|false|none|none|

#### Enumerated Values

|Property|Value|
|---|---|
|status|active|
|status|inactive|

<aside class="warning">
To perform this operation, you must be authenticated by means of one of the following methods:
OAuth2
</aside>

## 获取单个行动调度

<a id="opIdgetActionSchedule"></a>

`GET /api/bkn-backend/v1/knowledge-networks/{kn_id}/action-schedules/{schedule_id}`

<h3 id="获取单个行动调度-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络 ID|
|schedule_id|path|string|true|行动调度 ID|
|branch|query|string|false|分支名称，默认 main|

> Example responses

> 200 Response

```json
{
  "id": "string",
  "name": "string",
  "kn_id": "string",
  "branch": "string",
  "action_type_id": "string",
  "cron_expression": "string",
  "_instance_identities": [
    {}
  ],
  "dynamic_params": {},
  "status": "active",
  "last_run_time": 0,
  "next_run_time": 0,
  "lock_holder": "string",
  "lock_time": 0,
  "creator": {
    "id": "string",
    "type": "string",
    "name": "string"
  },
  "create_time": 0,
  "updater": {
    "id": "string",
    "type": "string",
    "name": "string"
  },
  "update_time": 0
}
```

<h3 id="获取单个行动调度-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[ActionSchedule](#schemaactionschedule)|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|知识网络或行动调度不存在|None|

<h3 id="获取单个行动调度-responseschema">Response Schema</h3>

<aside class="warning">
To perform this operation, you must be authenticated by means of one of the following methods:
OAuth2
</aside>

## 更新行动调度

<a id="opIdupdateActionSchedule"></a>

`PUT /api/bkn-backend/v1/knowledge-networks/{kn_id}/action-schedules/{schedule_id}`

更新调度的可变字段（名称 / cron / 实例 / 动态参数），四者至少提供其一。
本端点不改状态和行动类。需 `Content-Type: application/json`。

> Body parameter

```json
{
  "name": "string",
  "cron_expression": "string",
  "_instance_identities": [
    {}
  ],
  "dynamic_params": {}
}
```

<h3 id="更新行动调度-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络 ID|
|schedule_id|path|string|true|行动调度 ID|
|branch|query|string|false|分支名称，默认 main|
|body|body|[ActionScheduleUpdateRequest](#schemaactionscheduleupdaterequest)|true|none|

> Example responses

<h3 id="更新行动调度-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|更新成功，无响应体|None|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|参数错误|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|知识网络或行动调度不存在|None|

<h3 id="更新行动调度-responseschema">Response Schema</h3>

<aside class="warning">
To perform this operation, you must be authenticated by means of one of the following methods:
OAuth2
</aside>

## 切换行动调度状态

<a id="opIdupdateActionScheduleStatus"></a>

`PUT /api/bkn-backend/v1/knowledge-networks/{kn_id}/action-schedules/{schedule_id}/status`

激活 / 停用调度。激活时按存储的 cron 重算下次运行时间。需 `Content-Type: application/json`。

> Body parameter

```json
{
  "status": "active"
}
```

<h3 id="切换行动调度状态-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络 ID|
|schedule_id|path|string|true|行动调度 ID|
|branch|query|string|false|分支名称，默认 main|
|body|body|object|true|none|
|» status|body|string|true|none|

#### Enumerated Values

|Parameter|Value|
|---|---|
|» status|active|
|» status|inactive|

> Example responses

<h3 id="切换行动调度状态-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|切换成功，无响应体|None|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|状态非法|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|知识网络或行动调度不存在|None|

<h3 id="切换行动调度状态-responseschema">Response Schema</h3>

<aside class="warning">
To perform this operation, you must be authenticated by means of one of the following methods:
OAuth2
</aside>

## 批量删除行动调度

<a id="opIddeleteActionSchedules"></a>

`DELETE /api/bkn-backend/v1/knowledge-networks/{kn_id}/action-schedules/{schedule_ids}`

按 id 列表删除一个或多个调度。

<h3 id="批量删除行动调度-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络 ID|
|schedule_ids|path|string|true|行动调度 ID 列表，逗号分隔|
|branch|query|string|false|分支名称，默认 main|

> Example responses

<h3 id="批量删除行动调度-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|删除成功，无响应体|None|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|参数错误（id 与 kn/branch 不匹配）|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|知识网络或某个行动调度不存在|None|

<h3 id="批量删除行动调度-responseschema">Response Schema</h3>

<aside class="warning">
To perform this operation, you must be authenticated by means of one of the following methods:
OAuth2
</aside>

# Schemas

<h2 id="tocS_AccountInfo">AccountInfo</h2>
<!-- backwards compatibility -->
<a id="schemaaccountinfo"></a>
<a id="schema_AccountInfo"></a>
<a id="tocSaccountinfo"></a>
<a id="tocsaccountinfo"></a>

```json
{
  "id": "string",
  "type": "string",
  "name": "string"
}

```

账户信息（创建者 / 更新者）

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|false|none|none|
|type|string|false|none|none|
|name|string|false|none|none|

<h2 id="tocS_ActionSchedule">ActionSchedule</h2>
<!-- backwards compatibility -->
<a id="schemaactionschedule"></a>
<a id="schema_ActionSchedule"></a>
<a id="tocSactionschedule"></a>
<a id="tocsactionschedule"></a>

```json
{
  "id": "string",
  "name": "string",
  "kn_id": "string",
  "branch": "string",
  "action_type_id": "string",
  "cron_expression": "string",
  "_instance_identities": [
    {}
  ],
  "dynamic_params": {},
  "status": "active",
  "last_run_time": 0,
  "next_run_time": 0,
  "lock_holder": "string",
  "lock_time": 0,
  "creator": {
    "id": "string",
    "type": "string",
    "name": "string"
  },
  "create_time": 0,
  "updater": {
    "id": "string",
    "type": "string",
    "name": "string"
  },
  "update_time": 0
}

```

行动调度对象

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|false|none|none|
|name|string|false|none|none|
|kn_id|string|false|none|none|
|branch|string|false|none|none|
|action_type_id|string|false|none|绑定的行动类 ID|
|cron_expression|string|false|none|5 段标准 cron（分 时 日 月 周）|
|_instance_identities|[object]|false|none|实例标识列表（注意 JSON key 带前导下划线）|
|dynamic_params|object|false|none|动态参数|
|status|string|false|none|none|
|last_run_time|integer(int64)|false|none|上次运行时间（Unix 毫秒）|
|next_run_time|integer(int64)|false|none|下次运行时间（Unix 毫秒）|
|lock_holder|string|false|none|none|
|lock_time|integer(int64)|false|none|none|
|creator|[AccountInfo](#schemaaccountinfo)|false|none|账户信息（创建者 / 更新者）|
|create_time|integer(int64)|false|none|创建时间（Unix 毫秒）|
|updater|[AccountInfo](#schemaaccountinfo)|false|none|账户信息（创建者 / 更新者）|
|update_time|integer(int64)|false|none|更新时间（Unix 毫秒）|

#### Enumerated Values

|Property|Value|
|---|---|
|status|active|
|status|inactive|

<h2 id="tocS_ActionScheduleCreateRequest">ActionScheduleCreateRequest</h2>
<!-- backwards compatibility -->
<a id="schemaactionschedulecreaterequest"></a>
<a id="schema_ActionScheduleCreateRequest"></a>
<a id="tocSactionschedulecreaterequest"></a>
<a id="tocsactionschedulecreaterequest"></a>

```json
{
  "name": "string",
  "action_type_id": "string",
  "cron_expression": "string",
  "_instance_identities": [
    {}
  ],
  "dynamic_params": {},
  "status": "active"
}

```

创建行动调度请求

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|名称，最长 100|
|action_type_id|string|true|none|行动类 ID，须存在于该 KN/branch|
|cron_expression|string|true|none|合法的 5 段 cron|
|_instance_identities|[object]|true|none|实例标识列表，非空（JSON key 带前导下划线）|
|dynamic_params|object|false|none|none|
|status|string|false|none|默认 inactive；为 active 时创建即算下次运行时间|

#### Enumerated Values

|Property|Value|
|---|---|
|status|active|
|status|inactive|

<h2 id="tocS_ActionScheduleUpdateRequest">ActionScheduleUpdateRequest</h2>
<!-- backwards compatibility -->
<a id="schemaactionscheduleupdaterequest"></a>
<a id="schema_ActionScheduleUpdateRequest"></a>
<a id="tocSactionscheduleupdaterequest"></a>
<a id="tocsactionscheduleupdaterequest"></a>

```json
{
  "name": "string",
  "cron_expression": "string",
  "_instance_identities": [
    {}
  ],
  "dynamic_params": {}
}

```

更新行动调度请求（四字段至少其一）

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|false|none|最长 100|
|cron_expression|string|false|none|合法 cron；仅当调度为 active 时重算 next_run_time|
|_instance_identities|[object]|false|none|none|
|dynamic_params|object|false|none|none|



<!-- Generator: Widdershins v4.0.1 -->

<h1 id="actiontype">ActionType v0.1.0</h1>


<h1 id="actiontype-default">Default</h1>

## 获取行动类列表

`GET /api/bkn-backend/v1/knowledge-networks/{kn_id}/action-types`

<h3 id="获取行动类列表-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|name_pattern|query|string|false|根据络名称模糊查询，默认为空|
|sort|query|string|false|排序类型，默认是update_time|
|direction|query|string|false|排序结果方向，可选asc、desc。|
|offset|query|integer(int64)|false|开始响应的项目的偏移量	|
|limit|query|integer(int64)|false|每页最多可返回的项目数；|
|tag|query|string|false|根据标签精准查询，默认为空.|
|action_type|query|string|false|**查询过滤**：按行动分类枚举筛选（与响应体中 `action_type` / `action_intent` 取值域相同）。|
|object_type_id|query|string|false|绑定对象类|

#### Detailed descriptions

**direction**: 排序结果方向，可选asc、desc。
默认desc

**offset**: 开始响应的项目的偏移量	
范围需大于等于0，默认值0

**limit**: 每页最多可返回的项目数；
分页可选1-1000，-1表示不分页；
默认值10

#### Enumerated Values

|Parameter|Value|
|---|---|
|sort|update_time|
|sort|name|
|direction|asc|
|direction|desc|
|action_type|add|
|action_type|modify|
|action_type|delete|

> Example responses

> 200 Response

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "action_type": "add",
      "action_intent": "add",
      "object_type_id": "string",
      "condition": {
        "operation": "and",
        "sub_conditions": [
          {
            "operation": "and",
            "sub_conditions": []
          }
        ]
      },
      "affect": {
        "comment": "string",
        "object_type_id": "string",
        "expected_operation": "add",
        "affected_fields": [
          "string"
        ]
      },
      "impact_contracts": [
        {
          "object_type_id": "string",
          "expected_operation": "add",
          "description": "string",
          "affected_fields": [
            "string"
          ]
        }
      ],
      "action_source": {
        "type": "tool",
        "box_id": "string",
        "tool_id": "string"
      },
      "parameters": [
        {
          "name": "string",
          "type": "string",
          "source": "string",
          "value_from": "property",
          "value": "string"
        }
      ],
      "schedule": {
        "type": "FIX_RATE",
        "expression": "string"
      },
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string",
      "module_type": "action_type"
    }
  ],
  "total_count": 0
}
```

<h3 id="获取行动类列表-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[ListActionTypes](#schemalistactiontypes)|

<aside class="success">
This operation does not require authentication
</aside>

## 创建或检索行动类

`POST /api/bkn-backend/v1/knowledge-networks/{kn_id}/action-types`

> Body parameter

```json
[
  {
    "id": "restart_pod",
    "name": "重启pod",
    "tags": [
      "拓扑架构"
    ],
    "comment": "当pod状态是unknown或者failed时,重启pod",
    "icon": "",
    "color": "",
    "branch": "main",
    "action_type": "modify",
    "action_intent": "modify",
    "object_type_id": "pod",
    "condition": {
      "object_type_id": "pod",
      "field": "pod_status",
      "operation": "in",
      "value_from": "const",
      "value": [
        "Unknown",
        "Failed"
      ]
    },
    "impact_contracts": [
      {
        "object_type_id": "pod",
        "expected_operation": "modify",
        "description": "重启 pod",
        "affected_fields": []
      }
    ],
    "action_source": {
      "type": "tool",
      "box_id": "tool_123",
      "tool_id": "box_123"
    },
    "parameters": [
      {
        "name": "pod",
        "value_from": "property",
        "value": "pod_name"
      },
      {
        "name": "namespace",
        "value_from": "property",
        "value": "pod_namespace"
      },
      {
        "name": "cmd_type",
        "value_from": "input",
        "value": ""
      }
    ],
    "schedule": {
      "type": "",
      "expression": ""
    },
    "module_type": "action_type"
  }
]
```

<h3 id="创建或检索行动类-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|import_mode|query|string|false|导入模式，可选normal、ignore、overwrite，默认为normal|
|strict_mode|query|boolean|false|是否严格校验依赖，默认为true。为true时，需校验绑定对象类、影响对象类等依赖是否存在；为false时，依赖不存在不报错|
|validate_dependency|query|boolean|false|[已废弃] 请使用 strict_mode。兼容保留，strict_mode 为空时会读取此参数|
|x-http-method-override|header|string|true|重载请求头|
|body|body|[override](#schemaoverride)|true|none|

#### Enumerated Values

|Parameter|Value|
|---|---|
|import_mode|normal|
|import_mode|ignore|
|import_mode|overwrite|
|x-http-method-override|POST|
|x-http-method-override|GET|

> Example responses

> 重载GET, 检索行动类

```json
{
  "entries": [
    {
      "id": "restart_pod",
      "name": "重启pod",
      "tags": [
        "拓扑架构"
      ],
      "comment": "当pod状态是unknown或者failed时,重启pod",
      "icon": "",
      "color": "",
      "branch": "main",
      "detail": "",
      "creator": "a0f02238-6cec-11f0-82bb-fa1c4529a151",
      "create_time": 1757657606651,
      "updater": "a0f02238-6cec-11f0-82bb-fa1c4529a151",
      "update_time": 1758098535626,
      "kn_id": "kn_system_incident_event_network",
      "action_type": "modify",
      "action_intent": "modify",
      "object_type_id": "pod",
      "impact_contracts": [
        {
          "object_type_id": "pod",
          "expected_operation": "modify",
          "description": "重启pod",
          "affected_fields": []
        }
      ],
      "object_type": {
        "id": "",
        "name": "",
        "icon": "",
        "color": ""
      },
      "condition": {
        "object_type_id": "pod",
        "field": "pod_status",
        "operation": "in",
        "value_from": "const",
        "value": [
          "Unknown",
          "Failed"
        ]
      },
      "action_source": {
        "type": "tool",
        "box_id": "tool_123",
        "tool_id": "box_123"
      },
      "parameters": [
        {
          "name": "pod",
          "value_from": "property",
          "value": "pod_name"
        },
        {
          "name": "namespace",
          "value_from": "property",
          "value": "pod_namespace"
        },
        {
          "name": "cmd_type",
          "value_from": "input",
          "value": ""
        }
      ],
      "schedule": {
        "type": "",
        "expression": ""
      },
      "IfNameModify": false,
      "module_type": "action_type"
    }
  ],
  "total_count": 2,
  "search_after": [
    5.0170403,
    "restart_pod"
  ]
}
```

```json
{
  "entries": [
    {
      "id": "restart_pod_test",
      "name": "重启pod_test",
      "tags": [
        "拓扑架构"
      ],
      "comment": "当pod状态是unknown或者failed时,重启pod",
      "icon": "",
      "color": "",
      "branch": "main",
      "detail": "",
      "creator": "a0f02238-6cec-11f0-82bb-fa1c4529a151",
      "create_time": 1758346280120,
      "updater": "a0f02238-6cec-11f0-82bb-fa1c4529a151",
      "update_time": 1758346280120,
      "kn_id": "kn_system_incident_event_network",
      "action_type": "modify",
      "action_intent": "modify",
      "object_type_id": "pod",
      "impact_contracts": [
        {
          "object_type_id": "pod",
          "expected_operation": "modify",
          "description": "重启pod",
          "affected_fields": []
        }
      ],
      "object_type": {
        "id": "",
        "name": "",
        "icon": "",
        "color": ""
      },
      "condition": {
        "object_type_id": "pod",
        "field": "pod_status",
        "operation": "in",
        "value_from": "const",
        "value": [
          "Unknown",
          "Failed"
        ]
      },
      "action_source": {
        "type": "tool",
        "box_id": "tool_123",
        "tool_id": "box_123"
      },
      "parameters": [
        {
          "name": "pod",
          "value_from": "property",
          "value": "pod_name"
        },
        {
          "name": "namespace",
          "value_from": "property",
          "value": "pod_namespace"
        },
        {
          "name": "cmd_type",
          "value_from": "input",
          "value": ""
        }
      ],
      "schedule": {
        "type": "",
        "expression": ""
      },
      "IfNameModify": false,
      "module_type": "action_type"
    }
  ],
  "search_after": [
    5.011528,
    "restart_pod_test"
  ]
}
```

> 201 Response

```json
[
  {
    "id": "string"
  }
]
```

<h3 id="创建或检索行动类-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|重载GET, 检索行动类|[ActionTypeSearchResponse](#schemaactiontypesearchresponse)|
|201|[Created](https://tools.ietf.org/html/rfc7231#section-6.3.2)|ok|Inline|

<h3 id="创建或检索行动类-responseschema">Response Schema</h3>

Status Code **201**

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[[ID](#schemaid)]|false|none|[id]|
|» id|string|true|none|id|

<aside class="success">
This operation does not require authentication
</aside>

## 修改行动类

`PUT /api/bkn-backend/v1/knowledge-networks/{kn_id}/action-types/{at_id}`

> Body parameter

```json
{
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "action_type": "add",
  "action_intent": "add",
  "object_type_id": "string",
  "condition": {
    "operation": "and",
    "sub_conditions": [
      {
        "operation": "and",
        "sub_conditions": []
      }
    ]
  },
  "affect": {
    "comment": "string",
    "object_type_id": "string",
    "expected_operation": "add",
    "affected_fields": [
      "string"
    ]
  },
  "impact_contracts": [
    {
      "object_type_id": "string",
      "expected_operation": "add",
      "description": "string",
      "affected_fields": [
        "string"
      ]
    }
  ],
  "action_source": {
    "type": "tool",
    "box_id": "string",
    "tool_id": "string"
  },
  "parameters": [
    {
      "name": "string",
      "type": "string",
      "source": "string",
      "value_from": "property",
      "value": "string"
    }
  ],
  "schedule": {
    "type": "FIX_RATE",
    "expression": "string"
  }
}
```

<h3 id="修改行动类-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|at_id|path|string|true|行动类ID|
|branch|query|string|false|分支，不填则使用 main 分支|
|strict_mode|query|boolean|false|是否严格校验依赖，默认为 true。为 true 时校验绑定的对象类、影响对象类等是否存在；为 false 时不做该校验|
|validate_dependency|query|boolean|false|[已废弃] 请使用 strict_mode。兼容保留，strict_mode 为空时会读取此参数|
|body|body|[UpdateActionType](#schemaupdateactiontype)|true|none|

<h3 id="修改行动类-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|ok|None|

<aside class="success">
This operation does not require authentication
</aside>

## 获取行动类详情

`GET /api/bkn-backend/v1/knowledge-networks/{kn_id}/action-types/{at_ids}`

<h3 id="获取行动类详情-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|include_detail|query|boolean|false|是否包含说明书信息，默认false，不包含。|
|kn_id|path|string|true|业务知识网络ID|
|at_ids|path|array[string]|true|行动类ID|

> Example responses

> 200 Response

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "action_type": "add",
      "action_intent": "add",
      "object_type_id": "string",
      "object_type": {
        "id": "string",
        "name": "string",
        "icon": "string",
        "color": "string"
      },
      "condition": {
        "operation": "and",
        "sub_conditions": [
          {
            "operation": "and",
            "sub_conditions": []
          }
        ]
      },
      "affect": {
        "comment": "string",
        "object_type_id": "string",
        "expected_operation": "add",
        "affected_fields": [
          "string"
        ],
        "object_type": {
          "id": "string",
          "name": "string",
          "icon": "string",
          "color": "string"
        }
      },
      "impact_contracts": [
        {
          "object_type_id": "string",
          "expected_operation": "add",
          "description": "string",
          "affected_fields": [
            "string"
          ]
        }
      ],
      "action_source": {
        "type": "tool",
        "box_id": "string",
        "tool_id": "string"
      },
      "parameters": [
        {
          "name": "string",
          "type": "string",
          "source": "string",
          "value_from": "property",
          "value": "string"
        }
      ],
      "schedule": {
        "type": "FIX_RATE",
        "expression": "string"
      },
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string",
      "module_type": "action_type"
    }
  ]
}
```

<h3 id="获取行动类详情-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[ActionTypeDetails](#schemaactiontypedetails)|

<aside class="success">
This operation does not require authentication
</aside>

## 删除行动类

`DELETE /api/bkn-backend/v1/knowledge-networks/{kn_id}/action-types/{at_ids}`

<h3 id="删除行动类-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|at_ids|path|array[string]|true|行动类ID|

<h3 id="删除行动类-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|删除成功|None|

<aside class="success">
This operation does not require authentication
</aside>

## 校验行动类

`POST /api/bkn-backend/v1/knowledge-networks/{kn_id}/action-types/validation`

仅校验行动类依赖存在性，不写库。校验绑定对象类、影响对象类等依赖是否存在。

**响应**：HTTP 200 时 `valid`/`detail` 同其它 validate 接口；参数与鉴权错误为非 2xx。

**内部接口**：`POST /api/bkn-backend/in/v1/.../action-types/validation` 与 `POST /api/ontology-manager/in/v1/.../action-types/validation`；Header 解析访问者，无 OAuth。

> Body parameter

```json
{
  "entries": [
    {}
  ]
}
```

<h3 id="校验行动类-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|branch|query|string|false|分支，不填则使用 main 分支|
|strict_mode|query|boolean|false|是否严格校验依赖，默认为 true|
|import_mode|query|string|false|与创建行动类接口一致；用于行动类 ID/名称与落库冲突的校验语义（normal / ignore / overwrite）。|
|body|body|object|true|none|
|» entries|body|[object]|false|待校验的行动类列表，结构与创建接口一致|

#### Enumerated Values

|Parameter|Value|
|---|---|
|import_mode|normal|
|import_mode|ignore|
|import_mode|overwrite|

> Example responses

> 200 Response

```json
{
  "valid": true,
  "detail": "string"
}
```

<h3 id="校验行动类-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|已返回校验结果（通过与否均可能为 200）|Inline|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数错误等；业务校验未通过见 200 + valid:false|None|

<h3 id="校验行动类-responseschema">Response Schema</h3>

Status Code **200**

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» valid|boolean|true|none|none|
|» detail|string|false|none|当 valid 为 false 时的说明（error.Error()）|

<aside class="success">
This operation does not require authentication
</aside>

# Schemas

<h2 id="tocS_BasicInfo">BasicInfo</h2>
<!-- backwards compatibility -->
<a id="schemabasicinfo"></a>
<a id="schema_BasicInfo"></a>
<a id="tocSbasicinfo"></a>
<a id="tocsbasicinfo"></a>

```json
{
  "id": "string",
  "name": "string"
}

```

资源的基本信息，包含id和名称

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|资源ID|
|name|string|true|none|资源名称|

<h2 id="tocS_ConceptTypeResponse">ConceptTypeResponse</h2>
<!-- backwards compatibility -->
<a id="schemaconcepttyperesponse"></a>
<a id="schema_ConceptTypeResponse"></a>
<a id="tocSconcepttyperesponse"></a>
<a id="tocsconcepttyperesponse"></a>

```json
{
  "concept_type": "object_type",
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "groups": [
    "string"
  ]
}

```

对象类、关系类、行动类的查询返回结构

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|concept_type|string|true|none|概念类型|
|id|string|true|none|概念id|
|name|string|true|none|概念名称|
|tags|[string]|true|none|标签|
|groups|[string]|true|none|所属概念分组ID|

#### Enumerated Values

|Property|Value|
|---|---|
|concept_type|object_type|
|concept_type|relation_type|
|concept_type|action_type|

<h2 id="tocS_Object">Object</h2>
<!-- backwards compatibility -->
<a id="schemaobject"></a>
<a id="schema_Object"></a>
<a id="tocSobject"></a>
<a id="tocsobject"></a>

```json
{}

```

json，字段不定

### Properties

*None*

<h2 id="tocS_ExpectedImpactOperation">ExpectedImpactOperation</h2>
<!-- backwards compatibility -->
<a id="schemaexpectedimpactoperation"></a>
<a id="schema_ExpectedImpactOperation"></a>
<a id="tocSexpectedimpactoperation"></a>
<a id="tocsexpectedimpactoperation"></a>

```json
"add"

```

契约中的预期操作语义；**与 `action_intent`、`action_type` 同一枚举**（add / modify / delete）。
`impact_contracts[].expected_operation` 必填；`affect.expected_operation` 若填写则须合法。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|string|false|none|契约中的预期操作语义；**与 `action_intent`、`action_type` 同一枚举**（add / modify / delete）。<br>`impact_contracts[].expected_operation` 必填；`affect.expected_operation` 若填写则须合法。|

#### Enumerated Values

|Property|Value|
|---|---|
|*anonymous*|add|
|*anonymous*|modify|
|*anonymous*|delete|

<h2 id="tocS_ImpactContractItem">ImpactContractItem</h2>
<!-- backwards compatibility -->
<a id="schemaimpactcontractitem"></a>
<a id="schema_ImpactContractItem"></a>
<a id="tocSimpactcontractitem"></a>
<a id="tocsimpactcontractitem"></a>

```json
{
  "object_type_id": "string",
  "expected_operation": "add",
  "description": "string",
  "affected_fields": [
    "string"
  ]
}

```

行动影响契约的单条声明（结构化元数据）；与运行时 JSON、`t_action_type.f_impact_contracts` 序列化形状一致。**平台据契约做完整性等校验但不将其当作执行编排依据。** 参阅 `docs/design/bkn/features/action_type_rebuild/DESIGN.md` §7。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_type_id|string|false|none|受影响对象类 ID|
|expected_operation|[ExpectedImpactOperation](#schemaexpectedimpactoperation)|false|none|契约中的预期操作语义；**与 `action_intent`、`action_type` 同一枚举**（add / modify / delete）。<br>`impact_contracts[].expected_operation` 必填；`affect.expected_operation` 若填写则须合法。|
|description|string|false|none|可读说明（风险提示文案等）|
|affected_fields|[string]|false|none|预期牵涉的数据属性名列表|

<h2 id="tocS_Affect">Affect</h2>
<!-- backwards compatibility -->
<a id="schemaaffect"></a>
<a id="schema_Affect"></a>
<a id="tocSaffect"></a>
<a id="tocsaffect"></a>

```json
{
  "comment": "string",
  "object_type_id": "string",
  "expected_operation": "add",
  "affected_fields": [
    "string"
  ]
}

```

**[已废弃]** 请使用 `impact_contracts`（`ImpactContractItem` 数组）声明影响面。仅存单行影响时的兼容写法仍可能被服务端接受并折合为一元数组。
详见 `docs/design/bkn/features/action_type_rebuild/DESIGN.md` §5.9、§7.5。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|comment|string|false|none|影响描述|
|object_type_id|string|false|none|影响的对象类ID|
|expected_operation|[ExpectedImpactOperation](#schemaexpectedimpactoperation)|false|none|若填写须为 add / modify / delete 之一（与行动类 `action_intent` 一致）。<br>仅提交 `affect` 且服务端折行生成 `impact_contracts` 时，**仍以 `action_type` 写入契约行的 expected_operation**；本字段可省略。|
|affected_fields|[string]|false|none|预期牵涉的数据属性名列表（与 `impact_contracts[].affected_fields` 对齐）|

<h2 id="tocS_Schedule">Schedule</h2>
<!-- backwards compatibility -->
<a id="schemaschedule"></a>
<a id="schema_Schedule"></a>
<a id="tocSschedule"></a>
<a id="tocsschedule"></a>

```json
{
  "type": "FIX_RATE",
  "expression": "string"
}

```

执行频率配置项

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|type|string|true|none|执行类型。枚举，支持配置固定频率(FIX_RATE)和配置crontab表达式（CRON）|
|expression|string|true|none|执行表达式。<br><br>1.固定频率指以固定周期执行持久化，frequency=< time_durations >，用一个数字，后面跟时间单位来定义。时间单位可以是如下之一：m - 分钟； h - 小时； d - 天|

#### Enumerated Values

|Property|Value|
|---|---|
|type|FIX_RATE|
|type|CRON|

<h2 id="tocS_ID">ID</h2>
<!-- backwards compatibility -->
<a id="schemaid"></a>
<a id="schema_ID"></a>
<a id="tocSid"></a>
<a id="tocsid"></a>

```json
{
  "id": "string"
}

```

id

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|id|

<h2 id="tocS_Branch">Branch</h2>
<!-- backwards compatibility -->
<a id="schemabranch"></a>
<a id="schema_Branch"></a>
<a id="tocSbranch"></a>
<a id="tocsbranch"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "base_version": "string",
  "kn_id": "string"
}

```

分支信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|分支ID|
|name|string|true|none|分支名称|
|tags|[string]|true|none|标签|
|comment|string|true|none|备注|
|base_version|string|true|none|来源版本|
|kn_id|string|true|none|业务知识网络ID|

<h2 id="tocS_ListActionTypes">ListActionTypes</h2>
<!-- backwards compatibility -->
<a id="schemalistactiontypes"></a>
<a id="schema_ListActionTypes"></a>
<a id="tocSlistactiontypes"></a>
<a id="tocslistactiontypes"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "action_type": "add",
      "action_intent": "add",
      "object_type_id": "string",
      "condition": {
        "operation": "and",
        "sub_conditions": [
          {
            "operation": "and",
            "sub_conditions": []
          }
        ]
      },
      "affect": {
        "comment": "string",
        "object_type_id": "string",
        "expected_operation": "add",
        "affected_fields": [
          "string"
        ]
      },
      "impact_contracts": [
        {
          "object_type_id": "string",
          "expected_operation": "add",
          "description": "string",
          "affected_fields": [
            "string"
          ]
        }
      ],
      "action_source": {
        "type": "tool",
        "box_id": "string",
        "tool_id": "string"
      },
      "parameters": [
        {
          "name": "string",
          "type": "string",
          "source": "string",
          "value_from": "property",
          "value": "string"
        }
      ],
      "schedule": {
        "type": "FIX_RATE",
        "expression": "string"
      },
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string",
      "module_type": "action_type"
    }
  ],
  "total_count": 0
}

```

行动类列表

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[ActionType](#schemaactiontype)]|true|none|条目列表|
|total_count|integer|true|none|总条数|

<h2 id="tocS_ToolSource">ToolSource</h2>
<!-- backwards compatibility -->
<a id="schematoolsource"></a>
<a id="schema_ToolSource"></a>
<a id="tocStoolsource"></a>
<a id="tocstoolsource"></a>

```json
{
  "type": "tool",
  "box_id": "string",
  "tool_id": "string"
}

```

工具资源

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|type|string|true|none|资源类型|
|box_id|string|true|none|工具箱ID|
|tool_id|string|true|none|工具ID|

#### Enumerated Values

|Property|Value|
|---|---|
|type|tool|

<h2 id="tocS_ConceptGroup">ConceptGroup</h2>
<!-- backwards compatibility -->
<a id="schemaconceptgroup"></a>
<a id="schema_ConceptGroup"></a>
<a id="tocSconceptgroup"></a>
<a id="tocsconceptgroup"></a>

```json
{
  "id": "string",
  "name": "string"
}

```

概念分组

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|概念分组ID|
|name|string|true|none|概念分组名称|

<h2 id="tocS_SimpleObjectType">SimpleObjectType</h2>
<!-- backwards compatibility -->
<a id="schemasimpleobjecttype"></a>
<a id="schema_SimpleObjectType"></a>
<a id="tocSsimpleobjecttype"></a>
<a id="tocssimpleobjecttype"></a>

```json
{
  "id": "string",
  "name": "string",
  "icon": "string",
  "color": "string"
}

```

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|对象类id|
|name|string|true|none|对象类名称|
|icon|string|true|none|对象类图标|
|color|string|true|none|对象类颜色|

<h2 id="tocS_AffectDetail">AffectDetail</h2>
<!-- backwards compatibility -->
<a id="schemaaffectdetail"></a>
<a id="schema_AffectDetail"></a>
<a id="tocSaffectdetail"></a>
<a id="tocsaffectdetail"></a>

```json
{
  "comment": "string",
  "object_type_id": "string",
  "expected_operation": "add",
  "affected_fields": [
    "string"
  ],
  "object_type": {
    "id": "string",
    "name": "string",
    "icon": "string",
    "color": "string"
  }
}

```

**[已废弃]** 列表/详情中为兼容回填的单行影响；推荐使用响应中的 `impact_contracts`。
详见 `docs/design/bkn/features/action_type_rebuild/DESIGN.md` §7.5。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|comment|string|true|none|影响描述|
|object_type_id|string|true|none|影响的对象类ID|
|expected_operation|[ExpectedImpactOperation](#schemaexpectedimpactoperation)|false|none|若存在须为合法枚举（与 `action_intent` / `action_type` 一致）。|
|affected_fields|[string]|false|none|预期牵涉的数据属性名列表|
|object_type|[SimpleObjectType](#schemasimpleobjecttype)|true|none|对象类信息|

<h2 id="tocS_override">override</h2>
<!-- backwards compatibility -->
<a id="schemaoverride"></a>
<a id="schema_override"></a>
<a id="tocSoverride"></a>
<a id="tocsoverride"></a>

```json
{}

```

post 重载批量创建、行动类检索接口

### Properties

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|object|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[ReqActionTypes](#schemareqactiontypes)|false|none|批量创建请求体|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[override--get](#schemaoverride--get)|false|none|行动类检索请求体|

<h2 id="tocS_override--get">override--get</h2>
<!-- backwards compatibility -->
<a id="schemaoverride--get"></a>
<a id="schema_override--get"></a>
<a id="tocSoverride--get"></a>
<a id="tocsoverride--get"></a>

```json
{
  "concept_groups": [
    "string"
  ],
  "condition": {
    "operation": "and",
    "sub_conditions": [
      {
        "operation": "and",
        "sub_conditions": []
      }
    ]
  },
  "sort": [
    {
      "field": "string",
      "direction": "desc"
    }
  ],
  "limit": 0,
  "need_total": true
}

```

行动类检索请求体

### Properties

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[FirstQueryWithSearchAfter](#schemafirstquerywithsearchafter)|false|none|行动类检索第一次请求|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[PageTurnQueryWithSearchAfter](#schemapageturnquerywithsearchafter)|false|none|分页查询的后续分页查询请求|

<h2 id="tocS_Sort">Sort</h2>
<!-- backwards compatibility -->
<a id="schemasort"></a>
<a id="schema_Sort"></a>
<a id="tocSsort"></a>
<a id="tocssort"></a>

```json
{
  "field": "string",
  "direction": "desc"
}

```

排序字段。默认按 _score 倒序排序

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|排序字段|
|direction|string|true|none|排序方向|

#### Enumerated Values

|Property|Value|
|---|---|
|direction|desc|
|direction|asc|

<h2 id="tocS_ActionTypeSearchResponse">ActionTypeSearchResponse</h2>
<!-- backwards compatibility -->
<a id="schemaactiontypesearchresponse"></a>
<a id="schema_ActionTypeSearchResponse"></a>
<a id="tocSactiontypesearchresponse"></a>
<a id="tocsactiontypesearchresponse"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "action_type": "add",
      "action_intent": "add",
      "object_type_id": "string",
      "object_type": {
        "id": "string",
        "name": "string",
        "icon": "string",
        "color": "string"
      },
      "condition": {
        "operation": "and",
        "sub_conditions": [
          {
            "operation": "and",
            "sub_conditions": []
          }
        ]
      },
      "affect": {
        "comment": "string",
        "object_type_id": "string",
        "expected_operation": "add",
        "affected_fields": [
          "string"
        ],
        "object_type": {
          "id": "string",
          "name": "string",
          "icon": "string",
          "color": "string"
        }
      },
      "impact_contracts": [
        {
          "object_type_id": "string",
          "expected_operation": "add",
          "description": "string",
          "affected_fields": [
            "string"
          ]
        }
      ],
      "action_source": {
        "type": "tool",
        "box_id": "string",
        "tool_id": "string"
      },
      "parameters": [
        {
          "name": "string",
          "type": "string",
          "source": "string",
          "value_from": "property",
          "value": "string"
        }
      ],
      "schedule": {
        "type": "FIX_RATE",
        "expression": "string"
      },
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string",
      "module_type": "action_type"
    }
  ],
  "total_count": 0,
  "search_after": [
    null
  ]
}

```

行动类检索返回结果

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[ActionTypeDetail](#schemaactiontypedetail)]|true|none|对象实例数据|
|total_count|integer|false|none|总条数|
|search_after|[any]|true|none|表示返回的最后一个文档的排序值，获取这个用于下一次 search_after 分页。|

<h2 id="tocS_Parameter">Parameter</h2>
<!-- backwards compatibility -->
<a id="schemaparameter"></a>
<a id="schema_Parameter"></a>
<a id="tocSparameter"></a>
<a id="tocsparameter"></a>

```json
{
  "name": "string",
  "type": "string",
  "source": "string",
  "value_from": "property",
  "value": "string"
}

```

工具参数

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|参数名称|
|type|string|false|none|参数类型|
|source|string|false|none|参数来源|
|value_from|string|true|none|值来源|
|value|string|false|none|参数值。value_from=property时，填入的是对象类的数据属性名称；value_from=input时，不设置此字段|

#### Enumerated Values

|Property|Value|
|---|---|
|value_from|property|
|value_from|input|
|value_from|const|

<h2 id="tocS_ActionTypeDetails">ActionTypeDetails</h2>
<!-- backwards compatibility -->
<a id="schemaactiontypedetails"></a>
<a id="schema_ActionTypeDetails"></a>
<a id="tocSactiontypedetails"></a>
<a id="tocsactiontypedetails"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "action_type": "add",
      "action_intent": "add",
      "object_type_id": "string",
      "object_type": {
        "id": "string",
        "name": "string",
        "icon": "string",
        "color": "string"
      },
      "condition": {
        "operation": "and",
        "sub_conditions": [
          {
            "operation": "and",
            "sub_conditions": []
          }
        ]
      },
      "affect": {
        "comment": "string",
        "object_type_id": "string",
        "expected_operation": "add",
        "affected_fields": [
          "string"
        ],
        "object_type": {
          "id": "string",
          "name": "string",
          "icon": "string",
          "color": "string"
        }
      },
      "impact_contracts": [
        {
          "object_type_id": "string",
          "expected_operation": "add",
          "description": "string",
          "affected_fields": [
            "string"
          ]
        }
      ],
      "action_source": {
        "type": "tool",
        "box_id": "string",
        "tool_id": "string"
      },
      "parameters": [
        {
          "name": "string",
          "type": "string",
          "source": "string",
          "value_from": "property",
          "value": "string"
        }
      ],
      "schedule": {
        "type": "FIX_RATE",
        "expression": "string"
      },
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string",
      "module_type": "action_type"
    }
  ]
}

```

行动类详情

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[ActionTypeDetail](#schemaactiontypedetail)]|true|none|行动类详情|

<h2 id="tocS_ReqActionTypes">ReqActionTypes</h2>
<!-- backwards compatibility -->
<a id="schemareqactiontypes"></a>
<a id="schema_ReqActionTypes"></a>
<a id="tocSreqactiontypes"></a>
<a id="tocsreqactiontypes"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "action_type": "add",
      "action_intent": "add",
      "object_type_id": "string",
      "condition": {
        "operation": "and",
        "sub_conditions": [
          {
            "operation": "and",
            "sub_conditions": []
          }
        ]
      },
      "affect": {
        "comment": "string",
        "object_type_id": "string",
        "expected_operation": "add",
        "affected_fields": [
          "string"
        ]
      },
      "impact_contracts": [
        {
          "object_type_id": "string",
          "expected_operation": "add",
          "description": "string",
          "affected_fields": [
            "string"
          ]
        }
      ],
      "action_source": {
        "type": "tool",
        "box_id": "string",
        "tool_id": "string"
      },
      "parameters": [
        {
          "name": "string",
          "type": "string",
          "source": "string",
          "value_from": "property",
          "value": "string"
        }
      ],
      "schedule": {
        "type": "FIX_RATE",
        "expression": "string"
      }
    }
  ]
}

```

批量创建请求体

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[ReqActionType](#schemareqactiontype)]|true|none|行动类信息|

<h2 id="tocS_FirstQueryWithSearchAfter">FirstQueryWithSearchAfter</h2>
<!-- backwards compatibility -->
<a id="schemafirstquerywithsearchafter"></a>
<a id="schema_FirstQueryWithSearchAfter"></a>
<a id="tocSfirstquerywithsearchafter"></a>
<a id="tocsfirstquerywithsearchafter"></a>

```json
{
  "concept_groups": [
    "string"
  ],
  "condition": {
    "operation": "and",
    "sub_conditions": [
      {
        "operation": "and",
        "sub_conditions": []
      }
    ]
  },
  "sort": [
    {
      "field": "string",
      "direction": "desc"
    }
  ],
  "limit": 0,
  "need_total": true
}

```

行动类检索第一次请求

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|concept_groups|[string]|false|none|概念分组ID数组|
|condition|[Condition](#schemacondition)|true|none|行动类检索条件|
|sort|[[Sort](#schemasort)]|false|none|排序字段，默认使用 _score 排序，排序方向为 desc|
|limit|integer|true|none|返回的数量，默认值 10。范围 1-10000|
|need_total|boolean|false|none|是否需要总数，默认false|

<h2 id="tocS_PageTurnQueryWithSearchAfter">PageTurnQueryWithSearchAfter</h2>
<!-- backwards compatibility -->
<a id="schemapageturnquerywithsearchafter"></a>
<a id="schema_PageTurnQueryWithSearchAfter"></a>
<a id="tocSpageturnquerywithsearchafter"></a>
<a id="tocspageturnquerywithsearchafter"></a>

```json
{
  "concept_groups": [
    "string"
  ],
  "condition": {
    "operation": "and",
    "sub_conditions": [
      {
        "operation": "and",
        "sub_conditions": []
      }
    ]
  },
  "sort": [
    {
      "field": "string",
      "direction": "desc"
    }
  ],
  "limit": 0,
  "need_total": true,
  "search_after": [
    null
  ]
}

```

分页查询的后续分页查询请求

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|concept_groups|[string]|false|none|概念分组ID数组|
|condition|[Condition](#schemacondition)|true|none|过滤条件|
|sort|[[Sort](#schemasort)]|false|none|排序字段，默认使用 _score 排序，排序方向为 desc|
|limit|integer|true|none|返回的数量，默认值 10。范围 1-10000|
|need_total|boolean|false|none|是否需要总数，默认false|
|search_after|[any]|true|none|上次查询返回的最后一个文档的排序值。|

<h2 id="tocS_MCPSource">MCPSource</h2>
<!-- backwards compatibility -->
<a id="schemamcpsource"></a>
<a id="schema_MCPSource"></a>
<a id="tocSmcpsource"></a>
<a id="tocsmcpsource"></a>

```json
{
  "type": "mcp",
  "mcp_id": "string",
  "tool_name": "string"
}

```

MCP资源

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|type|string|true|none|资源类型|
|mcp_id|string|true|none|MCP ID|
|tool_name|string|true|none|工具名称|

#### Enumerated Values

|Property|Value|
|---|---|
|type|mcp|

<h2 id="tocS_ReqActionType">ReqActionType</h2>
<!-- backwards compatibility -->
<a id="schemareqactiontype"></a>
<a id="schema_ReqActionType"></a>
<a id="tocSreqactiontype"></a>
<a id="tocsreqactiontype"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "action_type": "add",
  "action_intent": "add",
  "object_type_id": "string",
  "condition": {
    "operation": "and",
    "sub_conditions": [
      {
        "operation": "and",
        "sub_conditions": []
      }
    ]
  },
  "affect": {
    "comment": "string",
    "object_type_id": "string",
    "expected_operation": "add",
    "affected_fields": [
      "string"
    ]
  },
  "impact_contracts": [
    {
      "object_type_id": "string",
      "expected_operation": "add",
      "description": "string",
      "affected_fields": [
        "string"
      ]
    }
  ],
  "action_source": {
    "type": "tool",
    "box_id": "string",
    "tool_id": "string"
  },
  "parameters": [
    {
      "name": "string",
      "type": "string",
      "source": "string",
      "value_from": "property",
      "value": "string"
    }
  ],
  "schedule": {
    "type": "FIX_RATE",
    "expression": "string"
  }
}

```

行动类创建信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|false|none|行动类ID|
|name|string|true|none|行动类名称|
|tags|[string]|false|none|标签。|
|comment|string|false|none|备注|
|icon|string|false|none|图标|
|color|string|false|none|颜色|
|branch|string|true|none|分支ID|
|action_type|string|true|none|**[已废弃]** 请优先使用 `action_intent`（枚举 `add`/`modify`/`delete`）。可同时出现但须与 `action_intent` 一致；仅填其一可由服务端回填。|
|action_intent|string|false|none|**推荐**：与历史 `action_type` 同枚举；对应列 `f_action_intent`。参阅 DESIGN §1.3。|
|object_type_id|string|true|none|行动类所绑定的对象类ID|
|condition|[ActionCondition](#schemaactioncondition)|false|none|行动条件|
|affect|[Affect](#schemaaffect)|false|none|**[已废弃]** 请使用 `impact_contracts`。除「仅单行 affect、由服务端折行」外，勿与 `impact_contracts` 同时提交。|
|impact_contracts|[[ImpactContractItem](#schemaimpactcontractitem)]|false|none|影响契约数组；列 `f_impact_contracts`（JSON）。与 `affect` 勿混写。|
|action_source|any|false|none|绑定的行动的资源|

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[ToolSource](#schematoolsource)|false|none|工具资源|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[MCPSource](#schemamcpsource)|false|none|MCP资源|

continued

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|parameters|[[Parameter](#schemaparameter)]|false|none|行动资源参数|
|schedule|[Schedule](#schemaschedule)|false|none|行动监听参数配置|

#### Enumerated Values

|Property|Value|
|---|---|
|action_type|add|
|action_type|modify|
|action_type|delete|
|action_intent|add|
|action_intent|modify|
|action_intent|delete|

<h2 id="tocS_UpdateActionType">UpdateActionType</h2>
<!-- backwards compatibility -->
<a id="schemaupdateactiontype"></a>
<a id="schema_UpdateActionType"></a>
<a id="tocSupdateactiontype"></a>
<a id="tocsupdateactiontype"></a>

```json
{
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "action_type": "add",
  "action_intent": "add",
  "object_type_id": "string",
  "condition": {
    "operation": "and",
    "sub_conditions": [
      {
        "operation": "and",
        "sub_conditions": []
      }
    ]
  },
  "affect": {
    "comment": "string",
    "object_type_id": "string",
    "expected_operation": "add",
    "affected_fields": [
      "string"
    ]
  },
  "impact_contracts": [
    {
      "object_type_id": "string",
      "expected_operation": "add",
      "description": "string",
      "affected_fields": [
        "string"
      ]
    }
  ],
  "action_source": {
    "type": "tool",
    "box_id": "string",
    "tool_id": "string"
  },
  "parameters": [
    {
      "name": "string",
      "type": "string",
      "source": "string",
      "value_from": "property",
      "value": "string"
    }
  ],
  "schedule": {
    "type": "FIX_RATE",
    "expression": "string"
  }
}

```

行动类更新信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|行动类名称|
|tags|[string]|false|none|标签。 （可以为空）|
|comment|string|false|none|备注（可以为空）|
|icon|string|false|none|图标|
|color|string|false|none|颜色|
|branch|string|true|none|分支ID|
|action_type|string|true|none|**[已废弃]** 请优先使用 `action_intent`；双写须一致。|
|action_intent|string|false|none|推荐；列 `f_action_intent`。|
|object_type_id|string|true|none|行动类所绑定的对象类ID|
|condition|[ActionCondition](#schemaactioncondition)|false|none|行动条件|
|affect|[Affect](#schemaaffect)|false|none|**[已废弃]** 使用 `impact_contracts`。|
|impact_contracts|[[ImpactContractItem](#schemaimpactcontractitem)]|false|none|列 `f_impact_contracts`（JSON）。|
|action_source|any|false|none|绑定的行动的资源|

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[ToolSource](#schematoolsource)|false|none|工具资源|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[MCPSource](#schemamcpsource)|false|none|MCP资源|

continued

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|parameters|[[Parameter](#schemaparameter)]|false|none|行动资源参数|
|schedule|[Schedule](#schemaschedule)|false|none|行动监听参数配置|

#### Enumerated Values

|Property|Value|
|---|---|
|action_type|add|
|action_type|modify|
|action_type|delete|
|action_intent|add|
|action_intent|modify|
|action_intent|delete|

<h2 id="tocS_ActionTypeDetail">ActionTypeDetail</h2>
<!-- backwards compatibility -->
<a id="schemaactiontypedetail"></a>
<a id="schema_ActionTypeDetail"></a>
<a id="tocSactiontypedetail"></a>
<a id="tocsactiontypedetail"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "kn_id": "string",
  "action_type": "add",
  "action_intent": "add",
  "object_type_id": "string",
  "object_type": {
    "id": "string",
    "name": "string",
    "icon": "string",
    "color": "string"
  },
  "condition": {
    "operation": "and",
    "sub_conditions": [
      {
        "operation": "and",
        "sub_conditions": []
      }
    ]
  },
  "affect": {
    "comment": "string",
    "object_type_id": "string",
    "expected_operation": "add",
    "affected_fields": [
      "string"
    ],
    "object_type": {
      "id": "string",
      "name": "string",
      "icon": "string",
      "color": "string"
    }
  },
  "impact_contracts": [
    {
      "object_type_id": "string",
      "expected_operation": "add",
      "description": "string",
      "affected_fields": [
        "string"
      ]
    }
  ],
  "action_source": {
    "type": "tool",
    "box_id": "string",
    "tool_id": "string"
  },
  "parameters": [
    {
      "name": "string",
      "type": "string",
      "source": "string",
      "value_from": "property",
      "value": "string"
    }
  ],
  "schedule": {
    "type": "FIX_RATE",
    "expression": "string"
  },
  "creator": "string",
  "create_time": 0,
  "updater": "string",
  "update_time": 0,
  "detail": "string",
  "module_type": "action_type"
}

```

行动类

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|行动类ID|
|name|string|true|none|行动类名称|
|tags|[string]|true|none|标签。 （可以为空）|
|comment|string|true|none|备注（可以为空）|
|icon|string|true|none|图标|
|color|string|true|none|颜色|
|branch|string|true|none|分支ID|
|kn_id|string|true|none|业务知识网络ID|
|action_type|string|true|none|**[已废弃]** 与 `action_intent` 等价；读侧二者通常同时返回。|
|action_intent|string|false|none|服务端返回的意图（`f_action_intent`）。|
|object_type_id|string|true|none|行动类所绑定的对象类ID|
|object_type|[SimpleObjectType](#schemasimpleobjecttype)|true|none|行动类所绑定的对象类名称.|
|condition|[ActionCondition](#schemaactioncondition)|true|none|行动条件|
|affect|[AffectDetail](#schemaaffectdetail)|true|none|**[已废弃]** 单行影响视图；等价见 `impact_contracts`。|
|impact_contracts|[[ImpactContractItem](#schemaimpactcontractitem)]|false|none|契约数组（`f_impact_contracts`）。|
|action_source|any|true|none|绑定的行动的资源|

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[ToolSource](#schematoolsource)|false|none|工具资源|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[MCPSource](#schemamcpsource)|false|none|MCP资源|

continued

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|parameters|[[Parameter](#schemaparameter)]|true|none|行动资源参数|
|schedule|[Schedule](#schemaschedule)|true|none|行动监听参数配置|
|creator|string|true|none|创建人ID|
|create_time|integer(int64)|true|none|创建时间|
|updater|string|true|none|最近一次修改人|
|update_time|integer(int64)|true|none|最近一次更新时间|
|detail|string|false|none|说明书。按需返回，若指定了include_detail=true，则返回，否则不返回|
|module_type|string|true|none|模块类型|

#### Enumerated Values

|Property|Value|
|---|---|
|action_type|add|
|action_type|modify|
|action_type|delete|
|action_intent|add|
|action_intent|modify|
|action_intent|delete|
|module_type|action_type|

<h2 id="tocS_ActionType">ActionType</h2>
<!-- backwards compatibility -->
<a id="schemaactiontype"></a>
<a id="schema_ActionType"></a>
<a id="tocSactiontype"></a>
<a id="tocsactiontype"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "kn_id": "string",
  "action_type": "add",
  "action_intent": "add",
  "object_type_id": "string",
  "condition": {
    "operation": "and",
    "sub_conditions": [
      {
        "operation": "and",
        "sub_conditions": []
      }
    ]
  },
  "affect": {
    "comment": "string",
    "object_type_id": "string",
    "expected_operation": "add",
    "affected_fields": [
      "string"
    ]
  },
  "impact_contracts": [
    {
      "object_type_id": "string",
      "expected_operation": "add",
      "description": "string",
      "affected_fields": [
        "string"
      ]
    }
  ],
  "action_source": {
    "type": "tool",
    "box_id": "string",
    "tool_id": "string"
  },
  "parameters": [
    {
      "name": "string",
      "type": "string",
      "source": "string",
      "value_from": "property",
      "value": "string"
    }
  ],
  "schedule": {
    "type": "FIX_RATE",
    "expression": "string"
  },
  "creator": "string",
  "create_time": 0,
  "updater": "string",
  "update_time": 0,
  "detail": "string",
  "module_type": "action_type"
}

```

行动类

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|行动类ID|
|name|string|true|none|行动类名称|
|tags|[string]|true|none|标签。 （可以为空）|
|comment|string|true|none|备注（可以为空）|
|icon|string|true|none|图标|
|color|string|true|none|颜色|
|branch|string|true|none|分支ID|
|kn_id|string|true|none|业务知识网络ID|
|action_type|string|true|none|**[已废弃]** 请读 `action_intent`。|
|action_intent|string|false|none|意图字段（与 `action_type` 回填一致）。|
|object_type_id|string|true|none|行动类所绑定的对象类ID|
|condition|[ActionCondition](#schemaactioncondition)|true|none|行动条件|
|affect|[Affect](#schemaaffect)|true|none|**[已废弃]** 见 `impact_contracts`。|
|impact_contracts|[[ImpactContractItem](#schemaimpactcontractitem)]|false|none|[行动影响契约的单条声明（结构化元数据）；与运行时 JSON、`t_action_type.f_impact_contracts` 序列化形状一致。**平台据契约做完整性等校验但不将其当作执行编排依据。** 参阅 `docs/design/bkn/features/action_type_rebuild/DESIGN.md` §7。<br>]|
|action_source|any|true|none|绑定的行动的资源|

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[ToolSource](#schematoolsource)|false|none|工具资源|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[MCPSource](#schemamcpsource)|false|none|MCP资源|

continued

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|parameters|[[Parameter](#schemaparameter)]|true|none|行动资源参数|
|schedule|[Schedule](#schemaschedule)|true|none|行动监听参数配置|
|creator|string|true|none|创建人ID|
|create_time|integer(int64)|true|none|创建时间|
|updater|string|true|none|最近一次修改人|
|update_time|integer(int64)|true|none|最近一次更新时间|
|detail|string|false|none|说明书。按需返回，若指定了include_detail=true，则返回，否则不返回|
|module_type|string|true|none|模块类型|

#### Enumerated Values

|Property|Value|
|---|---|
|action_type|add|
|action_type|modify|
|action_type|delete|
|action_intent|add|
|action_intent|modify|
|action_intent|delete|
|module_type|action_type|

<h2 id="tocS_condition_or">condition_or</h2>
<!-- backwards compatibility -->
<a id="schemacondition_or"></a>
<a id="schema_condition_or"></a>
<a id="tocScondition_or"></a>
<a id="tocscondition_or"></a>

```json
{
  "operation": "or",
  "sub_conditions": [
    {
      "operation": "and",
      "sub_conditions": [
        {
          "operation": "and",
          "sub_conditions": []
        }
      ]
    }
  ]
}

```

or 的过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|operation|string|true|none|过滤操作符|
|sub_conditions|[[Condition](#schemacondition)]|true|none|子过滤条件|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|or|

<h2 id="tocS_condition_eq">condition_eq</h2>
<!-- backwards compatibility -->
<a id="schemacondition_eq"></a>
<a id="schema_condition_eq"></a>
<a id="tocScondition_eq"></a>
<a id="tocscondition_eq"></a>

```json
{
  "field": "id",
  "operation": "==",
  "value": null
}

```

等于过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，等于支持的字段类型：数值、字符串|
|operation|string|true|none|操作符|
|value|any|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|id|
|field|name|
|field|comment|
|field|detail|
|operation|==|

<h2 id="tocS_condition_not_eq">condition_not_eq</h2>
<!-- backwards compatibility -->
<a id="schemacondition_not_eq"></a>
<a id="schema_condition_not_eq"></a>
<a id="tocScondition_not_eq"></a>
<a id="tocscondition_not_eq"></a>

```json
{
  "field": "id",
  "operation": "!=",
  "value": null
}

```

不等于的过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，不等于支持的字段类型：数值、字符串|
|operation|string|true|none|操作符|
|value|any|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|id|
|field|name|
|field|comment|
|field|detail|
|operation|!=|

<h2 id="tocS_condition_in">condition_in</h2>
<!-- backwards compatibility -->
<a id="schemacondition_in"></a>
<a id="schema_condition_in"></a>
<a id="tocScondition_in"></a>
<a id="tocscondition_in"></a>

```json
{
  "field": "id",
  "operation": "in",
  "value": [
    null
  ]
}

```

包含过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，包含支持所有类型|
|operation|string|true|none|操作符|
|value|[any]|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|id|
|field|name|
|field|comment|
|field|detail|
|operation|in|

<h2 id="tocS_condition_like">condition_like</h2>
<!-- backwards compatibility -->
<a id="schemacondition_like"></a>
<a id="schema_condition_like"></a>
<a id="tocScondition_like"></a>
<a id="tocscondition_like"></a>

```json
{
  "field": "id",
  "operation": "like",
  "value": "string"
}

```

like过滤

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，相似支持的字段类型：字符串|
|operation|string|true|none|操作符|
|value|string|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|id|
|field|name|
|field|comment|
|field|detail|
|operation|like|

<h2 id="tocS_condition_not_like">condition_not_like</h2>
<!-- backwards compatibility -->
<a id="schemacondition_not_like"></a>
<a id="schema_condition_not_like"></a>
<a id="tocScondition_not_like"></a>
<a id="tocscondition_not_like"></a>

```json
{
  "field": "id",
  "operation": "not_like",
  "value": "string"
}

```

not_like过滤

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，不相似支持的字段类型：字符串|
|operation|string|true|none|操作符|
|value|string|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|id|
|field|name|
|field|comment|
|field|detail|
|operation|not_like|

<h2 id="tocS_condition_regex">condition_regex</h2>
<!-- backwards compatibility -->
<a id="schemacondition_regex"></a>
<a id="schema_condition_regex"></a>
<a id="tocScondition_regex"></a>
<a id="tocscondition_regex"></a>

```json
{
  "field": "id",
  "operation": "regex",
  "value": "string"
}

```

regex过滤

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，正则支持的字段类型：字符串|
|operation|string|true|none|操作符|
|value|string|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|id|
|field|name|
|field|comment|
|field|detail|
|operation|regex|

<h2 id="tocS_condition_multi_match">condition_multi_match</h2>
<!-- backwards compatibility -->
<a id="schemacondition_multi_match"></a>
<a id="schema_condition_multi_match"></a>
<a id="tocScondition_multi_match"></a>
<a id="tocscondition_multi_match"></a>

```json
{
  "fields": [
    "string"
  ],
  "operation": "multi_match",
  "value": "string",
  "match_type": "best_fields"
}

```

多字段全文匹配

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|fields|[string]|false|none|过滤字段数组，多字段全文匹配支持的字段类型：字符串。为空时，用opensearch中 index.default_field 配置的字段进行查询。当需要对所有字段进行匹配时，此参数传 ["*"]. 可支持的字段有：name, comment, detail, *|
|operation|string|true|none|操作符|
|value|string|true|none|过滤值|
|match_type|string|false|none|全文匹配类型，默认是 best_fields|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|multi_match|
|match_type|best_fields|
|match_type|most_fields|
|match_type|cross_fields|
|match_type|phrase|
|match_type|phrase_prefix|
|match_type|bool_prefix|

<h2 id="tocS_condition_not_in">condition_not_in</h2>
<!-- backwards compatibility -->
<a id="schemacondition_not_in"></a>
<a id="schema_condition_not_in"></a>
<a id="tocScondition_not_in"></a>
<a id="tocscondition_not_in"></a>

```json
{
  "field": "id",
  "operation": "not_in",
  "value": [
    null
  ]
}

```

not_in过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，不包含支持所有类型|
|operation|string|true|none|操作符|
|value|[any]|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|id|
|field|name|
|field|comment|
|field|detail|
|operation|not_in|

<h2 id="tocS_condition_match_phrase">condition_match_phrase</h2>
<!-- backwards compatibility -->
<a id="schemacondition_match_phrase"></a>
<a id="schema_condition_match_phrase"></a>
<a id="tocScondition_match_phrase"></a>
<a id="tocscondition_match_phrase"></a>

```json
{
  "field": "name",
  "operation": "match_phrase",
  "value": "string"
}

```

match_phrase 过滤，支持单个字段和*, * 表示全部字段

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，短语匹配支持的字段类型：字符串|
|operation|string|true|none|操作符|
|value|string|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|name|
|field|comment|
|field|detail|
|field|*|
|operation|match_phrase|

<h2 id="tocS_condition_match">condition_match</h2>
<!-- backwards compatibility -->
<a id="schemacondition_match"></a>
<a id="schema_condition_match"></a>
<a id="tocScondition_match"></a>
<a id="tocscondition_match"></a>

```json
{
  "field": "name",
  "operation": "match",
  "value": "string"
}

```

match 过滤，支持单个字段和*, * 表示全部字段

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，全文匹配支持的字段类型：字符串|
|operation|string|true|none|操作符|
|value|string|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|name|
|field|comment|
|field|detail|
|field|*|
|operation|match|

<h2 id="tocS_condition_knn">condition_knn</h2>
<!-- backwards compatibility -->
<a id="schemacondition_knn"></a>
<a id="schema_condition_knn"></a>
<a id="tocScondition_knn"></a>
<a id="tocscondition_knn"></a>

```json
{
  "field": "*",
  "operation": "knn",
  "value": 0,
  "limit_key": "k",
  "limit_value": 100,
  "sub_conditions": [
    {
      "operation": "and",
      "sub_conditions": [
        {
          "operation": "and",
          "sub_conditions": []
        }
      ]
    }
  ]
}

```

knn 过滤，支持单个字段和*, * 表示"_vector"

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，概念索引是内部生成，不对外暴露，所以knn过滤时，field 传 * 即可|
|operation|string|true|none|操作符|
|value|number|true|none|过滤值。当limit_key为k时，limit_value为整型；当limit_key为max_distance和min_score时，limit_value为浮点型|
|limit_key|string|false|none|执行径向搜索时使用的过滤和评分行为, k:返回最相似的limit_value个结果；max_distance:返回距离小于等于limit_value的结果；min_score：返回相似度分数大于等于limit_value的结果。默认值为k|
|limit_value|number|false|none|执行径向搜索使用的值。默认值为100|
|sub_conditions|[[Condition](#schemacondition)]|false|none|knn下的子查询|

#### Enumerated Values

|Property|Value|
|---|---|
|field|*|
|operation|knn|
|limit_key|k|
|limit_key|max_distance|
|limit_key|min_score|

<h2 id="tocS_condition_and">condition_and</h2>
<!-- backwards compatibility -->
<a id="schemacondition_and"></a>
<a id="schema_condition_and"></a>
<a id="tocScondition_and"></a>
<a id="tocscondition_and"></a>

```json
{
  "operation": "and",
  "sub_conditions": [
    {
      "operation": "and",
      "sub_conditions": []
    }
  ]
}

```

and的过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|operation|string|true|none|过滤操作符|
|sub_conditions|[[Condition](#schemacondition)]|true|none|子过滤条件|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|and|

<h2 id="tocS_Condition">Condition</h2>
<!-- backwards compatibility -->
<a id="schemacondition"></a>
<a id="schema_Condition"></a>
<a id="tocScondition"></a>
<a id="tocscondition"></a>

```json
{
  "operation": "and",
  "sub_conditions": [
    {
      "operation": "and",
      "sub_conditions": []
    }
  ]
}

```

### Properties

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_and](#schemacondition_and)|false|none|and的过滤条件|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_or](#schemacondition_or)|false|none|or 的过滤条件|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_eq](#schemacondition_eq)|false|none|等于过滤条件|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_not_eq](#schemacondition_not_eq)|false|none|不等于的过滤条件|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_in](#schemacondition_in)|false|none|包含过滤条件|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_not_in](#schemacondition_not_in)|false|none|not_in过滤条件|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_like](#schemacondition_like)|false|none|like过滤|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_not_like](#schemacondition_not_like)|false|none|not_like过滤|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_regex](#schemacondition_regex)|false|none|regex过滤|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_match](#schemacondition_match)|false|none|match 过滤，支持单个字段和*, * 表示全部字段|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_match_phrase](#schemacondition_match_phrase)|false|none|match_phrase 过滤，支持单个字段和*, * 表示全部字段|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_knn](#schemacondition_knn)|false|none|knn 过滤，支持单个字段和*, * 表示"_vector"|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_multi_match](#schemacondition_multi_match)|false|none|多字段全文匹配|

<h2 id="tocS_action_condition_and">action_condition_and</h2>
<!-- backwards compatibility -->
<a id="schemaaction_condition_and"></a>
<a id="schema_action_condition_and"></a>
<a id="tocSaction_condition_and"></a>
<a id="tocsaction_condition_and"></a>

```json
{
  "operation": "and",
  "sub_conditions": [
    {
      "operation": "and",
      "sub_conditions": []
    }
  ]
}

```

and的过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|operation|string|true|none|过滤操作符|
|sub_conditions|[[ActionCondition](#schemaactioncondition)]|true|none|子过滤条件|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|and|

<h2 id="tocS_action_condition_or">action_condition_or</h2>
<!-- backwards compatibility -->
<a id="schemaaction_condition_or"></a>
<a id="schema_action_condition_or"></a>
<a id="tocSaction_condition_or"></a>
<a id="tocsaction_condition_or"></a>

```json
{
  "operation": "or",
  "sub_conditions": [
    {
      "operation": "and",
      "sub_conditions": [
        {
          "operation": "and",
          "sub_conditions": []
        }
      ]
    }
  ]
}

```

or 的过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|operation|string|true|none|过滤操作符|
|sub_conditions|[[ActionCondition](#schemaactioncondition)]|true|none|子过滤条件|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|or|

<h2 id="tocS_action_condition_eq">action_condition_eq</h2>
<!-- backwards compatibility -->
<a id="schemaaction_condition_eq"></a>
<a id="schema_action_condition_eq"></a>
<a id="tocSaction_condition_eq"></a>
<a id="tocsaction_condition_eq"></a>

```json
{
  "object_type_id": "string",
  "field": "string",
  "operation": "==",
  "value": null
}

```

等于过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_type_id|string|true|none|对象类ID。当时多个对象类的过滤时，需要把对象类ID带上，否则只要属性名属于对象类就会进行过滤。|
|field|string|true|none|过滤字段，即对象类的属性名称。支持的属性类型为数值、字符串|
|operation|string|true|none|操作符|
|value|any|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|==|

<h2 id="tocS_action_condition_not_eq">action_condition_not_eq</h2>
<!-- backwards compatibility -->
<a id="schemaaction_condition_not_eq"></a>
<a id="schema_action_condition_not_eq"></a>
<a id="tocSaction_condition_not_eq"></a>
<a id="tocsaction_condition_not_eq"></a>

```json
{
  "object_type_id": "string",
  "field": "string",
  "operation": "!=",
  "value": null
}

```

不等于的过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_type_id|string|true|none|对象类ID。当时多个对象类的过滤时，需要把对象类ID带上，否则只要属性名属于对象类就会进行过滤。|
|field|string|true|none|过滤字段，即对象类的属性名称。支持的属性类型为数值、字符串|
|operation|string|true|none|操作符|
|value|any|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|!=|

<h2 id="tocS_action_condition_gt">action_condition_gt</h2>
<!-- backwards compatibility -->
<a id="schemaaction_condition_gt"></a>
<a id="schema_action_condition_gt"></a>
<a id="tocSaction_condition_gt"></a>
<a id="tocsaction_condition_gt"></a>

```json
{
  "object_type_id": "string",
  "field": "string",
  "operation": ">",
  "value": null
}

```

大于的过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_type_id|string|true|none|对象类ID。当时多个对象类的过滤时，需要把对象类ID带上，否则只要属性名属于对象类就会进行过滤。|
|field|string|true|none|过滤字段，即对象类的属性名称。支持的属性类型为数值、字符串|
|operation|string|true|none|操作符|
|value|any|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|>|

<h2 id="tocS_action_condition_lt">action_condition_lt</h2>
<!-- backwards compatibility -->
<a id="schemaaction_condition_lt"></a>
<a id="schema_action_condition_lt"></a>
<a id="tocSaction_condition_lt"></a>
<a id="tocsaction_condition_lt"></a>

```json
{
  "object_type_id": "string",
  "field": "string",
  "operation": "<",
  "value": null
}

```

小于的过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_type_id|string|true|none|对象类ID。当时多个对象类的过滤时，需要把对象类ID带上，否则只要属性名属于对象类就会进行过滤。|
|field|string|true|none|过滤字段，即对象类的属性名称。支持的属性类型为数值、字符串|
|operation|string|true|none|操作符|
|value|any|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|<|

<h2 id="tocS_action_condition_gte">action_condition_gte</h2>
<!-- backwards compatibility -->
<a id="schemaaction_condition_gte"></a>
<a id="schema_action_condition_gte"></a>
<a id="tocSaction_condition_gte"></a>
<a id="tocsaction_condition_gte"></a>

```json
{
  "object_type_id": "string",
  "field": "string",
  "operation": ">=",
  "value": null
}

```

大于等于的过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_type_id|string|true|none|对象类ID。当时多个对象类的过滤时，需要把对象类ID带上，否则只要属性名属于对象类就会进行过滤。|
|field|string|true|none|过滤字段，即对象类的属性名称。支持的属性类型为数值、字符串|
|operation|string|true|none|操作符|
|value|any|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|>=|

<h2 id="tocS_action_condition_lte">action_condition_lte</h2>
<!-- backwards compatibility -->
<a id="schemaaction_condition_lte"></a>
<a id="schema_action_condition_lte"></a>
<a id="tocSaction_condition_lte"></a>
<a id="tocsaction_condition_lte"></a>

```json
{
  "object_type_id": "string",
  "field": "string",
  "operation": "<=",
  "value": null
}

```

大于等于的过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_type_id|string|true|none|对象类ID。当时多个对象类的过滤时，需要把对象类ID带上，否则只要属性名属于对象类就会进行过滤。|
|field|string|true|none|过滤字段，即对象类的属性名称。支持的属性类型为数值、字符串|
|operation|string|true|none|操作符|
|value|any|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|<=|

<h2 id="tocS_action_condition_in">action_condition_in</h2>
<!-- backwards compatibility -->
<a id="schemaaction_condition_in"></a>
<a id="schema_action_condition_in"></a>
<a id="tocSaction_condition_in"></a>
<a id="tocsaction_condition_in"></a>

```json
{
  "object_type_id": "string",
  "field": "string",
  "operation": "in",
  "value": [
    null
  ]
}

```

包含过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_type_id|string|true|none|对象类ID。当时多个对象类的过滤时，需要把对象类ID带上，否则只要属性名属于对象类就会进行过滤。|
|field|string|true|none|过滤字段，即对象类的属性名称。支持的属性类型为所有类型|
|operation|string|true|none|操作符|
|value|[any]|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|in|

<h2 id="tocS_action_condition_not_in">action_condition_not_in</h2>
<!-- backwards compatibility -->
<a id="schemaaction_condition_not_in"></a>
<a id="schema_action_condition_not_in"></a>
<a id="tocSaction_condition_not_in"></a>
<a id="tocsaction_condition_not_in"></a>

```json
{
  "object_type_id": "string",
  "field": "string",
  "operation": "not_in",
  "value": [
    null
  ]
}

```

不包含过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_type_id|string|true|none|对象类ID。当时多个对象类的过滤时，需要把对象类ID带上，否则只要属性名属于对象类就会进行过滤。|
|field|string|true|none|过滤字段，即对象类的属性名称。支持的属性类型为所有类型|
|operation|string|true|none|操作符|
|value|[any]|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|not_in|

<h2 id="tocS_action_condition_out_range">action_condition_out_range</h2>
<!-- backwards compatibility -->
<a id="schemaaction_condition_out_range"></a>
<a id="schema_action_condition_out_range"></a>
<a id="tocSaction_condition_out_range"></a>
<a id="tocsaction_condition_out_range"></a>

```json
{
  "object_type_id": "string",
  "field": "string",
  "operation": "range",
  "value": [
    null
  ]
}

```

范围外过滤。右侧值为长度为2的数组，边界为左闭右开, 即 [ value[0],  value[1] )。此种情况下，符合过滤条件的值的区间为 (-inf, value[0] ) & [ value[1], +inf )，即左侧指定字段＜value[0] 或 ≥value[1] 的值。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_type_id|string|true|none|对象类ID。当时多个对象类的过滤时，需要把对象类ID带上，否则只要属性名属于对象类就会进行过滤。|
|field|string|true|none|过滤字段，即对象类的属性名称。支持的属性类型为时间类型、数值|
|operation|string|true|none|操作符|
|value|[any]|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|range|

<h2 id="tocS_action_condition_exist">action_condition_exist</h2>
<!-- backwards compatibility -->
<a id="schemaaction_condition_exist"></a>
<a id="schema_action_condition_exist"></a>
<a id="tocSaction_condition_exist"></a>
<a id="tocsaction_condition_exist"></a>

```json
{
  "object_type_id": "string",
  "field": "string",
  "operation": "exist"
}

```

存在过滤

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_type_id|string|true|none|对象类ID。当时多个对象类的过滤时，需要把对象类ID带上，否则只要属性名属于对象类就会进行过滤。|
|field|string|true|none|过滤字段，即对象类的属性名称。支持的属性类型为所有类型|
|operation|string|true|none|操作符|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|exist|

<h2 id="tocS_action_condition_range">action_condition_range</h2>
<!-- backwards compatibility -->
<a id="schemaaction_condition_range"></a>
<a id="schema_action_condition_range"></a>
<a id="tocSaction_condition_range"></a>
<a id="tocsaction_condition_range"></a>

```json
{
  "object_type_id": "string",
  "field": "string",
  "operation": "range",
  "value": [
    null
  ]
}

```

范围内过滤。右侧值为长度为 2 的数组，边界为左闭右开

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_type_id|string|true|none|对象类ID。当时多个对象类的过滤时，需要把对象类ID带上，否则只要属性名属于对象类就会进行过滤。|
|field|string|true|none|过滤字段，即对象类的属性名称。支持的属性类型为时间类型、数值|
|operation|string|true|none|操作符|
|value|[any]|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|range|

<h2 id="tocS_action_condition_not_exist">action_condition_not_exist</h2>
<!-- backwards compatibility -->
<a id="schemaaction_condition_not_exist"></a>
<a id="schema_action_condition_not_exist"></a>
<a id="tocSaction_condition_not_exist"></a>
<a id="tocsaction_condition_not_exist"></a>

```json
{
  "object_type_id": "string",
  "field": "string",
  "operation": "not_exist"
}

```

不存在过滤

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_type_id|string|true|none|对象类ID。当时多个对象类的过滤时，需要把对象类ID带上，否则只要属性名属于对象类就会进行过滤。|
|field|string|true|none|过滤字段，即对象类的属性名称。支持的属性类型为所有类型|
|operation|string|true|none|操作符|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|not_exist|

<h2 id="tocS_ActionCondition">ActionCondition</h2>
<!-- backwards compatibility -->
<a id="schemaactioncondition"></a>
<a id="schema_ActionCondition"></a>
<a id="tocSactioncondition"></a>
<a id="tocsactioncondition"></a>

```json
{
  "operation": "and",
  "sub_conditions": [
    {
      "operation": "and",
      "sub_conditions": []
    }
  ]
}

```

### Properties

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[action_condition_and](#schemaaction_condition_and)|false|none|and的过滤条件|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[action_condition_or](#schemaaction_condition_or)|false|none|or 的过滤条件|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[action_condition_eq](#schemaaction_condition_eq)|false|none|等于过滤条件|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[action_condition_not_eq](#schemaaction_condition_not_eq)|false|none|不等于的过滤条件|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[action_condition_gt](#schemaaction_condition_gt)|false|none|大于的过滤条件|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[action_condition_gte](#schemaaction_condition_gte)|false|none|大于等于的过滤条件|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[action_condition_lt](#schemaaction_condition_lt)|false|none|小于的过滤条件|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[action_condition_lte](#schemaaction_condition_lte)|false|none|大于等于的过滤条件|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[action_condition_in](#schemaaction_condition_in)|false|none|包含过滤条件|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[action_condition_not_in](#schemaaction_condition_not_in)|false|none|不包含过滤条件|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[action_condition_range](#schemaaction_condition_range)|false|none|范围内过滤。右侧值为长度为 2 的数组，边界为左闭右开|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[action_condition_out_range](#schemaaction_condition_out_range)|false|none|范围外过滤。右侧值为长度为2的数组，边界为左闭右开, 即 [ value[0],  value[1] )。此种情况下，符合过滤条件的值的区间为 (-inf, value[0] ) & [ value[1], +inf )，即左侧指定字段＜value[0] 或 ≥value[1] 的值。|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[action_condition_exist](#schemaaction_condition_exist)|false|none|存在过滤|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[action_condition_not_exist](#schemaaction_condition_not_exist)|false|none|不存在过滤|



<!-- Generator: Widdershins v4.0.1 -->

<h1 id="bkn-metrics">BKN Metrics v0.1.0</h1>


<h1 id="bkn-metrics-default">Default</h1>

## 获取指标列表

`GET /api/bkn-backend/v1/knowledge-networks/{kn_id}/metrics`

<h3 id="获取指标列表-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|name_pattern|query|string|false|根据名称模糊查询，默认为空|
|sort|query|string|false|排序类型，默认是 update_time|
|direction|query|string|false|排序结果方向，可选 asc、desc。|
|offset|query|integer(int64)|false|开始响应的项目的偏移量	|
|limit|query|integer(int64)|false|每页最多可返回的项目数；|
|tag|query|string|false|根据标签精准查询，默认为空|
|branch|query|string|false|分支，不填则使用 main 分支|

#### Detailed descriptions

**direction**: 排序结果方向，可选 asc、desc。
默认 desc

**offset**: 开始响应的项目的偏移量	
范围需大于等于0，默认值0

**limit**: 每页最多可返回的项目数；
分页可选1-1000，-1表示不分页；
默认值10

#### Enumerated Values

|Parameter|Value|
|---|---|
|sort|update_time|
|sort|name|
|direction|asc|
|direction|desc|

> Example responses

> 200 Response

```json
{
  "entries": [
    {
      "id": "string",
      "kn_id": "string",
      "branch": "string",
      "name": "string",
      "comment": "string",
      "tags": [
        "string"
      ],
      "icon": "string",
      "color": "string",
      "unit_type": "numUnit",
      "unit": "none",
      "metric_type": "atomic",
      "scope_type": "object_type",
      "scope_ref": "string",
      "time_dimension": {
        "property": "string",
        "default_range_policy": "last_1h"
      },
      "calculation_formula": {
        "condition": null,
        "aggregation": {
          "property": "string",
          "aggr": "count_distinct"
        },
        "group_by": [
          {
            "property": "string",
            "description": "string"
          }
        ],
        "order_by": [
          {
            "property": "string",
            "direction": "asc"
          }
        ],
        "having": {
          "field": "__value",
          "operation": "==",
          "value": null
        }
      },
      "analysis_dimensions": [
        {
          "name": "string",
          "display_name": "string"
        }
      ]
    }
  ],
  "total_count": 0
}
```

<h3 id="获取指标列表-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[ListMetrics](#schemalistmetrics)|

<aside class="success">
This operation does not require authentication
</aside>

## 批量创建或概念检索指标

`POST /api/bkn-backend/v1/knowledge-networks/{kn_id}/metrics`

与对象类 `POST .../object-types` 一致：通过请求头 `x-http-method-override` 区分语义。
- `POST`：批量创建，请求体为 `ReqMetrics`（`entries` 数组）。
- `GET`：概念/条件检索（search_after 分页），请求体与对象类检索相同，复用 `FirstQueryWithSearchAfter` / `PageTurnQueryWithSearchAfter`（见 `object-type.yaml` 中 `override--get`）。

> Body parameter

```json
{}
```

<h3 id="批量创建或概念检索指标-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|x-http-method-override|header|string|true|重载请求头|
|branch|query|string|false|分支，不填则使用 main 分支|
|strict_mode|query|boolean|false|批量创建时是否严格校验依赖（scope、概念分组等），默认 true|
|body|body|[override](#schemaoverride)|true|none|

#### Enumerated Values

|Parameter|Value|
|---|---|
|x-http-method-override|POST|
|x-http-method-override|GET|

> Example responses

> 200 Response

```json
{
  "entries": [
    {
      "id": "string",
      "kn_id": "string",
      "branch": "string",
      "name": "string",
      "comment": "string",
      "tags": [
        "string"
      ],
      "icon": "string",
      "color": "string",
      "unit_type": "numUnit",
      "unit": "none",
      "metric_type": "atomic",
      "scope_type": "object_type",
      "scope_ref": "string",
      "time_dimension": {
        "property": "string",
        "default_range_policy": "last_1h"
      },
      "calculation_formula": {
        "condition": null,
        "aggregation": {
          "property": "string",
          "aggr": "count_distinct"
        },
        "group_by": [
          {
            "property": "string",
            "description": "string"
          }
        ],
        "order_by": [
          {
            "property": "string",
            "direction": "asc"
          }
        ],
        "having": {
          "field": "__value",
          "operation": "==",
          "value": null
        }
      },
      "analysis_dimensions": [
        {
          "name": "string",
          "display_name": "string"
        }
      ]
    }
  ],
  "total_count": 0,
  "search_after": [
    null
  ],
  "groups": [
    null
  ],
  "type": "string"
}
```

<h3 id="批量创建或概念检索指标-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|概念检索成功|[MetricSearchResponse](#schemametricsearchresponse)|
|201|[Created](https://tools.ietf.org/html/rfc7231#section-6.3.2)|批量新增成功|Inline|

<h3 id="批量创建或概念检索指标-responseschema">Response Schema</h3>

Status Code **201**

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|any|false|none|none|

<aside class="success">
This operation does not require authentication
</aside>

## 校验指标

`POST /api/bkn-backend/v1/knowledge-networks/{kn_id}/metrics/validation`

仅校验依赖存在性，不写库。语义与 `POST .../object-types/validation` 一致，用于导入前预检、批处理前自检。
**响应**：HTTP 200 时 body 中 `valid` 为 `true` 表示通过；为 `false` 时带 `detail`。

> Body parameter

```json
{
  "entries": [
    {
      "name": "string",
      "comment": "string",
      "tags": [
        "string"
      ],
      "icon": "string",
      "color": "string",
      "unit_type": "numUnit",
      "unit": "none",
      "metric_type": "atomic",
      "scope_type": "string",
      "scope_ref": "string",
      "time_dimension": {
        "property": "string",
        "default_range_policy": "last_1h"
      },
      "calculation_formula": {
        "condition": null,
        "aggregation": {
          "property": "string",
          "aggr": "count_distinct"
        },
        "group_by": [
          {
            "property": "string",
            "description": "string"
          }
        ],
        "order_by": [
          {
            "property": "string",
            "direction": "asc"
          }
        ],
        "having": {
          "field": "__value",
          "operation": "==",
          "value": null
        }
      },
      "analysis_dimensions": [
        {
          "name": "string",
          "display_name": "string"
        }
      ]
    }
  ]
}
```

<h3 id="校验指标-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|branch|query|string|false|分支，不填则使用 main 分支|
|strict_mode|query|boolean|false|是否严格校验依赖，默认为 true|
|import_mode|query|string|false|与批量创建语义一致；用于指标 ID/名称与落库冲突的校验语义（normal / ignore / overwrite）|
|body|body|object|true|none|
|» entries|body|[[CreateMetricRequest](#schemacreatemetricrequest)]|false|待校验的指标列表，结构与创建接口一致|
|»» name|body|string|true|none|
|»» comment|body|string|false|none|
|»» tags|body|[string]|false|none|
|»» icon|body|string|false|none|
|»» color|body|string|false|none|
|»» unit_type|body|[MetricUnitType](#schemametricunittype)|false|指标单位类型。取值须为下列枚举之一，与 bkn-backend `interfaces.ValidMetricUnitTypes` 校验一致。|
|»» unit|body|[MetricUnit](#schemametricunit)|false|指标度量单位。取值须为下列枚举之一，与 bkn-backend `interfaces.ValidMetricUnits` 校验一致。|
|»» metric_type|body|string|true|none|
|»» scope_type|body|string|true|none|
|»» scope_ref|body|string|true|none|
|»» time_dimension|body|[MetricTimeDimension](#schemametrictimedimension)|false|时间维度（DESIGN 附录 B.2）|
|»»» property|body|string|true|时间列或事件时间字段名（语义字段）|
|»»» default_range_policy|body|string|false|未传入 dynamic 时间时的默认策略；none 表示必须由请求显式给时间窗|
|»» calculation_formula|body|[MetricCalculationFormula](#schemametriccalculationformula)|true|指标计算公式，与 ontology-query Condition 同构的 condition（DESIGN 附录 B.1）|
|»»» condition|body|any|false|none|
|»»» aggregation|body|[MetricAggregation](#schemametricaggregation)|true|单一聚合（DESIGN 附录 B.1）|
|»»»» property|body|string|true|none|
|»»»» aggr|body|string|true|none|
|»»» group_by|body|[[MetricGroupBy](#schemametricgroupby)]|false|none|
|»»»» property|body|string|true|none|
|»»»» description|body|string|false|none|
|»»» order_by|body|[[MetricOrderBy](#schemametricorderby)]|false|none|
|»»»» property|body|string|true|none|
|»»»» direction|body|string|true|none|
|»»» having|body|[MetricHaving](#schemametrichaving)|false|对聚合结果的过滤（DESIGN 附录 B.1）|
|»»»» field|body|string|false|none|
|»»»» operation|body|string|false|none|
|»»»» value|body|any|false|none|
|»» analysis_dimensions|body|[[MetricAnalysisDimension](#schemametricanalysisdimension)]|false|[分析维度条目（DESIGN 附录 B.2）]|
|»»» name|body|string|true|none|
|»»» display_name|body|string|false|none|

#### Enumerated Values

|Parameter|Value|
|---|---|
|import_mode|normal|
|import_mode|ignore|
|import_mode|overwrite|
|»» unit_type|numUnit|
|»» unit_type|storeUnit|
|»» unit_type|percent|
|»» unit_type|transmissionRate|
|»» unit_type|timeUnit|
|»» unit_type|currencyUnit|
|»» unit_type|percentageUnit|
|»» unit_type|countUnit|
|»» unit_type|weightUnit|
|»» unit_type|ordinalRankUnit|
|»» unit|none|
|»» unit|K|
|»» unit|Mil|
|»» unit|Bil|
|»» unit|Tri|
|»» unit|bit|
|»» unit|Byte|
|»» unit|KB|
|»» unit|MB|
|»» unit|GB|
|»» unit|TB|
|»» unit|PB|
|»» unit|bps|
|»» unit|Kbps|
|»» unit|Mbps|
|»» unit|μs|
|»» unit|ms|
|»» unit|s|
|»» unit|m|
|»» unit|h|
|»» unit|day|
|»» unit|week|
|»» unit|month|
|»» unit|year|
|»» unit|quarter|
|»» unit|Fen|
|»» unit|Jiao|
|»» unit|CNY|
|»» unit|10K_CNY|
|»» unit|1M_CNY|
|»» unit|100M_CNY|
|»» unit|US_Cent|
|»» unit|USD|
|»» unit|EUR_Cent|
|»» unit|%|
|»» unit|‰|
|»» unit|household|
|»» unit|transaction|
|»» unit|piece|
|»» unit|item|
|»» unit|times|
|»» unit|man_day|
|»» unit|family|
|»» unit|hand|
|»» unit|sheet|
|»» unit|packet|
|»» unit|ton|
|»» unit|kg|
|»» unit|rank|
|»» metric_type|atomic|
|»» metric_type|derived|
|»» metric_type|composite|
|»»» default_range_policy|last_1h|
|»»» default_range_policy|last_24h|
|»»» default_range_policy|calendar_day|
|»»» default_range_policy|none|
|»»»» aggr|count_distinct|
|»»»» aggr|sum|
|»»»» aggr|max|
|»»»» aggr|min|
|»»»» aggr|avg|
|»»»» aggr|count|
|»»»» direction|asc|
|»»»» direction|desc|
|»»»» field|__value|
|»»»» operation|==|
|»»»» operation|!=|
|»»»» operation|>|
|»»»» operation|>=|
|»»»» operation|<|
|»»»» operation|<=|
|»»»» operation|in|
|»»»» operation|not_in|
|»»»» operation|range|
|»»»» operation|out_range|

> Example responses

> 200 Response

```json
{
  "valid": true,
  "detail": "string"
}
```

<h3 id="校验指标-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|已返回校验结果（通过与否均可能为 200）|Inline|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数错误等；业务校验「不通过」由 200 + valid:false + detail 表达|None|

<h3 id="校验指标-responseschema">Response Schema</h3>

Status Code **200**

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» valid|boolean|true|none|none|
|» detail|string|false|none|当 valid 为 false 时的说明|

<aside class="success">
This operation does not require authentication
</aside>

## 批量获取指标详情

`GET /api/bkn-backend/v1/knowledge-networks/{kn_id}/metrics/{metric_ids}`

<h3 id="批量获取指标详情-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|metric_ids|path|array[string]|true|指标ID列表|
|kn_id|path|string|true|业务知识网络ID|
|branch|query|string|false|分支，不填则使用 main 分支|

> Example responses

> 200 Response

```json
{
  "entries": [
    {
      "id": "string",
      "kn_id": "string",
      "branch": "string",
      "name": "string",
      "comment": "string",
      "tags": [
        "string"
      ],
      "icon": "string",
      "color": "string",
      "unit_type": "numUnit",
      "unit": "none",
      "metric_type": "atomic",
      "scope_type": "object_type",
      "scope_ref": "string",
      "time_dimension": {
        "property": "string",
        "default_range_policy": "last_1h"
      },
      "calculation_formula": {
        "condition": null,
        "aggregation": {
          "property": "string",
          "aggr": "count_distinct"
        },
        "group_by": [
          {
            "property": "string",
            "description": "string"
          }
        ],
        "order_by": [
          {
            "property": "string",
            "direction": "asc"
          }
        ],
        "having": {
          "field": "__value",
          "operation": "==",
          "value": null
        }
      },
      "analysis_dimensions": [
        {
          "name": "string",
          "display_name": "string"
        }
      ]
    }
  ]
}
```

<h3 id="批量获取指标详情-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[MetricDefinitions](#schemametricdefinitions)|

<aside class="success">
This operation does not require authentication
</aside>

## 更新指标

`PUT /api/bkn-backend/v1/knowledge-networks/{kn_id}/metrics/{metric_ids}`

更新指标定义。路由为 `metrics/{metric_ids}`（与批量获取 / 删除同槽）。需 `Content-Type: application/json`。

> Body parameter

```json
{
  "comment": "string",
  "tags": [
    "string"
  ],
  "icon": "string",
  "color": "string",
  "unit_type": "numUnit",
  "unit": "none",
  "metric_type": "atomic",
  "time_dimension": {
    "property": "string",
    "default_range_policy": "last_1h"
  },
  "calculation_formula": {
    "condition": null,
    "aggregation": {
      "property": "string",
      "aggr": "count_distinct"
    },
    "group_by": [
      {
        "property": "string",
        "description": "string"
      }
    ],
    "order_by": [
      {
        "property": "string",
        "direction": "asc"
      }
    ],
    "having": {
      "field": "__value",
      "operation": "==",
      "value": null
    }
  },
  "analysis_dimensions": [
    {
      "name": "string",
      "display_name": "string"
    }
  ]
}
```

<h3 id="更新指标-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|metric_ids|path|string|true|指标ID|
|branch|query|string|false|分支，不填则使用 main 分支|
|strict_mode|query|boolean|false|是否严格校验依赖，默认为 true|
|body|body|[UpdateMetricRequest](#schemaupdatemetricrequest)|true|none|

<h3 id="更新指标-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|ok|None|

<aside class="success">
This operation does not require authentication
</aside>

## 批量删除指标

`DELETE /api/bkn-backend/v1/knowledge-networks/{kn_id}/metrics/{metric_ids}`

<h3 id="批量删除指标-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|metric_ids|path|array[string]|true|指标ID列表|
|branch|query|string|false|分支，不填则使用 main 分支|

<h3 id="批量删除指标-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|ok|None|

<aside class="success">
This operation does not require authentication
</aside>

# Schemas

<h2 id="tocS_MetricUnitType">MetricUnitType</h2>
<!-- backwards compatibility -->
<a id="schemametricunittype"></a>
<a id="schema_MetricUnitType"></a>
<a id="tocSmetricunittype"></a>
<a id="tocsmetricunittype"></a>

```json
"numUnit"

```

指标单位类型。取值须为下列枚举之一，与 bkn-backend `interfaces.ValidMetricUnitTypes` 校验一致。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|string|false|none|指标单位类型。取值须为下列枚举之一，与 bkn-backend `interfaces.ValidMetricUnitTypes` 校验一致。|

#### Enumerated Values

|Property|Value|
|---|---|
|*anonymous*|numUnit|
|*anonymous*|storeUnit|
|*anonymous*|percent|
|*anonymous*|transmissionRate|
|*anonymous*|timeUnit|
|*anonymous*|currencyUnit|
|*anonymous*|percentageUnit|
|*anonymous*|countUnit|
|*anonymous*|weightUnit|
|*anonymous*|ordinalRankUnit|

<h2 id="tocS_MetricUnit">MetricUnit</h2>
<!-- backwards compatibility -->
<a id="schemametricunit"></a>
<a id="schema_MetricUnit"></a>
<a id="tocSmetricunit"></a>
<a id="tocsmetricunit"></a>

```json
"none"

```

指标度量单位。取值须为下列枚举之一，与 bkn-backend `interfaces.ValidMetricUnits` 校验一致。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|string|false|none|指标度量单位。取值须为下列枚举之一，与 bkn-backend `interfaces.ValidMetricUnits` 校验一致。|

#### Enumerated Values

|Property|Value|
|---|---|
|*anonymous*|none|
|*anonymous*|K|
|*anonymous*|Mil|
|*anonymous*|Bil|
|*anonymous*|Tri|
|*anonymous*|bit|
|*anonymous*|Byte|
|*anonymous*|KB|
|*anonymous*|MB|
|*anonymous*|GB|
|*anonymous*|TB|
|*anonymous*|PB|
|*anonymous*|bps|
|*anonymous*|Kbps|
|*anonymous*|Mbps|
|*anonymous*|μs|
|*anonymous*|ms|
|*anonymous*|s|
|*anonymous*|m|
|*anonymous*|h|
|*anonymous*|day|
|*anonymous*|week|
|*anonymous*|month|
|*anonymous*|year|
|*anonymous*|quarter|
|*anonymous*|Fen|
|*anonymous*|Jiao|
|*anonymous*|CNY|
|*anonymous*|10K_CNY|
|*anonymous*|1M_CNY|
|*anonymous*|100M_CNY|
|*anonymous*|US_Cent|
|*anonymous*|USD|
|*anonymous*|EUR_Cent|
|*anonymous*|%|
|*anonymous*|‰|
|*anonymous*|household|
|*anonymous*|transaction|
|*anonymous*|piece|
|*anonymous*|item|
|*anonymous*|times|
|*anonymous*|man_day|
|*anonymous*|family|
|*anonymous*|hand|
|*anonymous*|sheet|
|*anonymous*|packet|
|*anonymous*|ton|
|*anonymous*|kg|
|*anonymous*|rank|

<h2 id="tocS_ListMetrics">ListMetrics</h2>
<!-- backwards compatibility -->
<a id="schemalistmetrics"></a>
<a id="schema_ListMetrics"></a>
<a id="tocSlistmetrics"></a>
<a id="tocslistmetrics"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "kn_id": "string",
      "branch": "string",
      "name": "string",
      "comment": "string",
      "tags": [
        "string"
      ],
      "icon": "string",
      "color": "string",
      "unit_type": "numUnit",
      "unit": "none",
      "metric_type": "atomic",
      "scope_type": "object_type",
      "scope_ref": "string",
      "time_dimension": {
        "property": "string",
        "default_range_policy": "last_1h"
      },
      "calculation_formula": {
        "condition": null,
        "aggregation": {
          "property": "string",
          "aggr": "count_distinct"
        },
        "group_by": [
          {
            "property": "string",
            "description": "string"
          }
        ],
        "order_by": [
          {
            "property": "string",
            "direction": "asc"
          }
        ],
        "having": {
          "field": "__value",
          "operation": "==",
          "value": null
        }
      },
      "analysis_dimensions": [
        {
          "name": "string",
          "display_name": "string"
        }
      ]
    }
  ],
  "total_count": 0
}

```

指标列表（与 ListObjectTypes 同构）

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[MetricDefinition](#schemametricdefinition)]|true|none|条目列表|
|total_count|integer|true|none|总条数|

<h2 id="tocS_MetricDefinitions">MetricDefinitions</h2>
<!-- backwards compatibility -->
<a id="schemametricdefinitions"></a>
<a id="schema_MetricDefinitions"></a>
<a id="tocSmetricdefinitions"></a>
<a id="tocsmetricdefinitions"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "kn_id": "string",
      "branch": "string",
      "name": "string",
      "comment": "string",
      "tags": [
        "string"
      ],
      "icon": "string",
      "color": "string",
      "unit_type": "numUnit",
      "unit": "none",
      "metric_type": "atomic",
      "scope_type": "object_type",
      "scope_ref": "string",
      "time_dimension": {
        "property": "string",
        "default_range_policy": "last_1h"
      },
      "calculation_formula": {
        "condition": null,
        "aggregation": {
          "property": "string",
          "aggr": "count_distinct"
        },
        "group_by": [
          {
            "property": "string",
            "description": "string"
          }
        ],
        "order_by": [
          {
            "property": "string",
            "direction": "asc"
          }
        ],
        "having": {
          "field": "__value",
          "operation": "==",
          "value": null
        }
      },
      "analysis_dimensions": [
        {
          "name": "string",
          "display_name": "string"
        }
      ]
    }
  ]
}

```

指标详情批量返回（与 ObjectTypeDetails 同构）

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[MetricDefinition](#schemametricdefinition)]|true|none|指标数组|

<h2 id="tocS_ReqMetrics">ReqMetrics</h2>
<!-- backwards compatibility -->
<a id="schemareqmetrics"></a>
<a id="schema_ReqMetrics"></a>
<a id="tocSreqmetrics"></a>
<a id="tocsreqmetrics"></a>

```json
{
  "entries": [
    {
      "name": "string",
      "comment": "string",
      "tags": [
        "string"
      ],
      "icon": "string",
      "color": "string",
      "unit_type": "numUnit",
      "unit": "none",
      "metric_type": "atomic",
      "scope_type": "string",
      "scope_ref": "string",
      "time_dimension": {
        "property": "string",
        "default_range_policy": "last_1h"
      },
      "calculation_formula": {
        "condition": null,
        "aggregation": {
          "property": "string",
          "aggr": "count_distinct"
        },
        "group_by": [
          {
            "property": "string",
            "description": "string"
          }
        ],
        "order_by": [
          {
            "property": "string",
            "direction": "asc"
          }
        ],
        "having": {
          "field": "__value",
          "operation": "==",
          "value": null
        }
      },
      "analysis_dimensions": [
        {
          "name": "string",
          "display_name": "string"
        }
      ]
    }
  ]
}

```

批量创建指标请求体（与 ReqObjectTypes 同构）

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[CreateMetricRequest](#schemacreatemetricrequest)]|true|none|待创建的指标|

<h2 id="tocS_metric-override--get">metric-override--get</h2>
<!-- backwards compatibility -->
<a id="schemametric-override--get"></a>
<a id="schema_metric-override--get"></a>
<a id="tocSmetric-override--get"></a>
<a id="tocsmetric-override--get"></a>

```json
{}

```

指标概念检索请求体（与 override--get 同构，复用对象类检索 schema）

### Properties

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[./object-type.yamlFirstQueryWithSearchAfter](#schema./object-type.yamlfirstquerywithsearchafter)|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[./object-type.yamlPageTurnQueryWithSearchAfter](#schema./object-type.yamlpageturnquerywithsearchafter)|false|none|none|

<h2 id="tocS_override">override</h2>
<!-- backwards compatibility -->
<a id="schemaoverride"></a>
<a id="schema_override"></a>
<a id="tocSoverride"></a>
<a id="tocsoverride"></a>

```json
{}

```

post 重载批量创建、指标概念检索（与 object-types 的 override 同构）

### Properties

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|object|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[ReqMetrics](#schemareqmetrics)|false|none|批量创建指标请求体（与 ReqObjectTypes 同构）|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[metric-override--get](#schemametric-override--get)|false|none|指标概念检索请求体（与 override--get 同构，复用对象类检索 schema）|

<h2 id="tocS_MetricSearchResponse">MetricSearchResponse</h2>
<!-- backwards compatibility -->
<a id="schemametricsearchresponse"></a>
<a id="schema_MetricSearchResponse"></a>
<a id="tocSmetricsearchresponse"></a>
<a id="tocsmetricsearchresponse"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "kn_id": "string",
      "branch": "string",
      "name": "string",
      "comment": "string",
      "tags": [
        "string"
      ],
      "icon": "string",
      "color": "string",
      "unit_type": "numUnit",
      "unit": "none",
      "metric_type": "atomic",
      "scope_type": "object_type",
      "scope_ref": "string",
      "time_dimension": {
        "property": "string",
        "default_range_policy": "last_1h"
      },
      "calculation_formula": {
        "condition": null,
        "aggregation": {
          "property": "string",
          "aggr": "count_distinct"
        },
        "group_by": [
          {
            "property": "string",
            "description": "string"
          }
        ],
        "order_by": [
          {
            "property": "string",
            "direction": "asc"
          }
        ],
        "having": {
          "field": "__value",
          "operation": "==",
          "value": null
        }
      },
      "analysis_dimensions": [
        {
          "name": "string",
          "display_name": "string"
        }
      ]
    }
  ],
  "total_count": 0,
  "search_after": [
    null
  ],
  "groups": [
    null
  ],
  "type": "string"
}

```

指标概念检索返回结果（与 ObjectTypeSearchResponse 同构）

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[MetricDefinition](#schemametricdefinition)]|true|none|指标条目|
|total_count|integer|false|none|总条数|
|search_after|[any]|true|none|表示返回的最后一个文档的排序值，用于下一次 search_after 分页|
|groups|[any]|true|none|概念分组信息（与对象类检索一致）|
|type|string|true|none|资源类型标识|

<h2 id="tocS_MetricTimeDimension">MetricTimeDimension</h2>
<!-- backwards compatibility -->
<a id="schemametrictimedimension"></a>
<a id="schema_MetricTimeDimension"></a>
<a id="tocSmetrictimedimension"></a>
<a id="tocsmetrictimedimension"></a>

```json
{
  "property": "string",
  "default_range_policy": "last_1h"
}

```

时间维度（DESIGN 附录 B.2）

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|property|string|true|none|时间列或事件时间字段名（语义字段）|
|default_range_policy|string|false|none|未传入 dynamic 时间时的默认策略；none 表示必须由请求显式给时间窗|

#### Enumerated Values

|Property|Value|
|---|---|
|default_range_policy|last_1h|
|default_range_policy|last_24h|
|default_range_policy|calendar_day|
|default_range_policy|none|

<h2 id="tocS_MetricAggregation">MetricAggregation</h2>
<!-- backwards compatibility -->
<a id="schemametricaggregation"></a>
<a id="schema_MetricAggregation"></a>
<a id="tocSmetricaggregation"></a>
<a id="tocsmetricaggregation"></a>

```json
{
  "property": "string",
  "aggr": "count_distinct"
}

```

单一聚合（DESIGN 附录 B.1）

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|property|string|true|none|none|
|aggr|string|true|none|none|

#### Enumerated Values

|Property|Value|
|---|---|
|aggr|count_distinct|
|aggr|sum|
|aggr|max|
|aggr|min|
|aggr|avg|
|aggr|count|

<h2 id="tocS_MetricGroupBy">MetricGroupBy</h2>
<!-- backwards compatibility -->
<a id="schemametricgroupby"></a>
<a id="schema_MetricGroupBy"></a>
<a id="tocSmetricgroupby"></a>
<a id="tocsmetricgroupby"></a>

```json
{
  "property": "string",
  "description": "string"
}

```

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|property|string|true|none|none|
|description|string|false|none|none|

<h2 id="tocS_MetricOrderBy">MetricOrderBy</h2>
<!-- backwards compatibility -->
<a id="schemametricorderby"></a>
<a id="schema_MetricOrderBy"></a>
<a id="tocSmetricorderby"></a>
<a id="tocsmetricorderby"></a>

```json
{
  "property": "string",
  "direction": "asc"
}

```

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|property|string|true|none|none|
|direction|string|true|none|none|

#### Enumerated Values

|Property|Value|
|---|---|
|direction|asc|
|direction|desc|

<h2 id="tocS_MetricHaving">MetricHaving</h2>
<!-- backwards compatibility -->
<a id="schemametrichaving"></a>
<a id="schema_MetricHaving"></a>
<a id="tocSmetrichaving"></a>
<a id="tocsmetrichaving"></a>

```json
{
  "field": "__value",
  "operation": "==",
  "value": null
}

```

对聚合结果的过滤（DESIGN 附录 B.1）

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|false|none|none|
|operation|string|false|none|none|
|value|any|false|none|none|

#### Enumerated Values

|Property|Value|
|---|---|
|field|__value|
|operation|==|
|operation|!=|
|operation|>|
|operation|>=|
|operation|<|
|operation|<=|
|operation|in|
|operation|not_in|
|operation|range|
|operation|out_range|

<h2 id="tocS_MetricCalculationFormula">MetricCalculationFormula</h2>
<!-- backwards compatibility -->
<a id="schemametriccalculationformula"></a>
<a id="schema_MetricCalculationFormula"></a>
<a id="tocSmetriccalculationformula"></a>
<a id="tocsmetriccalculationformula"></a>

```json
{
  "condition": null,
  "aggregation": {
    "property": "string",
    "aggr": "count_distinct"
  },
  "group_by": [
    {
      "property": "string",
      "description": "string"
    }
  ],
  "order_by": [
    {
      "property": "string",
      "direction": "asc"
    }
  ],
  "having": {
    "field": "__value",
    "operation": "==",
    "value": null
  }
}

```

指标计算公式，与 ontology-query Condition 同构的 condition（DESIGN 附录 B.1）

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|condition|[../ontology-query/ontology-query.yamlCondition](#schema../ontology-query/ontology-query.yamlcondition)|false|none|none|
|aggregation|[MetricAggregation](#schemametricaggregation)|true|none|单一聚合（DESIGN 附录 B.1）|
|group_by|[[MetricGroupBy](#schemametricgroupby)]|false|none|none|
|order_by|[[MetricOrderBy](#schemametricorderby)]|false|none|none|
|having|[MetricHaving](#schemametrichaving)|false|none|对聚合结果的过滤（DESIGN 附录 B.1）|

<h2 id="tocS_MetricAnalysisDimension">MetricAnalysisDimension</h2>
<!-- backwards compatibility -->
<a id="schemametricanalysisdimension"></a>
<a id="schema_MetricAnalysisDimension"></a>
<a id="tocSmetricanalysisdimension"></a>
<a id="tocsmetricanalysisdimension"></a>

```json
{
  "name": "string",
  "display_name": "string"
}

```

分析维度条目（DESIGN 附录 B.2）

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|none|
|display_name|string|false|none|none|

<h2 id="tocS_MetricDefinition">MetricDefinition</h2>
<!-- backwards compatibility -->
<a id="schemametricdefinition"></a>
<a id="schema_MetricDefinition"></a>
<a id="tocSmetricdefinition"></a>
<a id="tocsmetricdefinition"></a>

```json
{
  "id": "string",
  "kn_id": "string",
  "branch": "string",
  "name": "string",
  "comment": "string",
  "tags": [
    "string"
  ],
  "icon": "string",
  "color": "string",
  "unit_type": "numUnit",
  "unit": "none",
  "metric_type": "atomic",
  "scope_type": "object_type",
  "scope_ref": "string",
  "time_dimension": {
    "property": "string",
    "default_range_policy": "last_1h"
  },
  "calculation_formula": {
    "condition": null,
    "aggregation": {
      "property": "string",
      "aggr": "count_distinct"
    },
    "group_by": [
      {
        "property": "string",
        "description": "string"
      }
    ],
    "order_by": [
      {
        "property": "string",
        "direction": "asc"
      }
    ],
    "having": {
      "field": "__value",
      "operation": "==",
      "value": null
    }
  },
  "analysis_dimensions": [
    {
      "name": "string",
      "display_name": "string"
    }
  ]
}

```

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|none|
|kn_id|string|true|none|none|
|branch|string|true|none|none|
|name|string|true|none|none|
|comment|string|false|none|none|
|tags|[string]|false|none|标签（与对象类 / 关系类展示一致）|
|icon|string|false|none|展示用图标标识|
|color|string|false|none|展示用颜色（语义色或色值，长度与库表 f_color 一致）|
|unit_type|[MetricUnitType](#schemametricunittype)|false|none|指标单位类型。取值须为下列枚举之一，与 bkn-backend `interfaces.ValidMetricUnitTypes` 校验一致。|
|unit|[MetricUnit](#schemametricunit)|false|none|指标度量单位。取值须为下列枚举之一，与 bkn-backend `interfaces.ValidMetricUnits` 校验一致。|
|metric_type|string|true|none|指标类型；当前仅 atomic 允许写入|
|scope_type|string|true|none|none|
|scope_ref|string|true|none|none|
|time_dimension|[MetricTimeDimension](#schemametrictimedimension)|false|none|时间维度（DESIGN 附录 B.2）|
|calculation_formula|[MetricCalculationFormula](#schemametriccalculationformula)|true|none|指标计算公式，与 ontology-query Condition 同构的 condition（DESIGN 附录 B.1）|
|analysis_dimensions|[[MetricAnalysisDimension](#schemametricanalysisdimension)]|false|none|[分析维度条目（DESIGN 附录 B.2）]|

#### Enumerated Values

|Property|Value|
|---|---|
|metric_type|atomic|
|metric_type|derived|
|metric_type|composite|
|scope_type|object_type|
|scope_type|subgraph|

<h2 id="tocS_CreateMetricRequest">CreateMetricRequest</h2>
<!-- backwards compatibility -->
<a id="schemacreatemetricrequest"></a>
<a id="schema_CreateMetricRequest"></a>
<a id="tocScreatemetricrequest"></a>
<a id="tocscreatemetricrequest"></a>

```json
{
  "name": "string",
  "comment": "string",
  "tags": [
    "string"
  ],
  "icon": "string",
  "color": "string",
  "unit_type": "numUnit",
  "unit": "none",
  "metric_type": "atomic",
  "scope_type": "string",
  "scope_ref": "string",
  "time_dimension": {
    "property": "string",
    "default_range_policy": "last_1h"
  },
  "calculation_formula": {
    "condition": null,
    "aggregation": {
      "property": "string",
      "aggr": "count_distinct"
    },
    "group_by": [
      {
        "property": "string",
        "description": "string"
      }
    ],
    "order_by": [
      {
        "property": "string",
        "direction": "asc"
      }
    ],
    "having": {
      "field": "__value",
      "operation": "==",
      "value": null
    }
  },
  "analysis_dimensions": [
    {
      "name": "string",
      "display_name": "string"
    }
  ]
}

```

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|none|
|comment|string|false|none|none|
|tags|[string]|false|none|none|
|icon|string|false|none|none|
|color|string|false|none|none|
|unit_type|[MetricUnitType](#schemametricunittype)|false|none|指标单位类型。取值须为下列枚举之一，与 bkn-backend `interfaces.ValidMetricUnitTypes` 校验一致。|
|unit|[MetricUnit](#schemametricunit)|false|none|指标度量单位。取值须为下列枚举之一，与 bkn-backend `interfaces.ValidMetricUnits` 校验一致。|
|metric_type|string|true|none|none|
|scope_type|string|true|none|none|
|scope_ref|string|true|none|none|
|time_dimension|[MetricTimeDimension](#schemametrictimedimension)|false|none|时间维度（DESIGN 附录 B.2）|
|calculation_formula|[MetricCalculationFormula](#schemametriccalculationformula)|true|none|指标计算公式，与 ontology-query Condition 同构的 condition（DESIGN 附录 B.1）|
|analysis_dimensions|[[MetricAnalysisDimension](#schemametricanalysisdimension)]|false|none|[分析维度条目（DESIGN 附录 B.2）]|

#### Enumerated Values

|Property|Value|
|---|---|
|metric_type|atomic|
|metric_type|derived|
|metric_type|composite|

<h2 id="tocS_UpdateMetricRequest">UpdateMetricRequest</h2>
<!-- backwards compatibility -->
<a id="schemaupdatemetricrequest"></a>
<a id="schema_UpdateMetricRequest"></a>
<a id="tocSupdatemetricrequest"></a>
<a id="tocsupdatemetricrequest"></a>

```json
{
  "comment": "string",
  "tags": [
    "string"
  ],
  "icon": "string",
  "color": "string",
  "unit_type": "numUnit",
  "unit": "none",
  "metric_type": "atomic",
  "time_dimension": {
    "property": "string",
    "default_range_policy": "last_1h"
  },
  "calculation_formula": {
    "condition": null,
    "aggregation": {
      "property": "string",
      "aggr": "count_distinct"
    },
    "group_by": [
      {
        "property": "string",
        "description": "string"
      }
    ],
    "order_by": [
      {
        "property": "string",
        "direction": "asc"
      }
    ],
    "having": {
      "field": "__value",
      "operation": "==",
      "value": null
    }
  },
  "analysis_dimensions": [
    {
      "name": "string",
      "display_name": "string"
    }
  ]
}

```

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|comment|string|false|none|none|
|tags|[string]|false|none|none|
|icon|string|false|none|none|
|color|string|false|none|none|
|unit_type|[MetricUnitType](#schemametricunittype)|false|none|指标单位类型。取值须为下列枚举之一，与 bkn-backend `interfaces.ValidMetricUnitTypes` 校验一致。|
|unit|[MetricUnit](#schemametricunit)|false|none|指标度量单位。取值须为下列枚举之一，与 bkn-backend `interfaces.ValidMetricUnits` 校验一致。|
|metric_type|string|false|none|none|
|time_dimension|[MetricTimeDimension](#schemametrictimedimension)|false|none|时间维度（DESIGN 附录 B.2）|
|calculation_formula|[MetricCalculationFormula](#schemametriccalculationformula)|false|none|指标计算公式，与 ontology-query Condition 同构的 condition（DESIGN 附录 B.1）|
|analysis_dimensions|[[MetricAnalysisDimension](#schemametricanalysisdimension)]|false|none|[分析维度条目（DESIGN 附录 B.2）]|

#### Enumerated Values

|Property|Value|
|---|---|
|metric_type|atomic|
|metric_type|derived|
|metric_type|composite|



<!-- Generator: Widdershins v4.0.1 -->

<h1 id="bkn-backend-api">BKN Backend API v1.0.0</h1>


BKN (Business Knowledge Network) Backend API 提供了业务知识网络的导入导出功能。

Base URLs:

* <a href="http://localhost:13014">http://localhost:13014</a>

# Authentication

- oAuth2 authentication. OAuth2 认证，用于外网接口

    - Flow: clientCredentials

    - Token URL = [/oauth/token](/oauth/token)

|Scope|Scope Description|
|---|---|

<h1 id="bkn-backend-api-bkn-import-export">BKN Import/Export</h1>

BKN 导入导出接口

## 上传 BKN tar 包并导入

<a id="opIduploadBKN"></a>

`POST /api/bkn-backend/v1/bkns`

上传 BKN tar 包文件并导入为业务知识网络，tar 包内容格式参考 docs/design/bkn/features/bkn_docs/SPECIFICATION.md

> Body parameter

```yaml
file: string

```

<h3 id="上传-bkn-tar-包并导入-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|branch|query|string|false|分支名称，默认为 main|
|X-Business-Domain|header|string|true|业务域|
|body|body|object|true|none|
|» file|body|string(binary)|false|BKN tar 包文件|

> Example responses

> 200 Response

```json
{
  "kn_id": "string"
}
```

<h3 id="上传-bkn-tar-包并导入-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|导入成功|Inline|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数错误|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权|None|

<h3 id="上传-bkn-tar-包并导入-responseschema">Response Schema</h3>

Status Code **200**

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» kn_id|string|false|none|创建的知识网络 ID|

<aside class="warning">
To perform this operation, you must be authenticated by means of one of the following methods:
OAuth2
</aside>

## 下载 BKN tar 包

<a id="opIddownloadBKN"></a>

`GET /api/bkn-backend/v1/bkns/{kn_id}`

导出业务知识网络为 BKN tar 包文件，tar 包内容格式参考 docs/design/bkn/features/bkn_docs/SPECIFICATION.md

<h3 id="下载-bkn-tar-包-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|知识网络 ID|
|branch|query|string|false|分支名称，默认为 main|

> Example responses

> 200 Response

<h3 id="下载-bkn-tar-包-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|导出成功|string|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数错误|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|知识网络不存在|None|

<aside class="warning">
To perform this operation, you must be authenticated by means of one of the following methods:
OAuth2
</aside>

<h1 id="bkn-backend-api-bkn">BKN</h1>

## 按 ID 批量取知识网络名称

<a id="opIdqueryKNNamesByIDs"></a>

`POST /api/bkn-backend/v1/knowledge-networks/names`

按 ID 批量回显知识网络名称（用于对象级授权页等）。不存在的 ID 略过，不报错。需 `Content-Type: application/json`。

> Body parameter

```json
{
  "ids": [
    "string"
  ]
}
```

<h3 id="按-id-批量取知识网络名称-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|body|body|object|true|none|
|» ids|body|[string]|false|待取名的知识网络 ID 列表，空列表返回空 entries|

> Example responses

> 200 Response

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string"
    }
  ]
}
```

<h3 id="按-id-批量取知识网络名称-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|Inline|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数错误|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权|None|

<h3 id="按-id-批量取知识网络名称-responseschema">Response Schema</h3>

Status Code **200**

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» entries|[object]|false|none|none|
|»» id|string|false|none|none|
|»» name|string|false|none|none|

<aside class="warning">
To perform this operation, you must be authenticated by means of one of the following methods:
OAuth2
</aside>

## 列出资源

<a id="opIdlistResources"></a>

`GET /api/bkn-backend/v1/resources`

按资源类型分页列出资源（统一资源平台）。当前支持 `resource_type=kn`（知识网络）。

<h3 id="列出资源-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|resource_type|query|string|true|资源类型，如 `kn`|
|keyword|query|string|false|名称关键字过滤，默认空|
|offset|query|integer(int64)|false|偏移量，默认 0|
|limit|query|integer(int64)|false|每页条数，默认 10|

> Example responses

> 200 Response

```json
{
  "entries": [
    {
      "type": "string",
      "id": "string",
      "name": "string"
    }
  ],
  "total_count": 0
}
```

<h3 id="列出资源-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|Inline|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数错误|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权|None|

<h3 id="列出资源-responseschema">Response Schema</h3>

Status Code **200**

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» entries|[object]|false|none|none|
|»» type|string|false|none|none|
|»» id|string|false|none|none|
|»» name|string|false|none|none|
|» total_count|integer(int64)|false|none|none|

<aside class="warning">
To perform this operation, you must be authenticated by means of one of the following methods:
OAuth2
</aside>



<!-- Generator: Widdershins v4.0.1 -->

<h1 id="businessknowledgenetwork">BusinessKnowledgeNetwork v0.1.0</h1>


<h1 id="businessknowledgenetwork-default">Default</h1>

## 获取业务知识网络列表

`GET /api/bkn-backend/v1/knowledge-networks`

<h3 id="获取业务知识网络列表-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|name_pattern|query|string|false|根据业务知识网络名称模糊查询，默认为空|
|sort|query|string|false|排序类型，默认是update_time|
|direction|query|string|false|排序结果方向，可选asc、desc。|
|offset|query|integer(int64)|false|开始响应的项目的偏移量	|
|limit|query|integer(int64)|false|每页最多可返回的项目数；|
|tag|query|string|false|根据标签精准查询，默认为空.|

#### Detailed descriptions

**direction**: 排序结果方向，可选asc、desc。
默认desc

**offset**: 开始响应的项目的偏移量	
范围需大于等于0，默认值0

**limit**: 每页最多可返回的项目数；
分页可选1-1000，-1表示不分页；
默认值10

#### Enumerated Values

|Parameter|Value|
|---|---|
|sort|update_time|
|sort|name|
|direction|asc|
|direction|desc|

> Example responses

> 200 Response

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string",
      "statistics": {
        "object_types_total": 0,
        "relation_types_total": 0,
        "action_types_total": 0
      },
      "concept_groups": [
        {
          "id": "string",
          "name": "string",
          "tags": [
            "string"
          ],
          "comment": "string",
          "icon": "string",
          "color": "string",
          "kn_id": "string",
          "branch": "string",
          "creator": "string",
          "create_time": 0,
          "updator": "string",
          "update_time": 0,
          "detail": "string"
        }
      ],
      "object_types": [
        {
          "concept_type": "object_type",
          "id": "string",
          "name": "string",
          "tags": [
            "string"
          ],
          "comment": "string",
          "icon": "string",
          "color": "string",
          "branch": "string",
          "kn_id": "string",
          "concept_groups": [
            {
              "id": "string",
              "name": "string"
            }
          ],
          "data_source": {
            "type": "data_view",
            "id": "string",
            "name": "string"
          },
          "data_properties": [
            {
              "name": "string",
              "display_name": "string",
              "type": "string",
              "comment": "string",
              "mapped_field": "string",
              "index": true,
              "fulltext_config": {
                "analyzer": "standard",
                "field_keyword": true
              },
              "vector_config": {
                "dimension": 0
              }
            }
          ],
          "logic_properties": [
            {
              "name": "string",
              "display_name": "string",
              "type": "string",
              "comment": "string",
              "index": true,
              "data_source": {
                "type": "data_view",
                "id": "string",
                "name": "string"
              },
              "parameters": [
                {
                  "name": "string",
                  "value_from": "property",
                  "value": "string"
                }
              ]
            }
          ],
          "primary_keys": [
            "string"
          ],
          "display_key": "string",
          "creator": "string",
          "create_time": 0,
          "updater": "string",
          "update_time": 0,
          "detail": "string"
        }
      ],
      "relation_types": [
        {
          "concept_type": "relation_type",
          "id": "string",
          "name": "string",
          "tags": [
            "string"
          ],
          "comment": "string",
          "icon": "string",
          "color": "string",
          "branch": "string",
          "kn_id": "string",
          "concept_groups": [
            {
              "id": "string",
              "name": "string"
            }
          ],
          "source_object_type_id": "string",
          "source_object_type_name": "string",
          "target_object_type_id": "string",
          "target_object_type_name": "string",
          "type": "direct",
          "mapping_rules": {},
          "creator": "string",
          "create_time": 0,
          "updater": "string",
          "update_time": 0,
          "detail": "string"
        }
      ],
      "action_types": [
        {
          "concept_type": "action_type",
          "id": "string",
          "name": "string",
          "tags": [
            "string"
          ],
          "comment": "string",
          "icon": "string",
          "color": "string",
          "branch": "string",
          "kn_id": "string",
          "concept_groups": [
            {
              "id": "string",
              "name": "string"
            }
          ],
          "action_type": "add",
          "action_intent": "add",
          "object_type_id": "string",
          "object_type_name": "string",
          "condition": {
            "object_type_id": "string",
            "field": "string",
            "operation": "and",
            "sub_conditions": [
              {}
            ],
            "value": null,
            "value_from": "const"
          },
          "affect": {
            "object_type": "string",
            "comment": "string",
            "expected_operation": "add",
            "affected_fields": [
              "string"
            ]
          },
          "impact_contracts": [
            {
              "object_type_id": "string",
              "expected_operation": "add",
              "description": "string",
              "affected_fields": [
                "string"
              ]
            }
          ],
          "action_source": {
            "type": "data_view",
            "id": "string",
            "name": "string"
          },
          "parameters": [
            {
              "name": "string",
              "value_from": "property",
              "value": "string"
            }
          ],
          "schedule": {
            "type": "FIX_RATE",
            "expression": "string"
          },
          "detail": "string"
        }
      ]
    }
  ],
  "total_count": 0
}
```

<h3 id="获取业务知识网络列表-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[ListKN](#schemalistkn)|

<aside class="success">
This operation does not require authentication
</aside>

## 创建业务知识网路

`POST /api/bkn-backend/v1/knowledge-networks`

> Body parameter

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string"
}
```

<h3 id="创建业务知识网路-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|import_mode|query|string|false|导入模式，可选normal、ignore、overwrite，默认为normal|
|strict_mode|query|boolean|false|是否严格校验依赖，默认为true。为true时，需校验对象类的视图、关系类的视图等依赖是否存在；为false时，依赖不存在不报错|
|validate_dependency|query|boolean|false|[已废弃] 请使用 strict_mode。兼容保留，strict_mode 为空时会读取此参数|
|body|body|[ReqKnowledgeNetwork](#schemareqknowledgenetwork)|true|none|

#### Enumerated Values

|Parameter|Value|
|---|---|
|import_mode|normal|
|import_mode|ignore|
|import_mode|overwrite|

> Example responses

> 201 Response

```json
[
  {
    "id": "string"
  }
]
```

<h3 id="创建业务知识网路-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|201|[Created](https://tools.ietf.org/html/rfc7231#section-6.3.2)|创建成功|Inline|

<h3 id="创建业务知识网路-responseschema">Response Schema</h3>

Status Code **201**

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[[ID](#schemaid)]|false|none|[id]|
|» id|string|true|none|id|

<aside class="success">
This operation does not require authentication
</aside>

## 获取业务知识网络详情

`GET /api/bkn-backend/v1/knowledge-networks/{kn_id}`

获取本体模型详情，按需包含说明书内容

<h3 id="获取业务知识网络详情-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|mode|query|string|false|查询模式，当前支持export。默认是只查知识网络的详情，不返回包含的子类。|
|include_statistics|query|boolean|false|是否包含业务知识网络下的概念的统计信息。默认false|
|kn_id|path|string|true|业务知识网络ID|

#### Enumerated Values

|Parameter|Value|
|---|---|
|mode||
|mode|export|

> Example responses

> ok

```json
{
  "id": "kn_system_incident_event_network",
  "name": "DIP系统故障事件网络",
  "tags": [
    "事件",
    "故障"
  ],
  "comment": "DIP系统故障事件网络。。。。。。。。。。。。",
  "icon": "",
  "color": "",
  "branch": "main",
  "creator": "a0f02238-6cec-11f0-82bb-fa1c4529a151",
  "create_time": 1757583140948,
  "updater": "a0f02238-6cec-11f0-82bb-fa1c4529a151",
  "update_time": 1757583140948,
  "detail": "markdown文本"
}
```

```json
{
  "id": "kn_system_incident_event_network",
  "name": "DIP系统故障事件网络",
  "tags": [
    "事件",
    "故障"
  ],
  "comment": "DIP系统故障事件网络。。。。。。。。。。。。",
  "icon": "",
  "color": "",
  "branch": "main",
  "creator": "a0f02238-6cec-11f0-82bb-fa1c4529a151",
  "create_time": 1757583140948,
  "updater": "a0f02238-6cec-11f0-82bb-fa1c4529a151",
  "update_time": 1757583140948
}
```

<h3 id="获取业务知识网络详情-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[KnowledgeNetworkDetail](#schemaknowledgenetworkdetail)|

<aside class="success">
This operation does not require authentication
</aside>

## 修改业务知识网路

`PUT /api/bkn-backend/v1/knowledge-networks/{kn_id}`

> Body parameter

```json
{
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string"
}
```

<h3 id="修改业务知识网路-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|branch|query|string|false|分支，不填则使用 main 分支|
|strict_mode|query|boolean|false|是否严格校验依赖，默认为 true。为 true 时，对请求体中嵌套的对象类/关系类/行动类/概念分组等做依赖存在性校验；为 false 时不做该校验|
|validate_dependency|query|boolean|false|[已废弃] 请使用 strict_mode。兼容保留，strict_mode 为空时会读取此参数|
|body|body|[UpdateKnowledgeNetwork](#schemaupdateknowledgenetwork)|true|none|
|kn_id|path|string|true|业务知识网络ID|

<h3 id="修改业务知识网路-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|修改成功|None|

<aside class="success">
This operation does not require authentication
</aside>

## 删除业务知识网络

`DELETE /api/bkn-backend/v1/knowledge-networks/{kn_id}`

删除业务知识网络，会把其下的对象类、关系类、行动类和概念分组都删除

<h3 id="删除业务知识网络-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|

<h3 id="删除业务知识网络-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|删除成功|None|

<aside class="success">
This operation does not require authentication
</aside>

## 校验业务知识网络

`POST /api/bkn-backend/v1/knowledge-networks/{kn_id}/validation`

仅校验知识网络整体依赖存在性，不写库。校验对象类、关系类、行动类、概念分组等全部依赖。
服务端从请求体顶层四桶与各 concept_groups 嵌套桶合并构建 BatchIDIndex，整次预检共用该索引，
以便仅出现在嵌套分组内的 OT 仍可作为顶层或其它桶内 RT/AT 的引用目标（与 CreateKN 同批语义对齐）。

**响应**：HTTP 200 表示请求已处理完成。响应体 `valid` 为 `true` 表示校验通过；为 `false` 表示校验未通过或服务端在校验过程中返回错误，原因见 `detail`（为服务端错误的 `Error()` 字符串，可能为结构化错误 JSON）。  
请求体无法解析、`strict_mode` 非法、知识网络不存在或无权限等仍返回非 2xx（如 400/403），不返回上述 `valid`/`detail` 包。

**内部接口**：与上述请求/响应一致，路径为 `POST /api/bkn-backend/in/v1/knowledge-networks/{kn_id}/validation` 或 `POST /api/ontology-manager/in/v1/knowledge-networks/{kn_id}/validation`；访问者从 Header 解析，不经过 OAuth。

> Body parameter

```json
{
  "object_types": [],
  "relation_types": [],
  "action_types": [],
  "concept_groups": []
}
```

<h3 id="校验业务知识网络-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|branch|query|string|false|分支，不填则使用 main 分支|
|strict_mode|query|boolean|false|是否严格校验依赖，默认为 true|
|import_mode|query|string|false|与创建接口一致。导入模式；未传且未传 mode 时视为 normal。用于与落库相同的 ID/名称冲突语义（normal 报错、ignore 跳过冲突项、overwrite 按覆盖规则校验）。|
|body|body|[ValidateKNRequestBody](#schemavalidateknrequestbody)|true|none|

#### Enumerated Values

|Parameter|Value|
|---|---|
|import_mode|normal|
|import_mode|ignore|
|import_mode|overwrite|

> Example responses

> 200 Response

```json
{
  "valid": true,
  "detail": "string"
}
```

<h3 id="校验业务知识网络-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|已返回校验结果（通过与否均可能为 200）|Inline|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数错误（如请求体非法、strict_mode 无法解析、import_mode/mode 非法等），非业务校验结论|None|

<h3 id="校验业务知识网络-responseschema">Response Schema</h3>

Status Code **200**

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» valid|boolean|true|none|为 true 表示校验通过；为 false 表示未通过或处理校验时发生错误|
|» detail|string|false|none|当 valid 为 false 时给出说明（error.Error()）|

<aside class="success">
This operation does not require authentication
</aside>

# Schemas

<h2 id="tocS_ValidateKNRequestBody">ValidateKNRequestBody</h2>
<!-- backwards compatibility -->
<a id="schemavalidateknrequestbody"></a>
<a id="schema_ValidateKNRequestBody"></a>
<a id="tocSvalidateknrequestbody"></a>
<a id="tocsvalidateknrequestbody"></a>

```json
{
  "object_types": [],
  "relation_types": [],
  "action_types": [],
  "concept_groups": []
}

```

知识网络结构，包含 object_types、relation_types、action_types、concept_groups 等子概念

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_types|array|false|none|none|
|relation_types|array|false|none|none|
|action_types|array|false|none|none|
|concept_groups|array|false|none|none|

<h2 id="tocS_BasicInfo">BasicInfo</h2>
<!-- backwards compatibility -->
<a id="schemabasicinfo"></a>
<a id="schema_BasicInfo"></a>
<a id="tocSbasicinfo"></a>
<a id="tocsbasicinfo"></a>

```json
{
  "id": "string",
  "name": "string"
}

```

资源的基本信息，包含id和名称

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|资源ID|
|name|string|true|none|资源名称|

<h2 id="tocS_ConceptCondition">ConceptCondition</h2>
<!-- backwards compatibility -->
<a id="schemaconceptcondition"></a>
<a id="schema_ConceptCondition"></a>
<a id="tocSconceptcondition"></a>
<a id="tocsconceptcondition"></a>

```json
{
  "field": "id",
  "operation": "and",
  "sub_conditions": [
    {
      "field": "id",
      "operation": "and",
      "sub_conditions": [],
      "value": null,
      "value_from": "const"
    }
  ],
  "value": null,
  "value_from": "const"
}

```

概念查询数据条件。可用于过滤的字段有类名、属性名和描述

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|false|none|字段名称|
|operation|string|true|none|操作符。<br><br>knn: 未对原文进行向量化的向量过滤，接收的值是数组，第一个值，过滤内容，第二个值为 int,是邻居搜索时返回的邻居个数。<br><br>knn_vector: 对原文进行向量化后的向量过滤，接收的值是数组，第一个值，向量，第二个值为 int,是邻居搜索时返回的邻居个数。|
|sub_conditions|[[ConceptCondition](#schemaconceptcondition)]|false|none|子过滤条件|
|value|any|false|none|字段值|
|value_from|string|false|none|字段值来源，当前仅支持 "const"|

#### Enumerated Values

|Property|Value|
|---|---|
|field|id|
|field|name|
|field|property_name|
|field|property_display_name|
|field|comment|
|field|*|
|operation|and|
|operation|or|
|operation|==|
|operation|!=|
|operation|in|
|operation|not_in|
|operation|like|
|operation|not_like|
|operation|regex|
|operation|match|
|operation|match_phrase|
|operation|knn|
|operation|knn_vector|
|value_from|const|

<h2 id="tocS_ConceptTypeQueryBody">ConceptTypeQueryBody</h2>
<!-- backwards compatibility -->
<a id="schemaconcepttypequerybody"></a>
<a id="schema_ConceptTypeQueryBody"></a>
<a id="tocSconcepttypequerybody"></a>
<a id="tocsconcepttypequerybody"></a>

```json
{
  "concept_groups": [
    "string"
  ],
  "concept_type": "object_type",
  "condition": {
    "field": "id",
    "operation": "and",
    "sub_conditions": [
      {}
    ],
    "value": null,
    "value_from": "const"
  }
}

```

对象类、关系类、行动类的查询请求体

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|concept_groups|[string]|false|none|概念分组ID数组，可选|
|concept_type|string|false|none|概念类型，可选。查询的是对象类、关系类还是行动类，为空时查全部|
|condition|[ConceptCondition](#schemaconceptcondition)|true|none|过滤条件|

#### Enumerated Values

|Property|Value|
|---|---|
|concept_type|object_type|
|concept_type|relation_type|
|concept_type|action_type|

<h2 id="tocS_ConceptTypeResponse">ConceptTypeResponse</h2>
<!-- backwards compatibility -->
<a id="schemaconcepttyperesponse"></a>
<a id="schema_ConceptTypeResponse"></a>
<a id="tocSconcepttyperesponse"></a>
<a id="tocsconcepttyperesponse"></a>

```json
{
  "concept_type": "object_type",
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "groups": [
    "string"
  ]
}

```

对象类、关系类、行动类的查询返回结构

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|concept_type|string|true|none|概念类型|
|id|string|true|none|概念id|
|name|string|true|none|概念名称|
|tags|[string]|true|none|标签|
|groups|[string]|true|none|所属概念分组ID|

#### Enumerated Values

|Property|Value|
|---|---|
|concept_type|object_type|
|concept_type|relation_type|
|concept_type|action_type|

<h2 id="tocS_Path">Path</h2>
<!-- backwards compatibility -->
<a id="schemapath"></a>
<a id="schema_Path"></a>
<a id="tocSpath"></a>
<a id="tocspath"></a>

```json
{
  "nodes": [
    {
      "concept_type": "object_type",
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "concept_groups": [
        {
          "id": "string",
          "name": "string"
        }
      ],
      "data_source": {
        "type": "data_view",
        "id": "string",
        "name": "string"
      },
      "data_properties": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string",
          "mapped_field": "string",
          "index": true,
          "fulltext_config": {
            "analyzer": "standard",
            "field_keyword": true
          },
          "vector_config": {
            "dimension": 0
          }
        }
      ],
      "logic_properties": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string",
          "index": true,
          "data_source": {
            "type": "data_view",
            "id": "string",
            "name": "string"
          },
          "parameters": [
            {
              "name": "string",
              "value_from": "property",
              "value": "string"
            }
          ]
        }
      ],
      "primary_keys": [
        "string"
      ],
      "display_key": "string",
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string"
    }
  ],
  "edges": [
    {
      "concept_type": "relation_type",
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "concept_groups": [
        {
          "id": "string",
          "name": "string"
        }
      ],
      "source_object_type_id": "string",
      "source_object_type_name": "string",
      "target_object_type_id": "string",
      "target_object_type_name": "string",
      "type": "direct",
      "mapping_rules": {},
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string"
    }
  ],
  "length": 0
}

```

路径

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|nodes|[[ObjectTypeDetail](#schemaobjecttypedetail)]|true|none|路径中的节点列表(有序)|
|edges|[[RelationTypeDetail](#schemarelationtypedetail)]|true|none|路径中的边列表(有序)|
|length|integer|true|none|路径长度(边数量)|

<h2 id="tocS_ConceptGroup">ConceptGroup</h2>
<!-- backwards compatibility -->
<a id="schemaconceptgroup"></a>
<a id="schema_ConceptGroup"></a>
<a id="tocSconceptgroup"></a>
<a id="tocsconceptgroup"></a>

```json
{
  "id": "string",
  "name": "string"
}

```

概念分组

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|概念分组ID|
|name|string|true|none|概念分组名称|

<h2 id="tocS_DataSource">DataSource</h2>
<!-- backwards compatibility -->
<a id="schemadatasource"></a>
<a id="schema_DataSource"></a>
<a id="tocSdatasource"></a>
<a id="tocsdatasource"></a>

```json
{
  "type": "data_view",
  "id": "string",
  "name": "string"
}

```

数据来源

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|type|string|true|none|数据来源类型|
|id|string|true|none|数据来源ID|
|name|string|false|none|名称。查看详情时返回。|

#### Enumerated Values

|Property|Value|
|---|---|
|type|data_view|

<h2 id="tocS_DataProperty">DataProperty</h2>
<!-- backwards compatibility -->
<a id="schemadataproperty"></a>
<a id="schema_DataProperty"></a>
<a id="tocSdataproperty"></a>
<a id="tocsdataproperty"></a>

```json
{
  "name": "string",
  "display_name": "string",
  "type": "string",
  "comment": "string",
  "mapped_field": "string",
  "index": true,
  "fulltext_config": {
    "analyzer": "standard",
    "field_keyword": true
  },
  "vector_config": {
    "dimension": 0
  }
}

```

数据属性

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|属性名称。只能包含小写英文字母、数字、下划线（_）、连字符（-），且不能以下划线和连字符开头|
|display_name|string|true|none|属性显示名|
|type|string|true|none|属性数据类型。除了视图的字段类型之外，还有 metric、objective、event、trace、log、operator|
|comment|string|true|none|属性描述|
|mapped_field|string|true|none|属性映射到数据来源中的字段名|
|index|boolean|true|none|是否开启索引，默认是true|
|fulltext_config|[FulltextConfig](#schemafulltextconfig)|true|none|全文索引的配置|
|vector_config|[VectorConfig](#schemavectorconfig)|true|none|向量索引的配置|

<h2 id="tocS_FulltextConfig">FulltextConfig</h2>
<!-- backwards compatibility -->
<a id="schemafulltextconfig"></a>
<a id="schema_FulltextConfig"></a>
<a id="tocSfulltextconfig"></a>
<a id="tocsfulltextconfig"></a>

```json
{
  "analyzer": "standard",
  "field_keyword": true
}

```

全文索引的配置

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|analyzer|string|true|none|分词器|
|field_keyword|boolean|true|none|是否保留原始字符串，保留原始字符串可用于精确匹配。默认是false|

#### Enumerated Values

|Property|Value|
|---|---|
|analyzer|standard|
|analyzer|ik_max_word|

<h2 id="tocS_VectorConfig">VectorConfig</h2>
<!-- backwards compatibility -->
<a id="schemavectorconfig"></a>
<a id="schema_VectorConfig"></a>
<a id="tocSvectorconfig"></a>
<a id="tocsvectorconfig"></a>

```json
{
  "dimension": 0
}

```

向量索引的配置

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|dimension|integer|true|none|向量维度|

<h2 id="tocS_LogicProperty">LogicProperty</h2>
<!-- backwards compatibility -->
<a id="schemalogicproperty"></a>
<a id="schema_LogicProperty"></a>
<a id="tocSlogicproperty"></a>
<a id="tocslogicproperty"></a>

```json
{
  "name": "string",
  "display_name": "string",
  "type": "string",
  "comment": "string",
  "index": true,
  "data_source": {
    "type": "data_view",
    "id": "string",
    "name": "string"
  },
  "parameters": [
    {
      "name": "string",
      "value_from": "property",
      "value": "string"
    }
  ]
}

```

逻辑属性

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|属性名称。只能包含小写英文字母、数字、下划线（_）、连字符（-），且不能以下划线和连字符开头|
|display_name|string|false|none|属性显示名|
|type|string|false|none|属性数据类型。除了视图的字段类型之外，还有 metric、objective、event、trace、log、operator|
|comment|string|false|none|属性描述|
|index|boolean|false|none|是否开启索引，默认是true|
|data_source|[DataSource](#schemadatasource)|true|none|逻辑来源|
|parameters|[[Parameter](#schemaparameter)]|true|none|逻辑所需的参数|

<h2 id="tocS_Object">Object</h2>
<!-- backwards compatibility -->
<a id="schemaobject"></a>
<a id="schema_Object"></a>
<a id="tocSobject"></a>
<a id="tocsobject"></a>

```json
{}

```

json，字段不定

### Properties

*None*

<h2 id="tocS_Parameter">Parameter</h2>
<!-- backwards compatibility -->
<a id="schemaparameter"></a>
<a id="schema_Parameter"></a>
<a id="tocSparameter"></a>
<a id="tocsparameter"></a>

```json
{
  "name": "string",
  "value_from": "property",
  "value": "string"
}

```

逻辑参数

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|参数名称|
|value_from|string|true|none|值来源|
|value|string|false|none|参数值。value_from=property时，填入的是对象类的数据属性名称；value_from=input时，不设置此字段|

#### Enumerated Values

|Property|Value|
|---|---|
|value_from|property|
|value_from|input|

<h2 id="tocS_DataViewMappingRule">DataViewMappingRule</h2>
<!-- backwards compatibility -->
<a id="schemadataviewmappingrule"></a>
<a id="schema_DataViewMappingRule"></a>
<a id="tocSdataviewmappingrule"></a>
<a id="tocsdataviewmappingrule"></a>

```json
{
  "backing_data_source": {
    "type": "data_view",
    "id": "string",
    "name": "string"
  },
  "source_mapping_rules": [
    {
      "target_property": "string",
      "source_property": "string"
    }
  ],
  "target_mapping_rules": [
    {
      "target_property": "string",
      "source_property": "string"
    }
  ]
}

```

关系类型为 data_view 时的匹配规则

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|backing_data_source|[DataSource](#schemadatasource)|true|none|数据来源视图|
|source_mapping_rules|[[Mapping](#schemamapping)]|true|none|起点对象类与数据集的匹配规则|
|target_mapping_rules|[[Mapping](#schemamapping)]|true|none|终点对象类与数据集匹配规则|

<h2 id="tocS_FilteredCrossJoinMappingRule">FilteredCrossJoinMappingRule</h2>
<!-- backwards compatibility -->
<a id="schemafilteredcrossjoinmappingrule"></a>
<a id="schema_FilteredCrossJoinMappingRule"></a>
<a id="tocSfilteredcrossjoinmappingrule"></a>
<a id="tocsfilteredcrossjoinmappingrule"></a>

```json
{
  "source_condition": {},
  "target_condition": {}
}

```

关系类型为 `filtered_cross_join`（分侧过滤全连接，FCJ）时的匹配规则。无数据视图与键映射；
`source_condition` / `target_condition` 为可选的实例过滤条件（结构与对象实例查询 Condition 一致，参见 ontology-query 等查询 API）。
两侧均可省略，或 `mapping_rules` 为 `{}`（表示两侧无额外过滤）。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|source_condition|object|false|none|起点侧实例过滤条件；可省略表示该侧无约束|
|target_condition|object|false|none|终点侧实例过滤条件；可省略表示该侧无约束|

<h2 id="tocS_ActionCondition">ActionCondition</h2>
<!-- backwards compatibility -->
<a id="schemaactioncondition"></a>
<a id="schema_ActionCondition"></a>
<a id="tocSactioncondition"></a>
<a id="tocsactioncondition"></a>

```json
{
  "object_type_id": "string",
  "field": "string",
  "operation": "and",
  "sub_conditions": [
    {
      "object_type_id": "string",
      "field": "string",
      "operation": "and",
      "sub_conditions": [],
      "value": null,
      "value_from": "const"
    }
  ],
  "value": null,
  "value_from": "const"
}

```

行动条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_type_id|string|false|none|对象类ID。当时多个对象类的过滤时，需要把对象类ID带上，否则只要属性名属于对象类就会进行过滤。|
|field|string|false|none|字段名称，也即对象类的属性名称|
|operation|string|false|none|操作符|
|sub_conditions|[[ActionCondition](#schemaactioncondition)]|false|none|子过滤条件|
|value|any|false|none|字段值|
|value_from|string|false|none|字段值来源，当前仅支持 "const"|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|and|
|operation|or|
|operation|==|
|operation|!=|
|operation|>|
|operation|>=|
|operation|<|
|operation|<=|
|operation|in|
|operation|not_in|
|operation|range|
|operation|out_range|
|operation|exist|
|operation|not_exist|
|value_from|const|

<h2 id="tocS_ExpectedImpactOperation">ExpectedImpactOperation</h2>
<!-- backwards compatibility -->
<a id="schemaexpectedimpactoperation"></a>
<a id="schema_ExpectedImpactOperation"></a>
<a id="tocSexpectedimpactoperation"></a>
<a id="tocsexpectedimpactoperation"></a>

```json
"add"

```

与 `action_intent` / `action_type` 一致；`impact_contracts[].expected_operation` 必填；`affect.expected_operation` 若填则须合法。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|string|false|none|与 `action_intent` / `action_type` 一致；`impact_contracts[].expected_operation` 必填；`affect.expected_operation` 若填则须合法。|

#### Enumerated Values

|Property|Value|
|---|---|
|*anonymous*|add|
|*anonymous*|modify|
|*anonymous*|delete|

<h2 id="tocS_ImpactContractItem">ImpactContractItem</h2>
<!-- backwards compatibility -->
<a id="schemaimpactcontractitem"></a>
<a id="schema_ImpactContractItem"></a>
<a id="tocSimpactcontractitem"></a>
<a id="tocsimpactcontractitem"></a>

```json
{
  "object_type_id": "string",
  "expected_operation": "add",
  "description": "string",
  "affected_fields": [
    "string"
  ]
}

```

行动影响契约单条（与 `action-type.yaml`、`f_impact_contracts` 一致）；DESIGN §7。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_type_id|string|false|none|none|
|expected_operation|[ExpectedImpactOperation](#schemaexpectedimpactoperation)|false|none|与 `action_intent` / `action_type` 一致；`impact_contracts[].expected_operation` 必填；`affect.expected_operation` 若填则须合法。|
|description|string|false|none|none|
|affected_fields|[string]|false|none|none|

<h2 id="tocS_Affect">Affect</h2>
<!-- backwards compatibility -->
<a id="schemaaffect"></a>
<a id="schema_Affect"></a>
<a id="tocSaffect"></a>
<a id="tocsaffect"></a>

```json
{
  "object_type": "string",
  "comment": "string",
  "expected_operation": "add",
  "affected_fields": [
    "string"
  ]
}

```

**[已废弃]** 请改用 `impact_contracts`。DESIGN §5.9。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_type|string|true|none|影响的对象类ID|
|comment|string|true|none|影响描述|
|expected_operation|[ExpectedImpactOperation](#schemaexpectedimpactoperation)|false|none|若填写须为 add / modify / delete 之一（与行动类 `action_intent` 一致）；可省略。|
|affected_fields|[string]|false|none|预期牵涉的数据属性名列表|

<h2 id="tocS_Schedule">Schedule</h2>
<!-- backwards compatibility -->
<a id="schemaschedule"></a>
<a id="schema_Schedule"></a>
<a id="tocSschedule"></a>
<a id="tocsschedule"></a>

```json
{
  "type": "FIX_RATE",
  "expression": "string"
}

```

执行频率配置项

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|type|string|true|none|执行类型。枚举，支持配置固定频率(FIX_RATE)和配置crontab表达式（CRON）|
|expression|string|true|none|执行表达式。<br><br>1.固定频率指以固定周期执行持久化，frequency=< time_durations >，用一个数字，后面跟时间单位来定义。时间单位可以是如下之一：m - 分钟； h - 小时； d - 天|

#### Enumerated Values

|Property|Value|
|---|---|
|type|FIX_RATE|
|type|CRON|

<h2 id="tocS_RelationPathReqeustBody">RelationPathReqeustBody</h2>
<!-- backwards compatibility -->
<a id="schemarelationpathreqeustbody"></a>
<a id="schema_RelationPathReqeustBody"></a>
<a id="tocSrelationpathreqeustbody"></a>
<a id="tocsrelationpathreqeustbody"></a>

```json
{
  "concept_groups": [
    "string"
  ],
  "source_object_type": "string",
  "target_object_type": "string",
  "path_max_length": 0,
  "direction": "forward",
  "path_select_policy": "string"
}

```

路径查询的请求体

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|concept_groups|[string]|false|none|概念分组id|
|source_object_type|string|true|none|起点对象类ID|
|target_object_type|string|false|none|终点对象类ID。当终点类不为空时，查找两个对象类之间的路径；当终点类为空时，从起点类按路径长度查找关联的所有的点|
|path_max_length|integer|true|none|最大路径长度,不超过5.|
|direction|string|true|none|方向：正向(forward)、反向(reverse)、双向(bidirectional)|
|path_select_policy|string|false|none|路径选择策略。多路径下选择路径的条件或规则，不给则把所有的关系都返回。当前需求不给，策略未来再设计实现|

#### Enumerated Values

|Property|Value|
|---|---|
|direction|forward|
|direction|reverse|
|direction|bidirectional|

<h2 id="tocS_ID">ID</h2>
<!-- backwards compatibility -->
<a id="schemaid"></a>
<a id="schema_ID"></a>
<a id="tocSid"></a>
<a id="tocsid"></a>

```json
{
  "id": "string"
}

```

id

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|id|

<h2 id="tocS_ReqBranch">ReqBranch</h2>
<!-- backwards compatibility -->
<a id="schemareqbranch"></a>
<a id="schema_ReqBranch"></a>
<a id="tocSreqbranch"></a>
<a id="tocsreqbranch"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "base_version": "string"
}

```

创建分支的信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|false|none|分支ID。新建后不可更改。只能包含小写英文字母、数字、下划线（_）、连字符（-），且不能以下划线和连字符开头|
|name|string|true|none|分支名称。新建后不可更改，只能包含小写英文字母、数字、下划线（_）、连字符（-），且不能以下划线和连字符开头|
|tags|[string]|false|none|标签。用于业务标识|
|comment|string|false|none|备注|
|base_version|string|true|none|来源版本|

<h2 id="tocS_UpdateBranch">UpdateBranch</h2>
<!-- backwards compatibility -->
<a id="schemaupdatebranch"></a>
<a id="schema_UpdateBranch"></a>
<a id="tocSupdatebranch"></a>
<a id="tocsupdatebranch"></a>

```json
{
  "tags": [
    "string"
  ],
  "comment": "string"
}

```

更新分支信息。只能修改分支的标签、描述

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|tags|[string]|false|none|标签。用于业务标识|
|comment|string|false|none|备注|

<h2 id="tocS_Branch">Branch</h2>
<!-- backwards compatibility -->
<a id="schemabranch"></a>
<a id="schema_Branch"></a>
<a id="tocSbranch"></a>
<a id="tocsbranch"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "base_version": "string",
  "kn_id": "string"
}

```

分支信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|分支ID|
|name|string|true|none|分支名称|
|tags|[string]|true|none|标签|
|comment|string|true|none|备注|
|base_version|string|true|none|来源版本|
|kn_id|string|true|none|业务知识网络ID|

<h2 id="tocS_ReqConceptGroup">ReqConceptGroup</h2>
<!-- backwards compatibility -->
<a id="schemareqconceptgroup"></a>
<a id="schema_ReqConceptGroup"></a>
<a id="tocSreqconceptgroup"></a>
<a id="tocsreqconceptgroup"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "branch": "string",
  "base_version": "string",
  "object_type_ids": [
    "string"
  ],
  "relation_type_ids": [
    "string"
  ],
  "action_type_ids": [
    "string"
  ]
}

```

概念分组创建信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|false|none|ID。新建后不可更改。只能包含小写英文字母、数字、下划线（_）、连字符（-），且不能以下划线和连字符开头|
|name|string|true|none|名称。|
|tags|[string]|false|none|标签。用于业务标识|
|comment|string|false|none|备注|
|branch|string|true|none|分支ID|
|base_version|string|true|none|来源版本|
|object_type_ids|[string]|false|none|概念分组包含的对象类列表|
|relation_type_ids|[string]|false|none|概念分组包含的关系类列表|
|action_type_ids|[string]|false|none|概念分组包含的行动类列表|

<h2 id="tocS_UpdateConceptGroup">UpdateConceptGroup</h2>
<!-- backwards compatibility -->
<a id="schemaupdateconceptgroup"></a>
<a id="schema_UpdateConceptGroup"></a>
<a id="tocSupdateconceptgroup"></a>
<a id="tocsupdateconceptgroup"></a>

```json
{
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "branch": "string",
  "base_version": "string",
  "object_type_ids": [
    "string"
  ],
  "relation_type_ids": [
    "string"
  ],
  "action_type_ids": [
    "string"
  ]
}

```

修改概念分组信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|false|none|名称。|
|tags|[string]|false|none|标签。用于业务标识|
|comment|string|false|none|备注|
|branch|string|true|none|分支ID|
|base_version|string|true|none|来源版本|
|object_type_ids|[string]|false|none|概念分组包含的对象类列表|
|relation_type_ids|[string]|false|none|概念分组包含的关系类列表|
|action_type_ids|[string]|false|none|概念分组包含的行动类列表|

<h2 id="tocS_ListBranches">ListBranches</h2>
<!-- backwards compatibility -->
<a id="schemalistbranches"></a>
<a id="schema_ListBranches"></a>
<a id="tocSlistbranches"></a>
<a id="tocslistbranches"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "base_version": "string",
      "kn_id": "string"
    }
  ],
  "total_count": 0
}

```

分支列表

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[Branch](#schemabranch)]|true|none|条目列表|
|total_count|integer|true|none|总条数|

<h2 id="tocS_ListObjectTypes">ListObjectTypes</h2>
<!-- backwards compatibility -->
<a id="schemalistobjecttypes"></a>
<a id="schema_ListObjectTypes"></a>
<a id="tocSlistobjecttypes"></a>
<a id="tocslistobjecttypes"></a>

```json
{
  "entries": [
    {
      "concept_type": "object_type",
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "concept_groups": [
        {
          "id": "string",
          "name": "string"
        }
      ],
      "data_source": {
        "type": "data_view",
        "id": "string",
        "name": "string"
      },
      "data_properties": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string",
          "mapped_field": "string",
          "index": true,
          "fulltext_config": {
            "analyzer": "standard",
            "field_keyword": true
          },
          "vector_config": {
            "dimension": 0
          }
        }
      ],
      "logic_properties": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string",
          "index": true,
          "data_source": {
            "type": "data_view",
            "id": "string",
            "name": "string"
          },
          "parameters": [
            {
              "name": "string",
              "value_from": "property",
              "value": "string"
            }
          ]
        }
      ],
      "primary_keys": [
        "string"
      ],
      "display_key": "string",
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string"
    }
  ],
  "total_count": 0
}

```

对象类列表

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[ObjectTypeDetail](#schemaobjecttypedetail)]|true|none|条目列表|
|total_count|integer|true|none|总条数|

<h2 id="tocS_ListRelationTypes">ListRelationTypes</h2>
<!-- backwards compatibility -->
<a id="schemalistrelationtypes"></a>
<a id="schema_ListRelationTypes"></a>
<a id="tocSlistrelationtypes"></a>
<a id="tocslistrelationtypes"></a>

```json
{
  "entries": [
    {
      "concept_type": "relation_type",
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "concept_groups": [
        {
          "id": "string",
          "name": "string"
        }
      ],
      "source_object_type_id": "string",
      "source_object_type_name": "string",
      "target_object_type_id": "string",
      "target_object_type_name": "string",
      "type": "direct",
      "mapping_rules": {},
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string"
    }
  ],
  "total_count": 0
}

```

关系类列表

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[RelationTypeDetail](#schemarelationtypedetail)]|true|none|条目列表|
|total_count|integer|true|none|总条数|

<h2 id="tocS_ListActionTypes">ListActionTypes</h2>
<!-- backwards compatibility -->
<a id="schemalistactiontypes"></a>
<a id="schema_ListActionTypes"></a>
<a id="tocSlistactiontypes"></a>
<a id="tocslistactiontypes"></a>

```json
{
  "entries": [
    {
      "concept_type": "action_type",
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "concept_groups": [
        {
          "id": "string",
          "name": "string"
        }
      ],
      "action_type": "add",
      "action_intent": "add",
      "object_type_id": "string",
      "object_type_name": "string",
      "condition": {
        "object_type_id": "string",
        "field": "string",
        "operation": "and",
        "sub_conditions": [
          {}
        ],
        "value": null,
        "value_from": "const"
      },
      "affect": {
        "object_type": "string",
        "comment": "string",
        "expected_operation": "add",
        "affected_fields": [
          "string"
        ]
      },
      "impact_contracts": [
        {
          "object_type_id": "string",
          "expected_operation": "add",
          "description": "string",
          "affected_fields": [
            "string"
          ]
        }
      ],
      "action_source": {
        "type": "data_view",
        "id": "string",
        "name": "string"
      },
      "parameters": [
        {
          "name": "string",
          "value_from": "property",
          "value": "string"
        }
      ],
      "schedule": {
        "type": "FIX_RATE",
        "expression": "string"
      },
      "detail": "string"
    }
  ],
  "total_count": 0
}

```

行动类列表

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[ActionTypeDetail](#schemaactiontypedetail)]|true|none|条目列表|
|total_count|integer|true|none|总条数|

<h2 id="tocS_Mapping">Mapping</h2>
<!-- backwards compatibility -->
<a id="schemamapping"></a>
<a id="schema_Mapping"></a>
<a id="tocSmapping"></a>
<a id="tocsmapping"></a>

```json
{
  "target_property": "string",
  "source_property": "string"
}

```

关联规则

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|target_property|string|true|none|起点属性|
|source_property|string|true|none|终点属性|

<h2 id="tocS_DirectMappings">DirectMappings</h2>
<!-- backwards compatibility -->
<a id="schemadirectmappings"></a>
<a id="schema_DirectMappings"></a>
<a id="tocSdirectmappings"></a>
<a id="tocsdirectmappings"></a>

```json
{}

```

直接关联

### Properties

*None*

<h2 id="tocS_m">m</h2>
<!-- backwards compatibility -->
<a id="schemam"></a>
<a id="schema_m"></a>
<a id="tocSm"></a>
<a id="tocsm"></a>

```json
{
  "mapping_rules": [
    {
      "target_property": "string",
      "source_property": "string"
    }
  ]
}

```

m

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|mapping_rules|[[Mapping](#schemamapping)]|false|none|[关联规则]|

<h2 id="tocS_ReqObjectType">ReqObjectType</h2>
<!-- backwards compatibility -->
<a id="schemareqobjecttype"></a>
<a id="schema_ReqObjectType"></a>
<a id="tocSreqobjecttype"></a>
<a id="tocsreqobjecttype"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "concept_groups": [
    "string"
  ],
  "data_source": {
    "type": "data_view",
    "id": "string",
    "name": "string"
  },
  "data_properties": [
    {
      "name": "string",
      "display_name": "string",
      "type": "string",
      "comment": "string",
      "mapped_field": "string",
      "index": true,
      "fulltext_config": {
        "analyzer": "standard",
        "field_keyword": true
      },
      "vector_config": {
        "dimension": 0
      }
    }
  ],
  "logic_properties": [
    {
      "name": "string",
      "display_name": "string",
      "type": "string",
      "comment": "string",
      "index": true,
      "data_source": {
        "type": "data_view",
        "id": "string",
        "name": "string"
      },
      "parameters": [
        {
          "name": "string",
          "value_from": "property",
          "value": "string"
        }
      ]
    }
  ],
  "primary_keys": [
    "string"
  ],
  "display_key": "string"
}

```

对象类创建信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|false|none|ID.新建后不可修改，只能包含小写英文字母、数字、下划线（_）、连字符（-），且不能以下划线和连字符开头|
|name|string|true|none|名称|
|tags|[string]|false|none|标签。用于业务标识|
|comment|string|false|none|备注|
|icon|string|false|none|图标|
|color|string|false|none|颜色|
|branch|string|true|none|分支ID|
|concept_groups|[string]|false|none|概念分组|
|data_source|[DataSource](#schemadatasource)|false|none|数据来源|
|data_properties|[[DataProperty](#schemadataproperty)]|true|none|数据属性|
|logic_properties|[[LogicProperty](#schemalogicproperty)]|false|none|逻辑属性|
|primary_keys|[string]|true|none|主键，唯一标识|
|display_key|string|true|none|对象的显示属性|

<h2 id="tocS_ReqKnowledgeNetwork">ReqKnowledgeNetwork</h2>
<!-- backwards compatibility -->
<a id="schemareqknowledgenetwork"></a>
<a id="schema_ReqKnowledgeNetwork"></a>
<a id="tocSreqknowledgenetwork"></a>
<a id="tocsreqknowledgenetwork"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string"
}

```

业务知识网络创建请求体

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|false|none|业务知识网络ID.新建后不可修改，只能包含小写英文字母、数字、下划线（_）、连字符（-），且不能以下划线和连字符开头|
|name|string|true|none|业务知识网络名称|
|tags|[string]|false|none|标签。用于业务标识|
|comment|string|false|none|备注|
|icon|string|false|none|图标|
|color|string|false|none|颜色|
|branch|string|true|none|分支ID|

<h2 id="tocS_UpdateKnowledgeNetwork">UpdateKnowledgeNetwork</h2>
<!-- backwards compatibility -->
<a id="schemaupdateknowledgenetwork"></a>
<a id="schema_UpdateKnowledgeNetwork"></a>
<a id="tocSupdateknowledgenetwork"></a>
<a id="tocsupdateknowledgenetwork"></a>

```json
{
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string"
}

```

业务知识网络创建请求体

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|业务知识网络名称|
|tags|[string]|false|none|标签。用于业务标识|
|comment|string|false|none|指标模型备注|
|icon|string|false|none|图标|
|color|string|false|none|颜色|
|branch|string|true|none|分支ID|

<h2 id="tocS_UpdateObjectType">UpdateObjectType</h2>
<!-- backwards compatibility -->
<a id="schemaupdateobjecttype"></a>
<a id="schema_UpdateObjectType"></a>
<a id="tocSupdateobjecttype"></a>
<a id="tocsupdateobjecttype"></a>

```json
{
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "concept_groups": [
    "string"
  ],
  "data_source": {
    "type": "data_view",
    "id": "string",
    "name": "string"
  },
  "data_properties": [
    {
      "name": "string",
      "display_name": "string",
      "type": "string",
      "comment": "string",
      "mapped_field": "string",
      "index": true,
      "fulltext_config": {
        "analyzer": "standard",
        "field_keyword": true
      },
      "vector_config": {
        "dimension": 0
      }
    }
  ],
  "logic_properties": [
    {
      "name": "string",
      "display_name": "string",
      "type": "string",
      "comment": "string",
      "index": true,
      "data_source": {
        "type": "data_view",
        "id": "string",
        "name": "string"
      },
      "parameters": [
        {
          "name": "string",
          "value_from": "property",
          "value": "string"
        }
      ]
    }
  ],
  "primary_keys": [
    "string"
  ],
  "display_key": "string"
}

```

更新对象类

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|名称|
|tags|[string]|false|none|标签。用于业务标识|
|comment|string|false|none|备注|
|icon|string|false|none|图标|
|color|string|false|none|颜色|
|branch|string|true|none|分支ID|
|concept_groups|[string]|false|none|概念分组|
|data_source|[DataSource](#schemadatasource)|false|none|数据来源|
|data_properties|[[DataProperty](#schemadataproperty)]|true|none|数据属性|
|logic_properties|[[LogicProperty](#schemalogicproperty)]|false|none|逻辑属性|
|primary_keys|[string]|true|none|主键，唯一标识|
|display_key|string|true|none|对象的显示属性|

<h2 id="tocS_ReqRelationType">ReqRelationType</h2>
<!-- backwards compatibility -->
<a id="schemareqrelationtype"></a>
<a id="schema_ReqRelationType"></a>
<a id="tocSreqrelationtype"></a>
<a id="tocsreqrelationtype"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "concept_groups": [
    "string"
  ],
  "source_object_type_id": "string",
  "target_object_type_id": "string",
  "type": "direct",
  "mapping_rules": {
    "mapping_rules": [
      {
        "target_property": "string",
        "source_property": "string"
      }
    ]
  }
}

```

关系类创建信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|false|none|ID.新建后不可修改，只能包含小写英文字母、数字、下划线（_）、连字符（-），且不能以下划线和连字符开头|
|name|string|true|none|名称|
|tags|[string]|false|none|标签。用于业务标识|
|comment|string|false|none|备注|
|icon|string|false|none|图标|
|color|string|false|none|颜色|
|branch|string|true|none|分支ID|
|concept_groups|[string]|false|none|概念分组|
|source_object_type_id|string|false|none|起点象类ID|
|target_object_type_id|string|false|none|终点对象类ID|
|type|string|true|none|关系类型|
|mapping_rules|any|false|none|关联的匹配规则。direct 为键映射；data_view 参考 DataViewMappingRule；filtered_cross_join（FCJ）参考 FilteredCrossJoinMappingRule（分侧条件）。直接关联时是一个 map，标记的是起点对象匹配属性1: 终点对象匹配属性1|

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[m](#schemam)|false|none|m|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[DataViewMappingRule](#schemadataviewmappingrule)|false|none|关系类型为 data_view 时的匹配规则|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[FilteredCrossJoinMappingRule](#schemafilteredcrossjoinmappingrule)|false|none|关系类型为 `filtered_cross_join`（分侧过滤全连接，FCJ）时的匹配规则。无数据视图与键映射；<br>`source_condition` / `target_condition` 为可选的实例过滤条件（结构与对象实例查询 Condition 一致，参见 ontology-query 等查询 API）。<br>两侧均可省略，或 `mapping_rules` 为 `{}`（表示两侧无额外过滤）。|

#### Enumerated Values

|Property|Value|
|---|---|
|type|direct|
|type|data_view|
|type|filtered_cross_join|

<h2 id="tocS_UpdateRelationType">UpdateRelationType</h2>
<!-- backwards compatibility -->
<a id="schemaupdaterelationtype"></a>
<a id="schema_UpdateRelationType"></a>
<a id="tocSupdaterelationtype"></a>
<a id="tocsupdaterelationtype"></a>

```json
{
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "concept_groups": [
    "string"
  ],
  "source_object_type_id": "string",
  "target_object_type_id": "string",
  "type": "direct",
  "mapping_rules": {}
}

```

关系类更新信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|名称|
|tags|[string]|false|none|标签。用于业务标识|
|comment|string|false|none|备注|
|icon|string|false|none|图标|
|color|string|false|none|颜色|
|branch|string|true|none|分支ID|
|concept_groups|[string]|false|none|概念分组|
|source_object_type_id|string|false|none|起点象类ID|
|target_object_type_id|string|false|none|终点对象类ID|
|type|string|false|none|关系类型|
|mapping_rules|any|false|none|关联的匹配规则。direct 为键映射；data_view 参考 DataViewMappingRule；filtered_cross_join（FCJ）参考 FilteredCrossJoinMappingRule。直接关联时是一个 map，标记的是起点对象匹配属性1: 终点对象匹配属性1|

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[Object](#schemaobject)|false|none|json，字段不定|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[DataViewMappingRule](#schemadataviewmappingrule)|false|none|关系类型为 data_view 时的匹配规则|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[FilteredCrossJoinMappingRule](#schemafilteredcrossjoinmappingrule)|false|none|关系类型为 `filtered_cross_join`（分侧过滤全连接，FCJ）时的匹配规则。无数据视图与键映射；<br>`source_condition` / `target_condition` 为可选的实例过滤条件（结构与对象实例查询 Condition 一致，参见 ontology-query 等查询 API）。<br>两侧均可省略，或 `mapping_rules` 为 `{}`（表示两侧无额外过滤）。|

#### Enumerated Values

|Property|Value|
|---|---|
|type|direct|
|type|data_view|
|type|filtered_cross_join|

<h2 id="tocS_ReqActionType">ReqActionType</h2>
<!-- backwards compatibility -->
<a id="schemareqactiontype"></a>
<a id="schema_ReqActionType"></a>
<a id="tocSreqactiontype"></a>
<a id="tocsreqactiontype"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "concept_groups": [
    {
      "id": "string",
      "name": "string"
    }
  ],
  "action_type": "add",
  "action_intent": "add",
  "object_type_id": "string",
  "condition": {
    "object_type_id": "string",
    "field": "string",
    "operation": "and",
    "sub_conditions": [
      {}
    ],
    "value": null,
    "value_from": "const"
  },
  "affect": {
    "object_type": "string",
    "comment": "string",
    "expected_operation": "add",
    "affected_fields": [
      "string"
    ]
  },
  "impact_contracts": [
    {
      "object_type_id": "string",
      "expected_operation": "add",
      "description": "string",
      "affected_fields": [
        "string"
      ]
    }
  ],
  "action_source": {
    "type": "data_view",
    "id": "string",
    "name": "string"
  },
  "parameters": [
    {
      "name": "string",
      "value_from": "property",
      "value": "string"
    }
  ],
  "schedule": {
    "type": "FIX_RATE",
    "expression": "string"
  }
}

```

行动类创建信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|false|none|行动类ID|
|name|string|true|none|行动类名称|
|tags|[string]|false|none|标签。|
|comment|string|false|none|备注|
|icon|string|false|none|图标|
|color|string|false|none|颜色|
|branch|string|true|none|分支ID|
|concept_groups|[[ConceptGroup](#schemaconceptgroup)]|false|none|概念分组id|
|action_type|string|true|none|**[已废弃]** 优先 `action_intent`（取值 add/modify/delete）；双写须一致。|
|action_intent|string|false|none|推荐写入的行动意图枚举。|
|object_type_id|string|true|none|行动类所绑定的对象类ID|
|condition|[ActionCondition](#schemaactioncondition)|false|none|行动条件|
|affect|[Affect](#schemaaffect)|false|none|**[已废弃]** 使用 `impact_contracts`。|
|impact_contracts|[[ImpactContractItem](#schemaimpactcontractitem)]|false|none|[行动影响契约单条（与 `action-type.yaml`、`f_impact_contracts` 一致）；DESIGN §7。<br>]|
|action_source|[DataSource](#schemadatasource)|false|none|绑定的行动的资源|
|parameters|[[Parameter](#schemaparameter)]|false|none|行动资源参数|
|schedule|[Schedule](#schemaschedule)|false|none|行动监听参数配置|

#### Enumerated Values

|Property|Value|
|---|---|
|action_type|add|
|action_type|modify|
|action_type|delete|
|action_intent|add|
|action_intent|modify|
|action_intent|delete|

<h2 id="tocS_UpdateActionType">UpdateActionType</h2>
<!-- backwards compatibility -->
<a id="schemaupdateactiontype"></a>
<a id="schema_UpdateActionType"></a>
<a id="tocSupdateactiontype"></a>
<a id="tocsupdateactiontype"></a>

```json
{
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "concept_groups": [
    {
      "id": "string",
      "name": "string"
    }
  ],
  "action_type": "add",
  "action_intent": "add",
  "object_type_id": "string",
  "condition": {
    "object_type_id": "string",
    "field": "string",
    "operation": "and",
    "sub_conditions": [
      {}
    ],
    "value": null,
    "value_from": "const"
  },
  "affect": {
    "object_type": "string",
    "comment": "string",
    "expected_operation": "add",
    "affected_fields": [
      "string"
    ]
  },
  "impact_contracts": [
    {
      "object_type_id": "string",
      "expected_operation": "add",
      "description": "string",
      "affected_fields": [
        "string"
      ]
    }
  ],
  "action_source": {
    "type": "data_view",
    "id": "string",
    "name": "string"
  },
  "parameters": [
    {
      "name": "string",
      "value_from": "property",
      "value": "string"
    }
  ],
  "schedule": {
    "type": "FIX_RATE",
    "expression": "string"
  }
}

```

行动类更新信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|行动类名称|
|tags|[string]|false|none|标签。 （可以为空）|
|comment|string|false|none|备注（可以为空）|
|icon|string|false|none|图标|
|color|string|false|none|颜色|
|branch|string|true|none|分支ID|
|concept_groups|[[ConceptGroup](#schemaconceptgroup)]|false|none|概念分组id|
|action_type|string|true|none|**[已废弃]** 优先 `action_intent`。|
|action_intent|string|false|none|服务端持久化字段 `f_action_intent`。|
|object_type_id|string|true|none|行动类所绑定的对象类ID|
|condition|[ActionCondition](#schemaactioncondition)|false|none|行动条件|
|affect|[Affect](#schemaaffect)|false|none|**[已废弃]** `impact_contracts`。|
|impact_contracts|[[ImpactContractItem](#schemaimpactcontractitem)]|false|none|[行动影响契约单条（与 `action-type.yaml`、`f_impact_contracts` 一致）；DESIGN §7。<br>]|
|action_source|[DataSource](#schemadatasource)|false|none|绑定的行动的资源|
|parameters|[[Parameter](#schemaparameter)]|false|none|行动资源参数|
|schedule|[Schedule](#schemaschedule)|false|none|行动监听参数配置|

#### Enumerated Values

|Property|Value|
|---|---|
|action_type|add|
|action_type|modify|
|action_type|delete|
|action_intent|add|
|action_intent|modify|
|action_intent|delete|

<h2 id="tocS_ActionTypeDetail">ActionTypeDetail</h2>
<!-- backwards compatibility -->
<a id="schemaactiontypedetail"></a>
<a id="schema_ActionTypeDetail"></a>
<a id="tocSactiontypedetail"></a>
<a id="tocsactiontypedetail"></a>

```json
{
  "concept_type": "action_type",
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "kn_id": "string",
  "concept_groups": [
    {
      "id": "string",
      "name": "string"
    }
  ],
  "action_type": "add",
  "action_intent": "add",
  "object_type_id": "string",
  "object_type_name": "string",
  "condition": {
    "object_type_id": "string",
    "field": "string",
    "operation": "and",
    "sub_conditions": [
      {}
    ],
    "value": null,
    "value_from": "const"
  },
  "affect": {
    "object_type": "string",
    "comment": "string",
    "expected_operation": "add",
    "affected_fields": [
      "string"
    ]
  },
  "impact_contracts": [
    {
      "object_type_id": "string",
      "expected_operation": "add",
      "description": "string",
      "affected_fields": [
        "string"
      ]
    }
  ],
  "action_source": {
    "type": "data_view",
    "id": "string",
    "name": "string"
  },
  "parameters": [
    {
      "name": "string",
      "value_from": "property",
      "value": "string"
    }
  ],
  "schedule": {
    "type": "FIX_RATE",
    "expression": "string"
  },
  "detail": "string"
}

```

行动类

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|concept_type|string|true|none|概念类型|
|id|string|true|none|行动类ID|
|name|string|true|none|行动类名称|
|tags|[string]|true|none|标签。 （可以为空）|
|comment|string|true|none|备注（可以为空）|
|icon|string|true|none|图标|
|color|string|true|none|颜色|
|branch|string|true|none|分支ID|
|kn_id|string|true|none|业务知识网络ID|
|concept_groups|[[ConceptGroup](#schemaconceptgroup)]|true|none|概念分组id|
|action_type|string|true|none|**[已废弃]** 等价于 `action_intent`。|
|action_intent|string|false|none|none|
|object_type_id|string|true|none|行动类所绑定的对象类ID|
|object_type_name|string|true|none|行动类所绑定的对象类名称|
|condition|[ActionCondition](#schemaactioncondition)|true|none|行动条件|
|affect|[Affect](#schemaaffect)|true|none|**[已废弃]** 单行影响；参见 `impact_contracts`。|
|impact_contracts|[[ImpactContractItem](#schemaimpactcontractitem)]|false|none|[行动影响契约单条（与 `action-type.yaml`、`f_impact_contracts` 一致）；DESIGN §7。<br>]|
|action_source|[DataSource](#schemadatasource)|true|none|绑定的行动的资源|
|parameters|[[Parameter](#schemaparameter)]|true|none|行动资源参数|
|schedule|[Schedule](#schemaschedule)|true|none|行动监听参数配置|
|detail|string|true|none|说明书。按需返回，若指定了include_detail=true，则返回，否则不返回|

#### Enumerated Values

|Property|Value|
|---|---|
|concept_type|action_type|
|action_type|add|
|action_type|modify|
|action_type|delete|
|action_intent|add|
|action_intent|modify|
|action_intent|delete|

<h2 id="tocS_ListKN">ListKN</h2>
<!-- backwards compatibility -->
<a id="schemalistkn"></a>
<a id="schema_ListKN"></a>
<a id="tocSlistkn"></a>
<a id="tocslistkn"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string",
      "statistics": {
        "object_types_total": 0,
        "relation_types_total": 0,
        "action_types_total": 0
      },
      "concept_groups": [
        {
          "id": "string",
          "name": "string",
          "tags": [
            "string"
          ],
          "comment": "string",
          "icon": "string",
          "color": "string",
          "kn_id": "string",
          "branch": "string",
          "creator": "string",
          "create_time": 0,
          "updator": "string",
          "update_time": 0,
          "detail": "string"
        }
      ],
      "object_types": [
        {
          "concept_type": "object_type",
          "id": "string",
          "name": "string",
          "tags": [
            "string"
          ],
          "comment": "string",
          "icon": "string",
          "color": "string",
          "branch": "string",
          "kn_id": "string",
          "concept_groups": [
            {
              "id": "string",
              "name": "string"
            }
          ],
          "data_source": {
            "type": "data_view",
            "id": "string",
            "name": "string"
          },
          "data_properties": [
            {
              "name": "string",
              "display_name": "string",
              "type": "string",
              "comment": "string",
              "mapped_field": "string",
              "index": true,
              "fulltext_config": {
                "analyzer": "standard",
                "field_keyword": true
              },
              "vector_config": {
                "dimension": 0
              }
            }
          ],
          "logic_properties": [
            {
              "name": "string",
              "display_name": "string",
              "type": "string",
              "comment": "string",
              "index": true,
              "data_source": {
                "type": "data_view",
                "id": "string",
                "name": "string"
              },
              "parameters": [
                {
                  "name": "string",
                  "value_from": "property",
                  "value": "string"
                }
              ]
            }
          ],
          "primary_keys": [
            "string"
          ],
          "display_key": "string",
          "creator": "string",
          "create_time": 0,
          "updater": "string",
          "update_time": 0,
          "detail": "string"
        }
      ],
      "relation_types": [
        {
          "concept_type": "relation_type",
          "id": "string",
          "name": "string",
          "tags": [
            "string"
          ],
          "comment": "string",
          "icon": "string",
          "color": "string",
          "branch": "string",
          "kn_id": "string",
          "concept_groups": [
            {
              "id": "string",
              "name": "string"
            }
          ],
          "source_object_type_id": "string",
          "source_object_type_name": "string",
          "target_object_type_id": "string",
          "target_object_type_name": "string",
          "type": "direct",
          "mapping_rules": {},
          "creator": "string",
          "create_time": 0,
          "updater": "string",
          "update_time": 0,
          "detail": "string"
        }
      ],
      "action_types": [
        {
          "concept_type": "action_type",
          "id": "string",
          "name": "string",
          "tags": [
            "string"
          ],
          "comment": "string",
          "icon": "string",
          "color": "string",
          "branch": "string",
          "kn_id": "string",
          "concept_groups": [
            {
              "id": "string",
              "name": "string"
            }
          ],
          "action_type": "add",
          "action_intent": "add",
          "object_type_id": "string",
          "object_type_name": "string",
          "condition": {
            "object_type_id": "string",
            "field": "string",
            "operation": "and",
            "sub_conditions": [
              {}
            ],
            "value": null,
            "value_from": "const"
          },
          "affect": {
            "object_type": "string",
            "comment": "string",
            "expected_operation": "add",
            "affected_fields": [
              "string"
            ]
          },
          "impact_contracts": [
            {
              "object_type_id": "string",
              "expected_operation": "add",
              "description": "string",
              "affected_fields": [
                "string"
              ]
            }
          ],
          "action_source": {
            "type": "data_view",
            "id": "string",
            "name": "string"
          },
          "parameters": [
            {
              "name": "string",
              "value_from": "property",
              "value": "string"
            }
          ],
          "schedule": {
            "type": "FIX_RATE",
            "expression": "string"
          },
          "detail": "string"
        }
      ]
    }
  ],
  "total_count": 0
}

```

业务知识网络列表

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[KnowledgeNetworkDetail](#schemaknowledgenetworkdetail)]|true|none|条目列表|
|total_count|integer|true|none|总条数|

<h2 id="tocS_ObjectTypeDetail">ObjectTypeDetail</h2>
<!-- backwards compatibility -->
<a id="schemaobjecttypedetail"></a>
<a id="schema_ObjectTypeDetail"></a>
<a id="tocSobjecttypedetail"></a>
<a id="tocsobjecttypedetail"></a>

```json
{
  "concept_type": "object_type",
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "kn_id": "string",
  "concept_groups": [
    {
      "id": "string",
      "name": "string"
    }
  ],
  "data_source": {
    "type": "data_view",
    "id": "string",
    "name": "string"
  },
  "data_properties": [
    {
      "name": "string",
      "display_name": "string",
      "type": "string",
      "comment": "string",
      "mapped_field": "string",
      "index": true,
      "fulltext_config": {
        "analyzer": "standard",
        "field_keyword": true
      },
      "vector_config": {
        "dimension": 0
      }
    }
  ],
  "logic_properties": [
    {
      "name": "string",
      "display_name": "string",
      "type": "string",
      "comment": "string",
      "index": true,
      "data_source": {
        "type": "data_view",
        "id": "string",
        "name": "string"
      },
      "parameters": [
        {
          "name": "string",
          "value_from": "property",
          "value": "string"
        }
      ]
    }
  ],
  "primary_keys": [
    "string"
  ],
  "display_key": "string",
  "creator": "string",
  "create_time": 0,
  "updater": "string",
  "update_time": 0,
  "detail": "string"
}

```

节点（对象类）信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|concept_type|string|true|none|概念类型|
|id|string|true|none|对象类ID|
|name|string|true|none|对象类名称|
|tags|[string]|true|none|标签。 （可以为空）|
|comment|string|true|none|备注（可以为空）|
|icon|string|true|none|图标|
|color|string|true|none|颜色|
|branch|string|true|none|分支ID|
|kn_id|string|true|none|业务知识网络id|
|concept_groups|[[ConceptGroup](#schemaconceptgroup)]|true|none|概念分组id|
|data_source|[DataSource](#schemadatasource)|true|none|数据来源|
|data_properties|[[DataProperty](#schemadataproperty)]|true|none|数据属性|
|logic_properties|[[LogicProperty](#schemalogicproperty)]|true|none|逻辑属性|
|primary_keys|[string]|true|none|主键|
|display_key|string|true|none|对象实例的显示属性|
|creator|string|false|none|创建人ID|
|create_time|integer(int64)|false|none|创建时间|
|updater|string|false|none|最近一次修改人|
|update_time|integer(int64)|false|none|最近一次更新时间|
|detail|string|false|none|说明书。按需返回，若指定了include_detail=true，则返回，否则不返回|

#### Enumerated Values

|Property|Value|
|---|---|
|concept_type|object_type|

<h2 id="tocS_RelationTypeDetail">RelationTypeDetail</h2>
<!-- backwards compatibility -->
<a id="schemarelationtypedetail"></a>
<a id="schema_RelationTypeDetail"></a>
<a id="tocSrelationtypedetail"></a>
<a id="tocsrelationtypedetail"></a>

```json
{
  "concept_type": "relation_type",
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "kn_id": "string",
  "concept_groups": [
    {
      "id": "string",
      "name": "string"
    }
  ],
  "source_object_type_id": "string",
  "source_object_type_name": "string",
  "target_object_type_id": "string",
  "target_object_type_name": "string",
  "type": "direct",
  "mapping_rules": {},
  "creator": "string",
  "create_time": 0,
  "updater": "string",
  "update_time": 0,
  "detail": "string"
}

```

关系类

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|concept_type|string|true|none|概念类型|
|id|string|true|none|关系类ID|
|name|string|true|none|关系类名称|
|tags|[string]|true|none|标签。 （可以为空）|
|comment|string|true|none|备注（可以为空）|
|icon|string|true|none|图标|
|color|string|true|none|颜色|
|branch|string|true|none|分支ID|
|kn_id|string|true|none|业务知识网络ID|
|concept_groups|[[ConceptGroup](#schemaconceptgroup)]|true|none|概念分组|
|source_object_type_id|string|true|none|起点象类ID|
|source_object_type_name|string|true|none|起点象类名称|
|target_object_type_id|string|true|none|终点对象类ID|
|target_object_type_name|string|true|none|终点对象类名称|
|type|string|true|none|关系类型|
|mapping_rules|any|true|none|关联的匹配规则。direct 为键映射；data_view 参考 DataViewMappingRule；filtered_cross_join（FCJ）参考 FilteredCrossJoinMappingRule。直接关联时是一个 map，标记的是起点对象匹配属性1: 终点对象匹配属性1|

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[Object](#schemaobject)|false|none|json，字段不定|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[DataViewMappingRule](#schemadataviewmappingrule)|false|none|关系类型为 data_view 时的匹配规则|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[FilteredCrossJoinMappingRule](#schemafilteredcrossjoinmappingrule)|false|none|关系类型为 `filtered_cross_join`（分侧过滤全连接，FCJ）时的匹配规则。无数据视图与键映射；<br>`source_condition` / `target_condition` 为可选的实例过滤条件（结构与对象实例查询 Condition 一致，参见 ontology-query 等查询 API）。<br>两侧均可省略，或 `mapping_rules` 为 `{}`（表示两侧无额外过滤）。|

continued

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|creator|string|false|none|创建人ID|
|create_time|integer(int64)|false|none|创建时间|
|updater|string|false|none|最近一次修改人|
|update_time|integer(int64)|false|none|最近一次更新时间|
|detail|string|false|none|说明书。按需返回，若指定了include_detail=true，则返回，否则不返回|

#### Enumerated Values

|Property|Value|
|---|---|
|concept_type|relation_type|
|type|direct|
|type|data_view|
|type|filtered_cross_join|

<h2 id="tocS_Statistics">Statistics</h2>
<!-- backwards compatibility -->
<a id="schemastatistics"></a>
<a id="schema_Statistics"></a>
<a id="tocSstatistics"></a>
<a id="tocsstatistics"></a>

```json
{
  "object_types_total": 0,
  "relation_types_total": 0,
  "action_types_total": 0
}

```

概念统计信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_types_total|integer|false|none|对象类数量|
|relation_types_total|integer|false|none|关系类数量|
|action_types_total|integer|false|none|行动类数量|

<h2 id="tocS_ConceptGroupInfo">ConceptGroupInfo</h2>
<!-- backwards compatibility -->
<a id="schemaconceptgroupinfo"></a>
<a id="schema_ConceptGroupInfo"></a>
<a id="tocSconceptgroupinfo"></a>
<a id="tocsconceptgroupinfo"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "kn_id": "string",
  "branch": "string",
  "creator": "string",
  "create_time": 0,
  "updator": "string",
  "update_time": 0,
  "detail": "string"
}

```

概念分组自身的信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|false|none|ID|
|name|string|false|none|名称|
|tags|[string]|false|none|标签，可为空|
|comment|string|false|none|备注，可为空|
|icon|string|false|none|图标|
|color|string|false|none|颜色|
|kn_id|string|false|none|业务知识网络ID|
|branch|string|false|none|分支ID|
|creator|string|false|none|创建人ID|
|create_time|integer(int64)|false|none|创建时间|
|updator|string|false|none|最近一次修改人|
|update_time|integer(int64)|false|none|最近一次更新时间|
|detail|string|false|none|说明书。|

<h2 id="tocS_KnowledgeNetworkDetail">KnowledgeNetworkDetail</h2>
<!-- backwards compatibility -->
<a id="schemaknowledgenetworkdetail"></a>
<a id="schema_KnowledgeNetworkDetail"></a>
<a id="tocSknowledgenetworkdetail"></a>
<a id="tocsknowledgenetworkdetail"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "creator": "string",
  "create_time": 0,
  "updater": "string",
  "update_time": 0,
  "detail": "string",
  "statistics": {
    "object_types_total": 0,
    "relation_types_total": 0,
    "action_types_total": 0
  },
  "concept_groups": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "kn_id": "string",
      "branch": "string",
      "creator": "string",
      "create_time": 0,
      "updator": "string",
      "update_time": 0,
      "detail": "string"
    }
  ],
  "object_types": [
    {
      "concept_type": "object_type",
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "concept_groups": [
        {
          "id": "string",
          "name": "string"
        }
      ],
      "data_source": {
        "type": "data_view",
        "id": "string",
        "name": "string"
      },
      "data_properties": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string",
          "mapped_field": "string",
          "index": true,
          "fulltext_config": {
            "analyzer": "standard",
            "field_keyword": true
          },
          "vector_config": {
            "dimension": 0
          }
        }
      ],
      "logic_properties": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string",
          "index": true,
          "data_source": {
            "type": "data_view",
            "id": "string",
            "name": "string"
          },
          "parameters": [
            {
              "name": "string",
              "value_from": "property",
              "value": "string"
            }
          ]
        }
      ],
      "primary_keys": [
        "string"
      ],
      "display_key": "string",
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string"
    }
  ],
  "relation_types": [
    {
      "concept_type": "relation_type",
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "concept_groups": [
        {
          "id": "string",
          "name": "string"
        }
      ],
      "source_object_type_id": "string",
      "source_object_type_name": "string",
      "target_object_type_id": "string",
      "target_object_type_name": "string",
      "type": "direct",
      "mapping_rules": {},
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string"
    }
  ],
  "action_types": [
    {
      "concept_type": "action_type",
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "concept_groups": [
        {
          "id": "string",
          "name": "string"
        }
      ],
      "action_type": "add",
      "action_intent": "add",
      "object_type_id": "string",
      "object_type_name": "string",
      "condition": {
        "object_type_id": "string",
        "field": "string",
        "operation": "and",
        "sub_conditions": [
          {}
        ],
        "value": null,
        "value_from": "const"
      },
      "affect": {
        "object_type": "string",
        "comment": "string",
        "expected_operation": "add",
        "affected_fields": [
          "string"
        ]
      },
      "impact_contracts": [
        {
          "object_type_id": "string",
          "expected_operation": "add",
          "description": "string",
          "affected_fields": [
            "string"
          ]
        }
      ],
      "action_source": {
        "type": "data_view",
        "id": "string",
        "name": "string"
      },
      "parameters": [
        {
          "name": "string",
          "value_from": "property",
          "value": "string"
        }
      ],
      "schedule": {
        "type": "FIX_RATE",
        "expression": "string"
      },
      "detail": "string"
    }
  ]
}

```

业务知识网络详情

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|业务知识网络ID|
|name|string|true|none|业务知识网络名称|
|tags|[string]|true|none|标签，可为空|
|comment|string|true|none|备注，可为空|
|icon|string|true|none|图标|
|color|string|true|none|颜色|
|branch|string|true|none|分支ID|
|creator|string|true|none|创建人ID|
|create_time|integer(int64)|true|none|创建时间|
|updater|string|true|none|最近一次修改人|
|update_time|integer(int64)|true|none|最近一次更新时间|
|detail|string|true|none|说明书。|
|statistics|[Statistics](#schemastatistics)|false|none|概念统计信息。当include_statistics为true时才计算概念统计信息|
|concept_groups|[[ConceptGroupInfo](#schemaconceptgroupinfo)]|false|none|概念分组信息|
|object_types|[[ObjectTypeDetail](#schemaobjecttypedetail)]|false|none|对象类|
|relation_types|[[RelationTypeDetail](#schemarelationtypedetail)]|false|none|关系类|
|action_types|[[ActionTypeDetail](#schemaactiontypedetail)]|false|none|行动类|



<!-- Generator: Widdershins v4.0.1 -->

<h1 id="conceptgroup">ConceptGroup v0.1.0</h1>


概念分组管理接口

<h1 id="conceptgroup-default">Default</h1>

## 获取概念分组详情

`GET /api/bkn-backend/v1/knowledge-networks/{kn_id}/concept-groups/{group_id}`

<h3 id="获取概念分组详情-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|undefined|true|none|
|group_id|path|undefined|true|none|
|branch|query|string|false|分支，不填是则使用 main 分支|

> Example responses

> 200 Response

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "kn_id": "string",
  "branch": "string",
  "object_types": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "concept_groups": [
        {
          "id": "string",
          "name": "string"
        }
      ],
      "data_source": {
        "type": "data_view",
        "id": "string",
        "name": "string"
      },
      "data_properties": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string",
          "mapped_field": {
            "name": "string",
            "display_name": "string",
            "type": "string"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 76
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "string"
          ]
        }
      ],
      "logic_properties": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string",
          "index": true,
          "data_source": {
            "type": "metric",
            "id": "string",
            "name": "string"
          },
          "parameters": [
            {
              "name": "string",
              "type": "string",
              "source": "string",
              "value_from": "property",
              "value": "string"
            }
          ]
        }
      ],
      "primary_keys": [
        "string"
      ],
      "display_key": "string",
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string",
      "module_type": "object_type"
    }
  ],
  "relation_types": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "source_object_type_id": "string",
      "source_object_type": {
        "id": "string",
        "name": "string",
        "icon": "string",
        "color": "string"
      },
      "target_object_type_id": "string",
      "target_object_type": {
        "id": "string",
        "name": "string",
        "icon": "string",
        "color": "string"
      },
      "type": "direct",
      "mapping_rules": [
        {
          "target_property": {
            "name": "string",
            "display_name": "string"
          },
          "source_property": {
            "name": "string",
            "display_name": "string"
          }
        }
      ],
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string"
    }
  ],
  "action_types": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "action_type": "add",
      "action_intent": "add",
      "object_type_id": "string",
      "object_type": {
        "id": "string",
        "name": "string",
        "icon": "string",
        "color": "string"
      },
      "condition": {
        "object_type_id": "string",
        "field": "string",
        "operation": "and",
        "sub_conditions": [
          {}
        ],
        "value": null,
        "value_from": "const"
      },
      "affect": {
        "comment": "string",
        "object_type_id": "string",
        "object_type": {
          "id": "string",
          "name": "string",
          "icon": "string",
          "color": "string"
        }
      },
      "impact_contracts": [
        {
          "object_type_id": "string",
          "expected_operation": "add",
          "description": "string",
          "affected_fields": [
            "string"
          ]
        }
      ],
      "action_source": {
        "type": "tool",
        "box_id": "string",
        "tool_id": "string"
      },
      "parameters": [
        {
          "name": "string",
          "type": "string",
          "source": "string",
          "value_from": "property",
          "value": "string"
        }
      ],
      "schedule": {
        "type": "FIX_RATE",
        "expression": "string"
      },
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string",
      "module_type": "action_type"
    }
  ],
  "creator": "string",
  "create_time": 0,
  "updator": "string",
  "update_time": 0,
  "detail": "string"
}
```

<h3 id="获取概念分组详情-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[ConceptGroupDetail](#schemaconceptgroupdetail)|

<aside class="success">
This operation does not require authentication
</aside>

## 修改概念分组

`PUT /api/bkn-backend/v1/knowledge-networks/{kn_id}/concept-groups/{group_id}`

> Body parameter

```json
{
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string"
}
```

<h3 id="修改概念分组-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|undefined|true|none|
|group_id|path|undefined|true|none|
|branch|query|string|false|分支，不填则使用 main 分支|
|strict_mode|query|boolean|false|是否严格校验依赖，默认为 true。为 true 时校验概念分组内声明的对象类引用等依赖是否存在；为 false 时不做该校验|
|validate_dependency|query|boolean|false|[已废弃] 请使用 strict_mode。兼容保留，strict_mode 为空时会读取此参数|
|body|body|[UpdateConceptGroup](#schemaupdateconceptgroup)|true|none|

<h3 id="修改概念分组-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|ok|None|

<aside class="success">
This operation does not require authentication
</aside>

## 删除概念分组，单个删除

`DELETE /api/bkn-backend/v1/knowledge-networks/{kn_id}/concept-groups/{group_id}`

<h3 id="删除概念分组，单个删除-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|undefined|true|none|
|group_id|path|undefined|true|none|

<h3 id="删除概念分组，单个删除-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|ok|None|

<aside class="success">
This operation does not require authentication
</aside>

## 获取概念分组列表

`GET /api/bkn-backend/v1/knowledge-networks/{kn_id}/concept-groups`

<h3 id="获取概念分组列表-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|name_pattern|query|string|false|根据络名称模糊查询，默认为空|
|sort|query|string|false|排序类型，默认是update_time|
|direction|query|string|false|排序结果方向，可选asc、desc。|
|offset|query|integer(int64)|false|开始响应的项目的偏移量	|
|limit|query|integer(int64)|false|每页最多可返回的项目数；|
|tag|query|string|false|根据标签精准查询，默认为空.|

#### Detailed descriptions

**direction**: 排序结果方向，可选asc、desc。
默认desc

**offset**: 开始响应的项目的偏移量	
范围需大于等于0，默认值0

**limit**: 每页最多可返回的项目数；
分页可选1-1000，-1表示不分页；
默认值10

#### Enumerated Values

|Parameter|Value|
|---|---|
|sort|update_time|
|sort|name|
|direction|asc|
|direction|desc|

> Example responses

> 200 Response

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "statistics": {
        "object_types_total": 0,
        "relation_types_total": 0,
        "action_types_total": 0
      },
      "creator": "string",
      "create_time": 0,
      "updator": "string",
      "update_time": 0,
      "detail": "string"
    }
  ],
  "total_count": 0
}
```

<h3 id="获取概念分组列表-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[ListConceptGroups](#schemalistconceptgroups)|

<aside class="success">
This operation does not require authentication
</aside>

## 创建概念分组

`POST /api/bkn-backend/v1/knowledge-networks/{kn_id}/concept-groups`

创建概念分组

> Body parameter

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string"
}
```

<h3 id="创建概念分组-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|branch|query|string|false|分支，不填是则使用 main 分支|
|import_mode|query|string|false|导入模式，可选normal、ignore、overwrite，默认为normal|
|strict_mode|query|boolean|false|是否严格校验依赖，默认为true。为true时，需校验对象类的视图、关系类的视图等依赖是否存在；为false时，依赖不存在不报错|
|validate_dependency|query|boolean|false|[已废弃] 请使用 strict_mode。兼容保留，strict_mode 为空时会读取此参数|
|body|body|[ReqConceptGroup](#schemareqconceptgroup)|true|none|
|kn_id|path|string|true|业务知识网络ID|

#### Enumerated Values

|Parameter|Value|
|---|---|
|import_mode|normal|
|import_mode|ignore|
|import_mode|overwrite|

> Example responses

> 201 Response

```json
{
  "id": "string"
}
```

<h3 id="创建概念分组-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|201|[Created](https://tools.ietf.org/html/rfc7231#section-6.3.2)|新增/导入成功|[ID](#schemaid)|

<aside class="success">
This operation does not require authentication
</aside>

## 添加对象类

`POST /api/bkn-backend/v1/knowledge-networks/{kn_id}/concept-groups/{group_id}/object-types`

> Body parameter

```json
{
  "entries": [
    {
      "id": "string"
    }
  ]
}
```

<h3 id="添加对象类-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|undefined|true|none|
|group_id|path|undefined|true|none|
|branch|query|string|false|分支，不填则使用 main 分支|
|strict_mode|query|boolean|false|是否严格校验依赖，默认为true。为true时，需校验对象类ID是否存在；为false时，对象类不存在不报错|
|validate_dependency|query|boolean|false|[已废弃] 请使用 strict_mode。兼容保留，strict_mode 为空时会读取此参数|
|body|body|[AddObjectTypeToGroup](#schemaaddobjecttypetogroup)|true|none|

<h3 id="添加对象类-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|201|[Created](https://tools.ietf.org/html/rfc7231#section-6.3.2)|添加成功|None|

<aside class="success">
This operation does not require authentication
</aside>

## 移除对象类

`DELETE /api/bkn-backend/v1/knowledge-networks/{kn_id}/concept-groups/{group_id}/object-types/{ot_ids}`

<h3 id="移除对象类-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|undefined|true|none|
|group_id|path|undefined|true|none|
|ot_ids|path|undefined|true|none|

<h3 id="移除对象类-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|移除成功|None|

<aside class="success">
This operation does not require authentication
</aside>

## 校验概念分组

`POST /api/bkn-backend/v1/knowledge-networks/{kn_id}/concept-groups/validation`

仅校验概念分组依赖存在性，不写库。校验分组内嵌套对象类、关系类、行动类及其依赖。
同批概念 ID 从本次请求的 concept_groups 树（含嵌套桶）收集，strict 下可与落库路径一致地解析尚未落库的互相引用。

**响应**：HTTP 200 时 `valid`/`detail` 语义同知识网络校验接口；参数与鉴权错误为非 2xx。

**内部接口**：同路径语义，`POST /api/bkn-backend/in/v1/.../concept-groups/validation` 与 `POST /api/ontology-manager/in/v1/.../concept-groups/validation`；Header 解析访问者，无 OAuth。

> Body parameter

```json
{
  "entries": [
    {}
  ]
}
```

<h3 id="校验概念分组-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|branch|query|string|false|分支，不填则使用 main 分支|
|strict_mode|query|boolean|false|是否严格校验依赖，默认为 true|
|import_mode|query|string|false|与创建概念分组接口一致；用于概念分组 ID/名称与落库冲突的校验语义（normal / ignore / overwrite）。|
|body|body|object|true|none|
|» entries|body|[object]|false|待校验的概念分组列表，结构与创建接口一致|

#### Enumerated Values

|Parameter|Value|
|---|---|
|import_mode|normal|
|import_mode|ignore|
|import_mode|overwrite|

> Example responses

> 200 Response

```json
{
  "valid": true,
  "detail": "string"
}
```

<h3 id="校验概念分组-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|已返回校验结果（通过与否均可能为 200）|Inline|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数错误等；业务校验未通过见 200 + valid:false|None|

<h3 id="校验概念分组-responseschema">Response Schema</h3>

Status Code **200**

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» valid|boolean|true|none|none|
|» detail|string|false|none|当 valid 为 false 时的说明（error.Error()）|

<aside class="success">
This operation does not require authentication
</aside>

# Schemas

<h2 id="tocS_ReqConceptGroup">ReqConceptGroup</h2>
<!-- backwards compatibility -->
<a id="schemareqconceptgroup"></a>
<a id="schema_ReqConceptGroup"></a>
<a id="tocSreqconceptgroup"></a>
<a id="tocsreqconceptgroup"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string"
}

```

概念分组信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|false|none|ID.新建后不可修改，只能包含小写英文字母、数字、下划线（_）、连字符（-），且不能以下划线和连字符开头|
|name|string|true|none|名称|
|tags|[string]|false|none|标签。用于业务标识|
|comment|string|false|none|备注|
|icon|string|false|none|图标|
|color|string|false|none|颜色|
|branch|string|true|none|分支ID|

<h2 id="tocS_ID">ID</h2>
<!-- backwards compatibility -->
<a id="schemaid"></a>
<a id="schema_ID"></a>
<a id="tocSid"></a>
<a id="tocsid"></a>

```json
{
  "id": "string"
}

```

id

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|id|

<h2 id="tocS_ListConceptGroups">ListConceptGroups</h2>
<!-- backwards compatibility -->
<a id="schemalistconceptgroups"></a>
<a id="schema_ListConceptGroups"></a>
<a id="tocSlistconceptgroups"></a>
<a id="tocslistconceptgroups"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "statistics": {
        "object_types_total": 0,
        "relation_types_total": 0,
        "action_types_total": 0
      },
      "creator": "string",
      "create_time": 0,
      "updator": "string",
      "update_time": 0,
      "detail": "string"
    }
  ],
  "total_count": 0
}

```

概念分组列表

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[ConceptGroup](#schemaconceptgroup)]|true|none|条目列表|
|total_count|integer|true|none|总条数|

<h2 id="tocS_ConceptGroup">ConceptGroup</h2>
<!-- backwards compatibility -->
<a id="schemaconceptgroup"></a>
<a id="schema_ConceptGroup"></a>
<a id="tocSconceptgroup"></a>
<a id="tocsconceptgroup"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "statistics": {
    "object_types_total": 0,
    "relation_types_total": 0,
    "action_types_total": 0
  },
  "creator": "string",
  "create_time": 0,
  "updator": "string",
  "update_time": 0,
  "detail": "string"
}

```

列表页的概念分组

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|ID|
|name|string|true|none|名称|
|tags|[string]|true|none|标签，可为空|
|comment|string|true|none|备注，可为空|
|icon|string|true|none|图标|
|color|string|true|none|颜色|
|branch|string|true|none|分支ID|
|statistics|[Statistics](#schemastatistics)|true|none|概念统计信息|
|creator|string|true|none|创建人ID|
|create_time|integer(int64)|true|none|创建时间|
|updator|string|true|none|最近一次修改人|
|update_time|integer(int64)|true|none|最近一次更新时间|
|detail|string|true|none|说明书。|

<h2 id="tocS_Statistics">Statistics</h2>
<!-- backwards compatibility -->
<a id="schemastatistics"></a>
<a id="schema_Statistics"></a>
<a id="tocSstatistics"></a>
<a id="tocsstatistics"></a>

```json
{
  "object_types_total": 0,
  "relation_types_total": 0,
  "action_types_total": 0
}

```

概念统计信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_types_total|integer|true|none|对象类数量|
|relation_types_total|integer|true|none|关系类数量|
|action_types_total|integer|true|none|行动类数量|

<h2 id="tocS_DataSource">DataSource</h2>
<!-- backwards compatibility -->
<a id="schemadatasource"></a>
<a id="schema_DataSource"></a>
<a id="tocSdatasource"></a>
<a id="tocsdatasource"></a>

```json
{
  "type": "data_view",
  "id": "string",
  "name": "string"
}

```

资源来源

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|type|string|true|none|数据来源类型|
|id|string|true|none|数据来源ID|
|name|string|false|none|名称。查看详情时返回。|

#### Enumerated Values

|Property|Value|
|---|---|
|type|data_view|

<h2 id="tocS_ExpectedImpactOperation">ExpectedImpactOperation</h2>
<!-- backwards compatibility -->
<a id="schemaexpectedimpactoperation"></a>
<a id="schema_ExpectedImpactOperation"></a>
<a id="tocSexpectedimpactoperation"></a>
<a id="tocsexpectedimpactoperation"></a>

```json
"add"

```

与 `action_intent` / `action_type` 一致。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|string|false|none|与 `action_intent` / `action_type` 一致。|

#### Enumerated Values

|Property|Value|
|---|---|
|*anonymous*|add|
|*anonymous*|modify|
|*anonymous*|delete|

<h2 id="tocS_ImpactContractItem">ImpactContractItem</h2>
<!-- backwards compatibility -->
<a id="schemaimpactcontractitem"></a>
<a id="schema_ImpactContractItem"></a>
<a id="tocSimpactcontractitem"></a>
<a id="tocsimpactcontractitem"></a>

```json
{
  "object_type_id": "string",
  "expected_operation": "add",
  "description": "string",
  "affected_fields": [
    "string"
  ]
}

```

行动影响契约单条（与 `action-type.yaml`、`f_impact_contracts` 一致）。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_type_id|string|false|none|none|
|expected_operation|[ExpectedImpactOperation](#schemaexpectedimpactoperation)|false|none|与 `action_intent` / `action_type` 一致。|
|description|string|false|none|none|
|affected_fields|[string]|false|none|none|

<h2 id="tocS_ActionTypeDetail">ActionTypeDetail</h2>
<!-- backwards compatibility -->
<a id="schemaactiontypedetail"></a>
<a id="schema_ActionTypeDetail"></a>
<a id="tocSactiontypedetail"></a>
<a id="tocsactiontypedetail"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "kn_id": "string",
  "action_type": "add",
  "action_intent": "add",
  "object_type_id": "string",
  "object_type": {
    "id": "string",
    "name": "string",
    "icon": "string",
    "color": "string"
  },
  "condition": {
    "object_type_id": "string",
    "field": "string",
    "operation": "and",
    "sub_conditions": [
      {}
    ],
    "value": null,
    "value_from": "const"
  },
  "affect": {
    "comment": "string",
    "object_type_id": "string",
    "object_type": {
      "id": "string",
      "name": "string",
      "icon": "string",
      "color": "string"
    }
  },
  "impact_contracts": [
    {
      "object_type_id": "string",
      "expected_operation": "add",
      "description": "string",
      "affected_fields": [
        "string"
      ]
    }
  ],
  "action_source": {
    "type": "tool",
    "box_id": "string",
    "tool_id": "string"
  },
  "parameters": [
    {
      "name": "string",
      "type": "string",
      "source": "string",
      "value_from": "property",
      "value": "string"
    }
  ],
  "schedule": {
    "type": "FIX_RATE",
    "expression": "string"
  },
  "creator": "string",
  "create_time": 0,
  "updater": "string",
  "update_time": 0,
  "detail": "string",
  "module_type": "action_type"
}

```

行动类

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|行动类ID|
|name|string|true|none|行动类名称|
|tags|[string]|true|none|标签。 （可以为空）|
|comment|string|true|none|备注（可以为空）|
|icon|string|true|none|图标|
|color|string|true|none|颜色|
|branch|string|true|none|分支ID|
|kn_id|string|true|none|业务知识网络ID|
|action_type|string|true|none|**[已废弃]** 等价于 `action_intent`（add/modify/delete）。|
|action_intent|string|false|none|与 `ActionType.action_type` 回填一致的首选字段。|
|object_type_id|string|true|none|行动类所绑定的对象类ID|
|object_type|[SimpleObjectType](#schemasimpleobjecttype)|true|none|行动类所绑定的对象类名称.|
|condition|[ActionCondition](#schemaactioncondition)|true|none|行动条件|
|affect|[AffectDetail](#schemaaffectdetail)|true|none|**[已废弃]** 单行影响；等价信息见 `impact_contracts`。|
|impact_contracts|[[ImpactContractItem](#schemaimpactcontractitem)]|false|none|[行动影响契约单条（与 `action-type.yaml`、`f_impact_contracts` 一致）。]|
|action_source|[ToolSource](#schematoolsource)|true|none|绑定的行动的资源|
|parameters|[[Parameter](#schemaparameter)]|true|none|行动资源参数|
|schedule|[Schedule](#schemaschedule)|true|none|行动监听参数配置|
|creator|string|true|none|创建人ID|
|create_time|integer(int64)|true|none|创建时间|
|updater|string|true|none|最近一次修改人|
|update_time|integer(int64)|true|none|最近一次更新时间|
|detail|string|true|none|说明书。按需返回，若指定了include_detail=true，则返回，否则不返回|
|module_type|string|true|none|模块类型|

#### Enumerated Values

|Property|Value|
|---|---|
|action_type|add|
|action_type|modify|
|action_type|delete|
|action_intent|add|
|action_intent|modify|
|action_intent|delete|
|module_type|action_type|

<h2 id="tocS_ToolSource">ToolSource</h2>
<!-- backwards compatibility -->
<a id="schematoolsource"></a>
<a id="schema_ToolSource"></a>
<a id="tocStoolsource"></a>
<a id="tocstoolsource"></a>

```json
{
  "type": "tool",
  "box_id": "string",
  "tool_id": "string"
}

```

数据来源

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|type|string|true|none|资源类型|
|box_id|string|true|none|工具箱ID|
|tool_id|string|true|none|工具ID|

#### Enumerated Values

|Property|Value|
|---|---|
|type|tool|

<h2 id="tocS_AffectDetail">AffectDetail</h2>
<!-- backwards compatibility -->
<a id="schemaaffectdetail"></a>
<a id="schema_AffectDetail"></a>
<a id="tocSaffectdetail"></a>
<a id="tocsaffectdetail"></a>

```json
{
  "comment": "string",
  "object_type_id": "string",
  "object_type": {
    "id": "string",
    "name": "string",
    "icon": "string",
    "color": "string"
  }
}

```

**[已废弃]** 请消费 `impact_contracts`。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|comment|string|true|none|影响描述|
|object_type_id|string|true|none|影响的对象类ID|
|object_type|[SimpleObjectType](#schemasimpleobjecttype)|true|none|对象类信息|

<h2 id="tocS_SimpleObjectType">SimpleObjectType</h2>
<!-- backwards compatibility -->
<a id="schemasimpleobjecttype"></a>
<a id="schema_SimpleObjectType"></a>
<a id="tocSsimpleobjecttype"></a>
<a id="tocssimpleobjecttype"></a>

```json
{
  "id": "string",
  "name": "string",
  "icon": "string",
  "color": "string"
}

```

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|对象类id|
|name|string|true|none|对象类名称|
|icon|string|true|none|对象类图标|
|color|string|true|none|对象类颜色|

<h2 id="tocS_ActionCondition">ActionCondition</h2>
<!-- backwards compatibility -->
<a id="schemaactioncondition"></a>
<a id="schema_ActionCondition"></a>
<a id="tocSactioncondition"></a>
<a id="tocsactioncondition"></a>

```json
{
  "object_type_id": "string",
  "field": "string",
  "operation": "and",
  "sub_conditions": [
    {
      "object_type_id": "string",
      "field": "string",
      "operation": "and",
      "sub_conditions": [],
      "value": null,
      "value_from": "const"
    }
  ],
  "value": null,
  "value_from": "const"
}

```

行动条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_type_id|string|false|none|对象类ID。当时多个对象类的过滤时，需要把对象类ID带上，否则只要属性名属于对象类就会进行过滤。|
|field|string|false|none|字段名称，也即对象类的属性名称|
|operation|string|false|none|操作符|
|sub_conditions|[[ActionCondition](#schemaactioncondition)]|false|none|子过滤条件|
|value|any|false|none|字段值|
|value_from|string|false|none|字段值来源，当前仅支持 "const"|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|and|
|operation|or|
|operation|==|
|operation|!=|
|operation|>|
|operation|>=|
|operation|<|
|operation|<=|
|operation|in|
|operation|not_in|
|operation|range|
|operation|out_range|
|operation|exist|
|operation|not_exist|
|value_from|const|

<h2 id="tocS_Parameter">Parameter</h2>
<!-- backwards compatibility -->
<a id="schemaparameter"></a>
<a id="schema_Parameter"></a>
<a id="tocSparameter"></a>
<a id="tocsparameter"></a>

```json
{
  "name": "string",
  "type": "string",
  "source": "string",
  "value_from": "property",
  "value": "string"
}

```

工具参数

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|参数名称|
|type|string|false|none|参数类型|
|source|string|false|none|参数来源|
|value_from|string|true|none|值来源|
|value|string|false|none|参数值。value_from=property时，填入的是对象类的数据属性名称；value_from=input时，不设置此字段|

#### Enumerated Values

|Property|Value|
|---|---|
|value_from|property|
|value_from|input|
|value_from|const|

<h2 id="tocS_Schedule">Schedule</h2>
<!-- backwards compatibility -->
<a id="schemaschedule"></a>
<a id="schema_Schedule"></a>
<a id="tocSschedule"></a>
<a id="tocsschedule"></a>

```json
{
  "type": "FIX_RATE",
  "expression": "string"
}

```

执行频率配置项

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|type|string|true|none|执行类型。枚举，支持配置固定频率(FIX_RATE)和配置crontab表达式（CRON）|
|expression|string|true|none|执行表达式。<br><br>1.固定频率指以固定周期执行持久化，frequency=< time_durations >，用一个数字，后面跟时间单位来定义。时间单位可以是如下之一：m - 分钟； h - 小时； d - 天|

#### Enumerated Values

|Property|Value|
|---|---|
|type|FIX_RATE|
|type|CRON|

<h2 id="tocS_ObjectTypeDetail">ObjectTypeDetail</h2>
<!-- backwards compatibility -->
<a id="schemaobjecttypedetail"></a>
<a id="schema_ObjectTypeDetail"></a>
<a id="tocSobjecttypedetail"></a>
<a id="tocsobjecttypedetail"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "kn_id": "string",
  "concept_groups": [
    {
      "id": "string",
      "name": "string"
    }
  ],
  "data_source": {
    "type": "data_view",
    "id": "string",
    "name": "string"
  },
  "data_properties": [
    {
      "name": "string",
      "display_name": "string",
      "type": "string",
      "comment": "string",
      "mapped_field": {
        "name": "string",
        "display_name": "string",
        "type": "string"
      },
      "index_config": {
        "keyword_config": {
          "enabled": true,
          "ignore_above_len": 76
        },
        "fulltext_config": {
          "analyzer": "standard",
          "enabled": true
        },
        "vector_config": {
          "enabled": true,
          "model_id": "some text"
        }
      },
      "condition_operations": [
        "string"
      ]
    }
  ],
  "logic_properties": [
    {
      "name": "string",
      "display_name": "string",
      "type": "string",
      "comment": "string",
      "index": true,
      "data_source": {
        "type": "metric",
        "id": "string",
        "name": "string"
      },
      "parameters": [
        {
          "name": "string",
          "type": "string",
          "source": "string",
          "value_from": "property",
          "value": "string"
        }
      ]
    }
  ],
  "primary_keys": [
    "string"
  ],
  "display_key": "string",
  "creator": "string",
  "create_time": 0,
  "updater": "string",
  "update_time": 0,
  "detail": "string",
  "module_type": "object_type"
}

```

节点（对象类）信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|对象类ID|
|name|string|true|none|对象类名称|
|tags|[string]|true|none|标签。 （可以为空）|
|comment|string|true|none|备注（可以为空）|
|icon|string|true|none|图标|
|color|string|true|none|颜色|
|branch|string|true|none|分支ID|
|kn_id|string|true|none|业务知识网络id|
|concept_groups|[[SimpleConceptGroup](#schemasimpleconceptgroup)]|true|none|概念分组id|
|data_source|[DataSource](#schemadatasource)|true|none|数据来源|
|data_properties|[[DataProperty](#schemadataproperty)]|true|none|数据属性|
|logic_properties|[[LogicProperty](#schemalogicproperty)]|true|none|逻辑属性|
|primary_keys|[string]|true|none|主键|
|display_key|string|true|none|对象实例的显示属性|
|creator|string|true|none|创建人ID|
|create_time|integer(int64)|true|none|创建时间|
|updater|string|true|none|最近一次修改人|
|update_time|integer(int64)|true|none|最近一次更新时间|
|detail|string|true|none|说明书。按需返回，若指定了include_detail=true，则返回，否则不返回。列表查询时不返回此字段|
|module_type|string|true|none|模块类型|

#### Enumerated Values

|Property|Value|
|---|---|
|module_type|object_type|

<h2 id="tocS_DataProperty">DataProperty</h2>
<!-- backwards compatibility -->
<a id="schemadataproperty"></a>
<a id="schema_DataProperty"></a>
<a id="tocSdataproperty"></a>
<a id="tocsdataproperty"></a>

```json
{
  "name": "string",
  "display_name": "string",
  "type": "string",
  "comment": "string",
  "mapped_field": {
    "name": "string",
    "display_name": "string",
    "type": "string"
  },
  "index_config": {
    "keyword_config": {
      "enabled": true,
      "ignore_above_len": 76
    },
    "fulltext_config": {
      "analyzer": "standard",
      "enabled": true
    },
    "vector_config": {
      "enabled": true,
      "model_id": "some text"
    }
  },
  "condition_operations": [
    "string"
  ]
}

```

数据属性

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|属性名称。只能包含小写英文字母、数字、下划线（_）、连字符（-），且不能以下划线和连字符开头|
|display_name|string|true|none|属性显示名|
|type|string|true|none|属性数据类型。除了视图的字段类型之外，还有 metric、objective、event、trace、log、operator|
|comment|string|false|none|属性描述|
|mapped_field|[ViewField](#schemaviewfield)|false|none|属性映射到数据来源中的字段名|
|index_config|[IndexConfig](#schemaindexconfig)|false|none|索引配置|
|condition_operations|[string]|false|none|字符串类型的属性能支持的过滤条件。字符串类型有string, text。|

<h2 id="tocS_IndexConfig">IndexConfig</h2>
<!-- backwards compatibility -->
<a id="schemaindexconfig"></a>
<a id="schema_IndexConfig"></a>
<a id="tocSindexconfig"></a>
<a id="tocsindexconfig"></a>

```json
{
  "keyword_config": {
    "enabled": true,
    "ignore_above_len": 76
  },
  "fulltext_config": {
    "analyzer": "standard",
    "enabled": true
  },
  "vector_config": {
    "enabled": true,
    "model_id": "some text"
  }
}

```

Root Type for IndexConfig

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|keyword_config|[KeywordConfig](#schemakeywordconfig)|false|none|关键字检索配置|
|fulltext_config|[FulltextConfig](#schemafulltextconfig)|false|none|全文检索配置|
|vector_config|[VectorConfig](#schemavectorconfig)|false|none|向量检索配置|

<h2 id="tocS_FulltextConfig">FulltextConfig</h2>
<!-- backwards compatibility -->
<a id="schemafulltextconfig"></a>
<a id="schema_FulltextConfig"></a>
<a id="tocSfulltextconfig"></a>
<a id="tocsfulltextconfig"></a>

```json
{
  "analyzer": "standard",
  "enabled": true
}

```

全文索引的配置

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|analyzer|string|true|none|分词器|
|enabled|boolean|true|none|是否启用|

#### Enumerated Values

|Property|Value|
|---|---|
|analyzer|standard|
|analyzer|ik_max_word|

<h2 id="tocS_KeywordConfig">KeywordConfig</h2>
<!-- backwards compatibility -->
<a id="schemakeywordconfig"></a>
<a id="schema_KeywordConfig"></a>
<a id="tocSkeywordconfig"></a>
<a id="tocskeywordconfig"></a>

```json
{
  "enabled": true,
  "ignore_above_len": 52
}

```

Root Type for KeywordConfig

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|enabled|boolean|false|none|是否启用|
|ignore_above_len|integer(int32)|false|none|忽略数据的长度上限|

<h2 id="tocS_VectorConfig">VectorConfig</h2>
<!-- backwards compatibility -->
<a id="schemavectorconfig"></a>
<a id="schema_VectorConfig"></a>
<a id="tocSvectorconfig"></a>
<a id="tocsvectorconfig"></a>

```json
{
  "enabled": true,
  "model_id": "some text"
}

```

向量索引的配置

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|enabled|boolean|true|none|是否启用|
|model_id|string|false|none|向量模型ID|

<h2 id="tocS_ViewField">ViewField</h2>
<!-- backwards compatibility -->
<a id="schemaviewfield"></a>
<a id="schema_ViewField"></a>
<a id="tocSviewfield"></a>
<a id="tocsviewfield"></a>

```json
{
  "name": "string",
  "display_name": "string",
  "type": "string"
}

```

视图字段信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|字段名称|
|display_name|string|false|none|字段显示名.查看时有此字段|
|type|string|false|none|视图字段类型，查看时有此字段|

<h2 id="tocS_LogicProperty">LogicProperty</h2>
<!-- backwards compatibility -->
<a id="schemalogicproperty"></a>
<a id="schema_LogicProperty"></a>
<a id="tocSlogicproperty"></a>
<a id="tocslogicproperty"></a>

```json
{
  "name": "string",
  "display_name": "string",
  "type": "string",
  "comment": "string",
  "index": true,
  "data_source": {
    "type": "metric",
    "id": "string",
    "name": "string"
  },
  "parameters": [
    {
      "name": "string",
      "type": "string",
      "source": "string",
      "value_from": "property",
      "value": "string"
    }
  ]
}

```

逻辑属性

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|属性名称。只能包含小写英文字母、数字、下划线（_）、连字符（-），且不能以下划线和连字符开头|
|display_name|string|false|none|属性显示名|
|type|string|false|none|属性数据类型。除了视图的字段类型之外，还有 metric、objective、event、trace、log、operator|
|comment|string|false|none|属性描述|
|index|boolean|false|none|是否开启索引，默认是true|
|data_source|[LogicSource](#schemalogicsource)|true|none|逻辑来源|
|parameters|[[Parameter](#schemaparameter)]|true|none|逻辑所需的参数|

<h2 id="tocS_LogicSource">LogicSource</h2>
<!-- backwards compatibility -->
<a id="schemalogicsource"></a>
<a id="schema_LogicSource"></a>
<a id="tocSlogicsource"></a>
<a id="tocslogicsource"></a>

```json
{
  "type": "metric",
  "id": "string",
  "name": "string"
}

```

数据来源

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|type|string|true|none|数据来源类型|
|id|string|true|none|数据来源ID|
|name|string|false|none|名称。查看详情时返回。|

#### Enumerated Values

|Property|Value|
|---|---|
|type|metric|
|type|operator|

<h2 id="tocS_RelationTypeDetail">RelationTypeDetail</h2>
<!-- backwards compatibility -->
<a id="schemarelationtypedetail"></a>
<a id="schema_RelationTypeDetail"></a>
<a id="tocSrelationtypedetail"></a>
<a id="tocsrelationtypedetail"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "kn_id": "string",
  "source_object_type_id": "string",
  "source_object_type": {
    "id": "string",
    "name": "string",
    "icon": "string",
    "color": "string"
  },
  "target_object_type_id": "string",
  "target_object_type": {
    "id": "string",
    "name": "string",
    "icon": "string",
    "color": "string"
  },
  "type": "direct",
  "mapping_rules": [
    {
      "target_property": {
        "name": "string",
        "display_name": "string"
      },
      "source_property": {
        "name": "string",
        "display_name": "string"
      }
    }
  ],
  "creator": "string",
  "create_time": 0,
  "updater": "string",
  "update_time": 0,
  "detail": "string"
}

```

关系类

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|关系类ID|
|name|string|true|none|关系类名称|
|tags|[string]|true|none|标签。 （可以为空）|
|comment|string|true|none|备注（可以为空）|
|icon|string|true|none|图标|
|color|string|true|none|颜色|
|branch|string|true|none|分支ID|
|kn_id|string|true|none|业务知识网络ID|
|source_object_type_id|string|true|none|起点象类ID|
|source_object_type|[SimpleObjectType](#schemasimpleobjecttype)|true|none|起点象类名称，当查看详情时，此字段才会返回。|
|target_object_type_id|string|true|none|终点对象类ID|
|target_object_type|[SimpleObjectType](#schemasimpleobjecttype)|true|none|终点对象类名称，当查看详情时，此字段才会返回|
|type|string|true|none|关系类型|
|mapping_rules|any|true|none|关联的匹配规则。`direct` 时为 Mapping 数组；`data_view` 时为 DataViewMappingRule；`filtered_cross_join`（FCJ）时为 FilteredCrossJoinMappingRule。|

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[DirectMappingRules](#schemadirectmappingrules)|false|none|直接关联|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[DataViewMappingRule](#schemadataviewmappingrule)|false|none|关系类型为 data_view 时的匹配规则|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[FilteredCrossJoinMappingRule](#schemafilteredcrossjoinmappingrule)|false|none|关系类型为 `filtered_cross_join`（分侧过滤全连接，FCJ）时的匹配规则。无数据视图与键映射；<br>`source_condition` / `target_condition` 为可选的实例过滤条件（结构与对象实例查询 Condition 一致，参见 relation-type / ontology-query 等 API 中的 Condition）。<br>两侧均可省略，或 `mapping_rules` 为 `{}`（表示两侧无额外过滤）。|

continued

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|creator|string|true|none|创建人ID|
|create_time|integer(int64)|true|none|创建时间|
|updater|string|true|none|最近一次修改人|
|update_time|integer(int64)|true|none|最近一次更新时间|
|detail|string|true|none|说明书。按需返回，若指定了include_detail=true，则返回，否则不返回。列表查询时不返回此字段。|

#### Enumerated Values

|Property|Value|
|---|---|
|type|direct|
|type|data_view|
|type|filtered_cross_join|

<h2 id="tocS_DirectMappingRules">DirectMappingRules</h2>
<!-- backwards compatibility -->
<a id="schemadirectmappingrules"></a>
<a id="schema_DirectMappingRules"></a>
<a id="tocSdirectmappingrules"></a>
<a id="tocsdirectmappingrules"></a>

```json
[
  {
    "target_property": {
      "name": "string",
      "display_name": "string"
    },
    "source_property": {
      "name": "string",
      "display_name": "string"
    }
  }
]

```

直接关联

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[[Mapping](#schemamapping)]|false|none|直接关联|

<h2 id="tocS_DataViewMappingRule">DataViewMappingRule</h2>
<!-- backwards compatibility -->
<a id="schemadataviewmappingrule"></a>
<a id="schema_DataViewMappingRule"></a>
<a id="tocSdataviewmappingrule"></a>
<a id="tocsdataviewmappingrule"></a>

```json
{
  "backing_data_source": {
    "type": "data_view",
    "id": "string",
    "name": "string"
  },
  "source_mapping_rules": [
    {
      "target_property": {
        "name": "string",
        "display_name": "string"
      },
      "source_property": {
        "name": "string",
        "display_name": "string"
      }
    }
  ],
  "target_mapping_rules": [
    {
      "target_property": {
        "name": "string",
        "display_name": "string"
      },
      "source_property": {
        "name": "string",
        "display_name": "string"
      }
    }
  ]
}

```

关系类型为 data_view 时的匹配规则

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|backing_data_source|[DataSource](#schemadatasource)|true|none|数据来源视图|
|source_mapping_rules|[[Mapping](#schemamapping)]|true|none|起点对象类与数据集的匹配规则|
|target_mapping_rules|[[Mapping](#schemamapping)]|true|none|终点对象类与数据集匹配规则|

<h2 id="tocS_FilteredCrossJoinMappingRule">FilteredCrossJoinMappingRule</h2>
<!-- backwards compatibility -->
<a id="schemafilteredcrossjoinmappingrule"></a>
<a id="schema_FilteredCrossJoinMappingRule"></a>
<a id="tocSfilteredcrossjoinmappingrule"></a>
<a id="tocsfilteredcrossjoinmappingrule"></a>

```json
{
  "source_condition": {},
  "target_condition": {}
}

```

关系类型为 `filtered_cross_join`（分侧过滤全连接，FCJ）时的匹配规则。无数据视图与键映射；
`source_condition` / `target_condition` 为可选的实例过滤条件（结构与对象实例查询 Condition 一致，参见 relation-type / ontology-query 等 API 中的 Condition）。
两侧均可省略，或 `mapping_rules` 为 `{}`（表示两侧无额外过滤）。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|source_condition|object|false|none|起点侧实例过滤条件；可省略表示该侧无约束|
|target_condition|object|false|none|终点侧实例过滤条件；可省略表示该侧无约束|

<h2 id="tocS_Mapping">Mapping</h2>
<!-- backwards compatibility -->
<a id="schemamapping"></a>
<a id="schema_Mapping"></a>
<a id="tocSmapping"></a>
<a id="tocsmapping"></a>

```json
{
  "target_property": {
    "name": "string",
    "display_name": "string"
  },
  "source_property": {
    "name": "string",
    "display_name": "string"
  }
}

```

关联规则

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|target_property|[SimpleProperty](#schemasimpleproperty)|true|none|起点属性|
|source_property|[SimpleProperty](#schemasimpleproperty)|true|none|终点属性|

<h2 id="tocS_SimpleProperty">SimpleProperty</h2>
<!-- backwards compatibility -->
<a id="schemasimpleproperty"></a>
<a id="schema_SimpleProperty"></a>
<a id="tocSsimpleproperty"></a>
<a id="tocssimpleproperty"></a>

```json
{
  "name": "string",
  "display_name": "string"
}

```

属性简单信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|属性名称|
|display_name|string|true|none|属性显示名。当查看详情时会返回此字段|

<h2 id="tocS_AddObjectTypeToGroup">AddObjectTypeToGroup</h2>
<!-- backwards compatibility -->
<a id="schemaaddobjecttypetogroup"></a>
<a id="schema_AddObjectTypeToGroup"></a>
<a id="tocSaddobjecttypetogroup"></a>
<a id="tocsaddobjecttypetogroup"></a>

```json
{
  "entries": [
    {
      "id": "string"
    }
  ]
}

```

添加对象类到概念分组

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[AddObjectType](#schemaaddobjecttype)]|true|none|添加对象类|

<h2 id="tocS_AddObjectType">AddObjectType</h2>
<!-- backwards compatibility -->
<a id="schemaaddobjecttype"></a>
<a id="schema_AddObjectType"></a>
<a id="tocSaddobjecttype"></a>
<a id="tocsaddobjecttype"></a>

```json
{
  "id": "string"
}

```

添加对象类

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|对象类id|

<h2 id="tocS_UpdateConceptGroup">UpdateConceptGroup</h2>
<!-- backwards compatibility -->
<a id="schemaupdateconceptgroup"></a>
<a id="schema_UpdateConceptGroup"></a>
<a id="tocSupdateconceptgroup"></a>
<a id="tocsupdateconceptgroup"></a>

```json
{
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string"
}

```

更新概念分组

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|名称|
|tags|[string]|false|none|标签。用于业务标识|
|comment|string|false|none|备注|
|icon|string|false|none|图标|
|color|string|false|none|颜色|

<h2 id="tocS_SimpleConceptGroup">SimpleConceptGroup</h2>
<!-- backwards compatibility -->
<a id="schemasimpleconceptgroup"></a>
<a id="schema_SimpleConceptGroup"></a>
<a id="tocSsimpleconceptgroup"></a>
<a id="tocssimpleconceptgroup"></a>

```json
{
  "id": "string",
  "name": "string"
}

```

概念分组简单信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|概念分组id|
|name|string|true|none|概念分组名称|

<h2 id="tocS_ConceptGroupDetail">ConceptGroupDetail</h2>
<!-- backwards compatibility -->
<a id="schemaconceptgroupdetail"></a>
<a id="schema_ConceptGroupDetail"></a>
<a id="tocSconceptgroupdetail"></a>
<a id="tocsconceptgroupdetail"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "kn_id": "string",
  "branch": "string",
  "object_types": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "concept_groups": [
        {
          "id": "string",
          "name": "string"
        }
      ],
      "data_source": {
        "type": "data_view",
        "id": "string",
        "name": "string"
      },
      "data_properties": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string",
          "mapped_field": {
            "name": "string",
            "display_name": "string",
            "type": "string"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 76
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "string"
          ]
        }
      ],
      "logic_properties": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string",
          "index": true,
          "data_source": {
            "type": "metric",
            "id": "string",
            "name": "string"
          },
          "parameters": [
            {
              "name": "string",
              "type": "string",
              "source": "string",
              "value_from": "property",
              "value": "string"
            }
          ]
        }
      ],
      "primary_keys": [
        "string"
      ],
      "display_key": "string",
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string",
      "module_type": "object_type"
    }
  ],
  "relation_types": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "source_object_type_id": "string",
      "source_object_type": {
        "id": "string",
        "name": "string",
        "icon": "string",
        "color": "string"
      },
      "target_object_type_id": "string",
      "target_object_type": {
        "id": "string",
        "name": "string",
        "icon": "string",
        "color": "string"
      },
      "type": "direct",
      "mapping_rules": [
        {
          "target_property": {
            "name": "string",
            "display_name": "string"
          },
          "source_property": {
            "name": "string",
            "display_name": "string"
          }
        }
      ],
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string"
    }
  ],
  "action_types": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "action_type": "add",
      "action_intent": "add",
      "object_type_id": "string",
      "object_type": {
        "id": "string",
        "name": "string",
        "icon": "string",
        "color": "string"
      },
      "condition": {
        "object_type_id": "string",
        "field": "string",
        "operation": "and",
        "sub_conditions": [
          {}
        ],
        "value": null,
        "value_from": "const"
      },
      "affect": {
        "comment": "string",
        "object_type_id": "string",
        "object_type": {
          "id": "string",
          "name": "string",
          "icon": "string",
          "color": "string"
        }
      },
      "impact_contracts": [
        {
          "object_type_id": "string",
          "expected_operation": "add",
          "description": "string",
          "affected_fields": [
            "string"
          ]
        }
      ],
      "action_source": {
        "type": "tool",
        "box_id": "string",
        "tool_id": "string"
      },
      "parameters": [
        {
          "name": "string",
          "type": "string",
          "source": "string",
          "value_from": "property",
          "value": "string"
        }
      ],
      "schedule": {
        "type": "FIX_RATE",
        "expression": "string"
      },
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string",
      "module_type": "action_type"
    }
  ],
  "creator": "string",
  "create_time": 0,
  "updator": "string",
  "update_time": 0,
  "detail": "string"
}

```

概念分组详情

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|ID|
|name|string|true|none|名称|
|tags|[string]|true|none|标签，可为空|
|comment|string|true|none|备注，可为空|
|icon|string|true|none|图标|
|color|string|true|none|颜色|
|kn_id|string|true|none|业务知识网络ID|
|branch|string|true|none|分支ID|
|object_types|[[ObjectTypeDetail](#schemaobjecttypedetail)]|true|none|对象类|
|relation_types|[[RelationTypeDetail](#schemarelationtypedetail)]|true|none|关系类|
|action_types|[[ActionTypeDetail](#schemaactiontypedetail)]|true|none|行动类|
|creator|string|true|none|创建人ID|
|create_time|integer(int64)|true|none|创建时间|
|updator|string|true|none|最近一次修改人|
|update_time|integer(int64)|true|none|最近一次更新时间|
|detail|string|true|none|说明书。|



<!-- Generator: Widdershins v4.0.1 -->

<h1 id="job">Job v0.1.0</h1>


任务管理API

<h1 id="job-default">Default</h1>

## 获取任务列表

`GET /api/bkn-backend/v1/knowledge-networks/{kn_id}/jobs`

<h3 id="获取任务列表-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|name_pattern|query|string|false|任务名称过滤|
|state|query|string|false|任务状态过滤|
|job_type|query|string|false|任务类型|
|limit|query|integer|false|返回任务条数|
|direction|query|string|false|排序方向|
|offset|query|integer|false|数据翻页起点|
|kn_id|path|string|true|业务知识网络id|

#### Enumerated Values

|Parameter|Value|
|---|---|
|state|pending|
|state|running|
|state|completed|
|state|canceled|
|state|failed|
|job_type|full|
|direction|asc|
|direction|desc|

> Example responses

> 200 Response

```json
{
  "entries": [
    {
      "id": "some text",
      "name": "some text",
      "kn_id": "some text",
      "state": "failed",
      "state_detail": "some text",
      "creator": "some text",
      "create_time": 50,
      "finished_time": 21,
      "time_cost": 92,
      "job_type": "full",
      "job_concept_config": [
        {
          "concept_type": "object_type",
          "concept_id": "some text"
        }
      ]
    },
    {
      "id": "some text",
      "name": "some text",
      "kn_id": "some text",
      "state": "canceled",
      "state_detail": "some text",
      "creator": "some text",
      "create_time": 73,
      "finished_time": 70,
      "time_cost": 21,
      "job_type": "full",
      "job_concept_config": []
    }
  ],
  "total_count": 48
}
```

<h3 id="获取任务列表-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|获取任务列表|[ListJobResp](#schemalistjobresp)|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|400-参数错误|[ErrorResponse](#schemaerrorresponse)|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|401-未授权|[ErrorResponse](#schemaerrorresponse)|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|404-对象不存在|[ErrorResponse](#schemaerrorresponse)|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|500-服务内部错误|[ErrorResponse](#schemaerrorresponse)|

<aside class="success">
This operation does not require authentication
</aside>

## 创建任务

`POST /api/bkn-backend/v1/knowledge-networks/{kn_id}/jobs`

> Body parameter

```json
{
  "name": "some text",
  "job_type": "full",
  "job_concept_config": [
    {
      "concept_type": "object_type",
      "concept_id": "some text"
    }
  ]
}
```

<h3 id="创建任务-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|body|body|[CreateJobReqBody](#schemacreatejobreqbody)|true|none|
|kn_id|path|string|true|业务知识网络id|

> Example responses

> 400 Response

```json
{
  "error_code": "some text",
  "description": "some text",
  "solution": "some text",
  "error_link": "some text",
  "error_details": "some text"
}
```

<h3 id="创建任务-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|201|[Created](https://tools.ietf.org/html/rfc7231#section-6.3.2)|创建成功|None|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|400-参数错误|[ErrorResponse](#schemaerrorresponse)|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|401-未授权|[ErrorResponse](#schemaerrorresponse)|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|404-对象不存在|[ErrorResponse](#schemaerrorresponse)|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|500-服务内部错误|[ErrorResponse](#schemaerrorresponse)|

<h3 id="创建任务-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 获取子任务列表

`GET /api/bkn-backend/v1/knowledge-networks/{kn_id}/jobs/{job_id}/tasks`

<h3 id="获取子任务列表-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|concept_type|query|string|false|概念类型|
|state|query|string|false|子任务状态|
|concept_name_pattern|query|string|false|概念名称|
|offset|query|integer|false|数据翻页起点|
|limit|query|integer|false|返回子任务条数|
|sort|query|string|false|排序字段|
|direction|query|string|false|排序方向|
|kn_id|path|string|true|业务知识网络id|
|job_id|path|string|true|任务id|

#### Enumerated Values

|Parameter|Value|
|---|---|
|concept_type|object_type|
|state|pending|
|state|running|
|state|completed|
|state|canceled|
|state|failed|
|sort|create_time|
|sort|finishe_time|
|sort|time_cost|
|direction|asc|
|direction|desc|

> Example responses

> 200 Response

```json
{
  "entries": [
    {
      "id": "some text",
      "state": "pending",
      "state_detail": "some text",
      "time_cost": 5,
      "concept_type": "object_type",
      "finish_time": 71,
      "start_time": 83,
      "job_id": "some text",
      "concept_id": "some text"
    },
    {
      "id": "some text",
      "state": "canceled",
      "state_detail": "some text",
      "time_cost": 24,
      "concept_type": "object_type",
      "finish_time": 6,
      "start_time": 54,
      "job_id": "some text",
      "concept_id": "some text"
    }
  ],
  "total_count": 15
}
```

<h3 id="获取子任务列表-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|子任务列表|[ListTaskResp](#schemalisttaskresp)|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|400-参数错误|[ErrorResponse](#schemaerrorresponse)|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|401-未授权|[ErrorResponse](#schemaerrorresponse)|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|404-对象不存在|[ErrorResponse](#schemaerrorresponse)|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|500-服务内部错误|[ErrorResponse](#schemaerrorresponse)|

<aside class="success">
This operation does not require authentication
</aside>

## 批量删除任务

`DELETE /api/bkn-backend/v1/knowledge-networks/{kn_id}/jobs/{job_ids}`

<h3 id="批量删除任务-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络id|
|job_ids|path|string|true|任务id列表，逗号分隔|

> Example responses

> 401 Response

```json
{
  "error_code": "some text",
  "description": "some text",
  "solution": "some text",
  "error_link": "some text",
  "error_details": "some text"
}
```

<h3 id="批量删除任务-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|删除成功|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|401-未授权|[ErrorResponse](#schemaerrorresponse)|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|404-对象不存在|[ErrorResponse](#schemaerrorresponse)|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|500-服务内部错误|[ErrorResponse](#schemaerrorresponse)|

<aside class="success">
This operation does not require authentication
</aside>

# Schemas

<h2 id="tocS_CreateJobReqBody">CreateJobReqBody</h2>
<!-- backwards compatibility -->
<a id="schemacreatejobreqbody"></a>
<a id="schema_CreateJobReqBody"></a>
<a id="tocScreatejobreqbody"></a>
<a id="tocscreatejobreqbody"></a>

```json
{
  "name": "some text",
  "job_type": "full",
  "job_concept_config": [
    {
      "concept_type": "object_type",
      "concept_id": "some text"
    }
  ]
}

```

Root Type for CreateJobReqBody

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|任务名称|
|job_type|string|true|none|任务类型|
|job_concept_config|[[ConceptConfig](#schemaconceptconfig)]|false|none|任务概念配置列表；可选，未传时服务端可能根据知识网络对象类型自动生成|

#### Enumerated Values

|Property|Value|
|---|---|
|job_type|full|

<h2 id="tocS_ConceptConfig">ConceptConfig</h2>
<!-- backwards compatibility -->
<a id="schemaconceptconfig"></a>
<a id="schema_ConceptConfig"></a>
<a id="tocSconceptconfig"></a>
<a id="tocsconceptconfig"></a>

```json
{
  "concept_type": "object_type",
  "concept_id": "some text"
}

```

ConceptConfig

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|concept_type|string|true|none|概念类型|
|concept_id|string|true|none|概念id|

#### Enumerated Values

|Property|Value|
|---|---|
|concept_type|object_type|
|concept_type|relation_type|

<h2 id="tocS_ListJobResp">ListJobResp</h2>
<!-- backwards compatibility -->
<a id="schemalistjobresp"></a>
<a id="schema_ListJobResp"></a>
<a id="tocSlistjobresp"></a>
<a id="tocslistjobresp"></a>

```json
{
  "entries": [
    {
      "id": "some text",
      "name": "some text",
      "kn_id": "some text",
      "state": "failed",
      "state_detail": "some text",
      "creator": "some text",
      "create_time": 50,
      "finished_time": 21,
      "time_cost": 92,
      "job_type": "full",
      "job_concept_config": [
        {
          "concept_type": "object_type",
          "concept_id": "some text"
        }
      ]
    },
    {
      "id": "some text",
      "name": "some text",
      "kn_id": "some text",
      "state": "canceled",
      "state_detail": "some text",
      "creator": "some text",
      "create_time": 73,
      "finished_time": 70,
      "time_cost": 21,
      "job_type": "full",
      "job_concept_config": []
    }
  ],
  "total_count": 48
}

```

Root Type for ListResp

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[Job](#schemajob)]|true|none|任务列表|
|total_count|integer(int64)|true|none|任务总数|

<h2 id="tocS_ListTaskResp">ListTaskResp</h2>
<!-- backwards compatibility -->
<a id="schemalisttaskresp"></a>
<a id="schema_ListTaskResp"></a>
<a id="tocSlisttaskresp"></a>
<a id="tocslisttaskresp"></a>

```json
{
  "entries": [
    {
      "id": "some text",
      "state": "pending",
      "state_detail": "some text",
      "time_cost": 5,
      "concept_type": "object_type",
      "finish_time": 71,
      "start_time": 83,
      "job_id": "some text",
      "concept_id": "some text"
    },
    {
      "id": "some text",
      "state": "canceled",
      "state_detail": "some text",
      "time_cost": 24,
      "concept_type": "object_type",
      "finish_time": 6,
      "start_time": 54,
      "job_id": "some text",
      "concept_id": "some text"
    }
  ],
  "total_count": 15
}

```

Root Type for ListResp

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[Task](#schematask)]|true|none|任务列表|
|total_count|integer(int64)|true|none|任务总数|

<h2 id="tocS_Job">Job</h2>
<!-- backwards compatibility -->
<a id="schemajob"></a>
<a id="schema_Job"></a>
<a id="tocSjob"></a>
<a id="tocsjob"></a>

```json
{
  "id": "some text",
  "name": "some text",
  "kn_id": "some text",
  "state": "running",
  "state_detail": "some text",
  "creator": "some text",
  "create_time": 59,
  "finished_time": 37,
  "time_cost": 26,
  "job_type": "full",
  "job_concept_config": [
    {
      "concept_type": "object_type",
      "concept_id": "some text"
    }
  ]
}

```

Root Type for Job

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|任务id|
|name|string|true|none|任务名称|
|kn_id|string|true|none|业务知识网络id|
|state|string|true|none|状态|
|state_detail|string|true|none|状态详情|
|creator|string|true|none|创建者|
|create_time|integer(int64)|true|none|创建时间|
|finished_time|integer|true|none|结束时间|
|time_cost|integer(int64)|true|none|任务运行时间|
|job_type|string|true|none|任务类型|
|job_concept_config|[[ConceptConfig](#schemaconceptconfig)]|false|none|任务概念配置列表|

#### Enumerated Values

|Property|Value|
|---|---|
|state|pending|
|state|running|
|state|completed|
|state|canceled|
|state|failed|
|job_type|full|

<h2 id="tocS_Task">Task</h2>
<!-- backwards compatibility -->
<a id="schematask"></a>
<a id="schema_Task"></a>
<a id="tocStask"></a>
<a id="tocstask"></a>

```json
{
  "id": "some text",
  "state": "completed",
  "state_detail": "some text",
  "time_cost": 9,
  "concept_type": "object_type",
  "finish_time": 98,
  "start_time": 12,
  "job_id": "some text",
  "concept_id": "some text"
}

```

Root Type for Job

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|子任务id|
|state|string|true|none|状态|
|state_detail|string|true|none|状态详情|
|time_cost|integer(int64)|true|none|任务运行时间|
|concept_type|string|true|none|概念类型|
|finish_time|integer(int64)|true|none|结束时间|
|start_time|integer(int64)|true|none|开始时间|
|job_id|string|true|none|任务id|
|concept_id|string|true|none|概念id|

#### Enumerated Values

|Property|Value|
|---|---|
|state|pending|
|state|running|
|state|completed|
|state|canceled|
|state|failed|
|concept_type|object_type|

<h2 id="tocS_CreateJobResp">CreateJobResp</h2>
<!-- backwards compatibility -->
<a id="schemacreatejobresp"></a>
<a id="schema_CreateJobResp"></a>
<a id="tocScreatejobresp"></a>
<a id="tocscreatejobresp"></a>

```json
{
  "id": "some text"
}

```

Root Type for CreateJobResp

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|任务id|

<h2 id="tocS_ErrorResponse">ErrorResponse</h2>
<!-- backwards compatibility -->
<a id="schemaerrorresponse"></a>
<a id="schema_ErrorResponse"></a>
<a id="tocSerrorresponse"></a>
<a id="tocserrorresponse"></a>

```json
{
  "error_code": "some text",
  "description": "some text",
  "solution": "some text",
  "error_link": "some text",
  "error_details": "some text"
}

```

Root Type for ErrorResponse

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|error_code|string|true|none|错误码|
|description|string|true|none|描述|
|solution|string|true|none|错误解决方法|
|error_link|string|true|none|错误解决指导link|
|error_details|string|true|none|错误详情|



<!-- Generator: Widdershins v4.0.1 -->

<h1 id="objecttype">ObjectType v0.1.0</h1>


<h1 id="objecttype-default">Default</h1>

## 获取对象类列表

`GET /api/bkn-backend/v1/knowledge-networks/{kn_id}/object-types`

<h3 id="获取对象类列表-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|name_pattern|query|string|false|根据络名称模糊查询，默认为空|
|sort|query|string|false|排序类型，默认是update_time|
|direction|query|string|false|排序结果方向，可选asc、desc。|
|offset|query|integer(int64)|false|开始响应的项目的偏移量	|
|limit|query|integer(int64)|false|每页最多可返回的项目数；|
|tag|query|string|false|根据标签精准查询，默认为空.|

#### Detailed descriptions

**direction**: 排序结果方向，可选asc、desc。
默认desc

**offset**: 开始响应的项目的偏移量	
范围需大于等于0，默认值0

**limit**: 每页最多可返回的项目数；
分页可选1-1000，-1表示不分页；
默认值10

#### Enumerated Values

|Parameter|Value|
|---|---|
|sort|update_time|
|sort|name|
|direction|asc|
|direction|desc|

> Example responses

> 200 Response

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "concept_groups": [
        {
          "id": "string",
          "name": "string"
        }
      ],
      "data_source": {
        "type": "data_view",
        "id": "string",
        "name": "string"
      },
      "data_properties": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string",
          "mapped_field": {
            "name": "string",
            "display_name": "string",
            "type": "string"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 76
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "string"
          ]
        }
      ],
      "logic_properties": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string",
          "index": true,
          "data_source": {
            "type": "metric",
            "id": "string",
            "name": "string"
          },
          "parameters": [
            {
              "name": "string",
              "type": "string",
              "source": "string",
              "value_from": "property",
              "value": "string"
            }
          ],
          "analysis_dimensions": [
            {
              "name": "string",
              "display_name": "string",
              "type": "string",
              "comment": "string"
            }
          ]
        }
      ],
      "primary_keys": [
        "string"
      ],
      "display_key": "string",
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string",
      "module_type": "object_type"
    }
  ],
  "total_count": 0
}
```

<h3 id="获取对象类列表-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[ListObjectTypes](#schemalistobjecttypes)|

<aside class="success">
This operation does not require authentication
</aside>

## 创建或检索对象类

`POST /api/bkn-backend/v1/knowledge-networks/{kn_id}/object-types`

> Body parameter

```json
{
  "need_total": true,
  "limit": 1,
  "condition": {
    "operation": "and",
    "sub_conditions": [
      {
        "field": "comment",
        "operation": "match",
        "value_from": "const",
        "value": "pod"
      },
      {
        "field": "*",
        "operation": "knn",
        "value_from": "const",
        "value": [
          "pod",
          10
        ]
      }
    ]
  }
}
```

<h3 id="创建或检索对象类-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|x-http-method-override|header|string|true|重载请求头|
|body|body|[override](#schemaoverride)|true|none|

#### Enumerated Values

|Parameter|Value|
|---|---|
|x-http-method-override|POST|
|x-http-method-override|GET|

> Example responses

> 重载GET, 检索对象类

```json
{
  "entries": [
    {
      "id": "pod_has_metric_test",
      "name": "pod_has_metric_test",
      "tags": [
        "事件网络",
        "拓扑架构"
      ],
      "comment": "绑定了指标模型的pod信息.....",
      "icon": "",
      "color": "",
      "branch": "main",
      "detail": "",
      "creator": "a0f02238-6cec-11f0-82bb-fa1c4529a151",
      "create_time": 1758157953104,
      "updater": "a0f02238-6cec-11f0-82bb-fa1c4529a151",
      "update_time": 1758157953104,
      "kn_id": "kn_system_incident_event_network",
      "data_source": {
        "type": "data_view",
        "id": "d2mio43q6gt6p380dis0",
        "name": ""
      },
      "data_properties": [
        {
          "name": "id",
          "display_name": "id",
          "type": "int64",
          "comment": "主键",
          "mapped_field": {
            "name": "id"
          }
        },
        {
          "name": "node_id",
          "display_name": "node_id",
          "type": "VARCHAR",
          "comment": "节点id，",
          "mapped_field": {
            "name": "node_id"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "pod_cluster_id",
          "display_name": "pod_cluster_id",
          "type": "VARCHAR",
          "comment": "pod的集群id",
          "mapped_field": {
            "name": "pod_cluster_id"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "pod_ip",
          "display_name": "pod_ip",
          "type": "VARCHAR",
          "comment": "pod的ip",
          "mapped_field": {
            "name": "pod_ip"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "pod_name",
          "display_name": "pod_name",
          "type": "VARCHAR",
          "comment": "pod名称",
          "mapped_field": {
            "name": "pod_name"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "pod_namespace",
          "display_name": "pod_namespace",
          "type": "VARCHAR",
          "comment": "pod所属命名空间",
          "mapped_field": {
            "name": "pod_namespace"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "pod_node_name",
          "display_name": "pod_node_name",
          "type": "int",
          "comment": "pod所在节点名称",
          "mapped_field": {
            "name": "pod_node_name"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "pod_port",
          "display_name": "pod_port",
          "type": "VARCHAR",
          "comment": "pod端口",
          "mapped_field": {
            "name": "pod_port"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "pod_status",
          "display_name": "pod_status",
          "type": "VARCHAR",
          "comment": "pod状态",
          "mapped_field": {
            "name": "pod_status"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "service_ip",
          "display_name": "service_ip",
          "type": "VARCHAR",
          "comment": "服务ip",
          "mapped_field": {
            "name": "service_ip"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "service_name",
          "display_name": "service_name",
          "type": "VARCHAR",
          "comment": "服务名称",
          "mapped_field": {
            "name": "service_name"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "component",
          "display_name": "component",
          "type": "VARCHAR",
          "comment": "组件",
          "mapped_field": {
            "name": "component"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "pod_create_time",
          "display_name": "pod_create_time",
          "type": "VARCHAR",
          "comment": "pod创建时间",
          "mapped_field": {
            "name": "pod_create_time"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "pod_delete_time",
          "display_name": "pod_delete_time",
          "type": "VARCHAR",
          "comment": "pod删除时间",
          "mapped_field": {
            "name": "pod_delete_time"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "s_create_time",
          "display_name": "s_create_time",
          "type": "timestamp",
          "comment": "系统创建时间",
          "mapped_field": {
            "name": "s_create_time"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "s_update_time",
          "display_name": "s_update_time",
          "type": "timestamp",
          "comment": "系统更新时间",
          "mapped_field": {
            "name": "s_update_time"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "_timestamp",
          "display_name": "_timestamp",
          "type": "timestamp",
          "comment": "时间",
          "mapped_field": {
            "name": "_timestamp"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "_action",
          "display_name": "_action",
          "type": "VARCHAR",
          "comment": "行动",
          "mapped_field": {
            "name": "_action"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        }
      ],
      "logic_properties": [
        {
          "name": "metric_test",
          "display_name": "metric_test",
          "type": "metric",
          "comment": "pod的指标",
          "index": true,
          "data_source": {
            "type": "metric-model",
            "id": "pod_metric",
            "name": ""
          },
          "parameters": [
            {
              "name": "id",
              "value_from": "property",
              "value": "id"
            },
            {
              "name": "pod_name",
              "value_from": "property",
              "value": "pod_name"
            },
            {
              "name": "instant",
              "value_from": "input",
              "value": ""
            },
            {
              "name": "start",
              "value_from": "input",
              "value": ""
            },
            {
              "name": "end",
              "value_from": "input",
              "value": ""
            },
            {
              "name": "instant",
              "value_from": "input",
              "value": ""
            },
            {
              "name": "start",
              "value_from": "input",
              "value": ""
            },
            {
              "name": "end",
              "value_from": "input",
              "value": ""
            }
          ]
        },
        {
          "name": "operator_test",
          "display_name": "operator_test",
          "type": "operator",
          "comment": "pod的算子",
          "index": true,
          "data_source": {
            "type": "operator",
            "id": "pod_operator",
            "name": ""
          },
          "parameters": [
            {
              "name": "operator_param1",
              "value_from": "property",
              "value": "id"
            },
            {
              "name": "operator_param2",
              "value_from": "property",
              "value": "pod_name"
            },
            {
              "name": "operator_param3",
              "value_from": "input",
              "value": ""
            }
          ]
        }
      ],
      "primary_keys": [
        "id"
      ],
      "display_key": "pod_name",
      "IfNameModify": false,
      "module_type": "object_type"
    }
  ],
  "total_count": 2,
  "search_after": [
    4.8061843,
    "pod_has_metric_test"
  ]
}
```

```json
{
  "entries": [
    {
      "id": "pod_has_metric_test2",
      "name": "pod_has_metric_test2",
      "tags": [
        "事件网络",
        "拓扑架构"
      ],
      "comment": "绑定了指标模型的pod信息.....",
      "icon": "",
      "color": "",
      "branch": "main",
      "detail": "",
      "creator": "a0f02238-6cec-11f0-82bb-fa1c4529a151",
      "create_time": 1758273996605,
      "updater": "a0f02238-6cec-11f0-82bb-fa1c4529a151",
      "update_time": 1758273996605,
      "kn_id": "kn_system_incident_event_network",
      "data_source": {
        "type": "data_view",
        "id": "d2mio43q6gt6p380dis0",
        "name": ""
      },
      "data_properties": [
        {
          "name": "id",
          "display_name": "id",
          "type": "int64",
          "comment": "主键",
          "mapped_field": {
            "name": "id"
          },
          "index": true,
          "fulltext_config": {
            "analyzer": "",
            "field_keyword": false
          },
          "vector_config": {
            "analyzer": "",
            "field_keyword": false
          }
        },
        {
          "name": "node_id",
          "display_name": "node_id",
          "type": "VARCHAR",
          "comment": "节点id，",
          "mapped_field": {
            "name": "node_id"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "pod_cluster_id",
          "display_name": "pod_cluster_id",
          "type": "VARCHAR",
          "comment": "pod的集群id",
          "mapped_field": {
            "name": "pod_cluster_id"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "pod_ip",
          "display_name": "pod_ip",
          "type": "VARCHAR",
          "comment": "pod的ip",
          "mapped_field": {
            "name": "pod_ip"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "pod_name",
          "display_name": "pod_name",
          "type": "VARCHAR",
          "comment": "pod名称",
          "mapped_field": {
            "name": "pod_name"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "pod_namespace",
          "display_name": "pod_namespace",
          "type": "VARCHAR",
          "comment": "pod所属命名空间",
          "mapped_field": {
            "name": "pod_namespace"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "pod_node_name",
          "display_name": "pod_node_name",
          "type": "int",
          "comment": "pod所在节点名称",
          "mapped_field": {
            "name": "pod_node_name"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "pod_port",
          "display_name": "pod_port",
          "type": "VARCHAR",
          "comment": "pod端口",
          "mapped_field": {
            "name": "pod_port"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "pod_status",
          "display_name": "pod_status",
          "type": "VARCHAR",
          "comment": "pod状态",
          "mapped_field": {
            "name": "pod_status"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "service_ip",
          "display_name": "service_ip",
          "type": "VARCHAR",
          "comment": "服务ip",
          "mapped_field": {
            "name": "service_ip"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "service_name",
          "display_name": "service_name",
          "type": "VARCHAR",
          "comment": "服务名称",
          "mapped_field": {
            "name": "service_name"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "component",
          "display_name": "component",
          "type": "VARCHAR",
          "comment": "组件",
          "mapped_field": {
            "name": "component"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "pod_create_time",
          "display_name": "pod_create_time",
          "type": "VARCHAR",
          "comment": "pod创建时间",
          "mapped_field": {
            "name": "pod_create_time"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "pod_delete_time",
          "display_name": "pod_delete_time",
          "type": "VARCHAR",
          "comment": "pod删除时间",
          "mapped_field": {
            "name": "pod_delete_time"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "s_create_time",
          "display_name": "s_create_time",
          "type": "timestamp",
          "comment": "系统创建时间",
          "mapped_field": {
            "name": "s_create_time"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "s_update_time",
          "display_name": "s_update_time",
          "type": "timestamp",
          "comment": "系统更新时间",
          "mapped_field": {
            "name": "s_update_time"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "_timestamp",
          "display_name": "_timestamp",
          "type": "timestamp",
          "comment": "时间",
          "mapped_field": {
            "name": "_timestamp"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        },
        {
          "name": "_action",
          "display_name": "_action",
          "type": "VARCHAR",
          "comment": "行动",
          "mapped_field": {
            "name": "_action"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 1024
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "==",
            "!=",
            "like",
            "not_like",
            "in",
            "not_in"
          ]
        }
      ],
      "logic_properties": [
        {
          "name": "metric_test",
          "display_name": "metric_test",
          "type": "metric",
          "comment": "pod的指标",
          "index": true,
          "data_source": {
            "type": "metric-model",
            "id": "pod_metric",
            "name": ""
          },
          "parameters": [
            {
              "name": "id",
              "value_from": "property",
              "value": "id"
            },
            {
              "name": "pod_name",
              "value_from": "property",
              "value": "pod_name"
            },
            {
              "name": "instant",
              "value_from": "input",
              "value": ""
            },
            {
              "name": "start",
              "value_from": "input",
              "value": ""
            },
            {
              "name": "end",
              "value_from": "input",
              "value": ""
            },
            {
              "name": "instant",
              "value_from": "input",
              "value": ""
            },
            {
              "name": "start",
              "value_from": "input",
              "value": ""
            },
            {
              "name": "end",
              "value_from": "input",
              "value": ""
            }
          ]
        },
        {
          "name": "operator_test",
          "display_name": "operator_test",
          "type": "operator",
          "comment": "pod的算子",
          "index": true,
          "data_source": {
            "type": "operator",
            "id": "pod_operator",
            "name": ""
          },
          "parameters": [
            {
              "name": "operator_param1",
              "value_from": "property",
              "value": "id"
            },
            {
              "name": "operator_param2",
              "value_from": "property",
              "value": "pod_name"
            },
            {
              "name": "operator_param3",
              "value_from": "input",
              "value": ""
            }
          ]
        }
      ],
      "primary_keys": [
        "id"
      ],
      "display_key": "pod_name",
      "IfNameModify": false,
      "module_type": "object_type"
    }
  ],
  "search_after": [
    4.806153,
    "pod_has_metric_test2"
  ]
}
```

> 201 Response

```json
[
  {
    "id": "string"
  }
]
```

<h3 id="创建或检索对象类-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|重载GET, 检索对象类|[ObjectTypeSearchResponse](#schemaobjecttypesearchresponse)|
|201|[Created](https://tools.ietf.org/html/rfc7231#section-6.3.2)|新增/导入成功|Inline|

<h3 id="创建或检索对象类-responseschema">Response Schema</h3>

Status Code **201**

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[[ID](#schemaid)]|false|none|[id]|
|» id|string|true|none|id|

<aside class="success">
This operation does not require authentication
</aside>

## 修改对象类

`PUT /api/bkn-backend/v1/knowledge-networks/{kn_id}/object-types/{ob_id}`

> Body parameter

```json
{
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "concept_groups": [
    {
      "id": "string",
      "name": "string"
    }
  ],
  "data_source": {
    "type": "data_view",
    "id": "string",
    "name": "string"
  },
  "data_properties": [
    {
      "name": "string",
      "display_name": "string",
      "type": "string",
      "comment": "string",
      "mapped_field": {
        "name": "string",
        "display_name": "string",
        "type": "string"
      },
      "index_config": {
        "keyword_config": {
          "enabled": true,
          "ignore_above_len": 76
        },
        "fulltext_config": {
          "analyzer": "standard",
          "enabled": true
        },
        "vector_config": {
          "enabled": true,
          "model_id": "some text"
        }
      },
      "condition_operations": [
        "string"
      ]
    }
  ],
  "logic_properties": [
    {
      "name": "string",
      "display_name": "string",
      "type": "string",
      "comment": "string",
      "index": true,
      "data_source": {
        "type": "metric",
        "id": "string",
        "name": "string"
      },
      "parameters": [
        {
          "name": "string",
          "type": "string",
          "source": "string",
          "value_from": "property",
          "value": "string"
        }
      ],
      "analysis_dimensions": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string"
        }
      ]
    }
  ],
  "primary_keys": [
    "string"
  ],
  "display_key": "string"
}
```

<h3 id="修改对象类-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|ob_id|path|string|true|对象类ID|
|branch|query|string|false|分支，不填则使用 main 分支|
|strict_mode|query|boolean|false|是否严格校验依赖，默认为 true。为 true 时校验数据视图、向量小模型、逻辑属性绑定的指标/算子等外部依赖是否存在；为 false 时不做该校验|
|validate_dependency|query|boolean|false|[已废弃] 请使用 strict_mode。兼容保留，strict_mode 为空时会读取此参数|
|body|body|[UpdateObjectType](#schemaupdateobjecttype)|true|none|

<h3 id="修改对象类-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|ok|None|

<aside class="success">
This operation does not require authentication
</aside>

## 获取对象类详情

`GET /api/bkn-backend/v1/knowledge-networks/{kn_id}/object-types/{ob_ids}`

<h3 id="获取对象类详情-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|ob_ids|path|array[string]|true|对象类ID|
|kn_id|path|string|true|业务知识网络ID|
|include_detail|query|boolean|false|是否包含说明书信息，默认false，不包含。|

> Example responses

> 200 Response

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "concept_groups": [
        {
          "id": "string",
          "name": "string"
        }
      ],
      "data_source": {
        "type": "data_view",
        "id": "string",
        "name": "string"
      },
      "data_properties": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string",
          "mapped_field": {
            "name": "string",
            "display_name": "string",
            "type": "string"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 76
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "string"
          ]
        }
      ],
      "logic_properties": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string",
          "index": true,
          "data_source": {
            "type": "metric",
            "id": "string",
            "name": "string"
          },
          "parameters": [
            {
              "name": "string",
              "type": "string",
              "source": "string",
              "value_from": "property",
              "value": "string"
            }
          ],
          "analysis_dimensions": [
            {
              "name": "string",
              "display_name": "string",
              "type": "string",
              "comment": "string"
            }
          ]
        }
      ],
      "primary_keys": [
        "string"
      ],
      "display_key": "string",
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string",
      "module_type": "object_type"
    }
  ]
}
```

<h3 id="获取对象类详情-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[ObjectTypeDetails](#schemaobjecttypedetails)|

<aside class="success">
This operation does not require authentication
</aside>

## 删除对象类

`DELETE /api/bkn-backend/v1/knowledge-networks/{kn_id}/object-types/{ob_ids}`

<h3 id="删除对象类-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|ob_ids|path|array[string]|true|对象类ID|
|force_delete|query|boolean|false|是否强制删除，默认为false。当为false时，如果对象类被关系类绑定则报错；当为true时，跳过校验直接删除|

<h3 id="删除对象类-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|ok|None|

<aside class="success">
This operation does not require authentication
</aside>

## 修改数据属性

`PUT /api/bkn-backend/v1/knowledge-networks/{kn_id}/object-types/{ob_id}/data_properties/{property_names}`

> Body parameter

```json
{
  "entries": [
    {
      "name": "string",
      "display_name": "string",
      "type": "string",
      "comment": "string",
      "mapped_field": {
        "name": "string",
        "display_name": "string",
        "type": "string"
      },
      "index_config": {
        "keyword_config": {
          "enabled": true,
          "ignore_above_len": 76
        },
        "fulltext_config": {
          "analyzer": "standard",
          "enabled": true
        },
        "vector_config": {
          "enabled": true,
          "model_id": "some text"
        }
      },
      "condition_operations": [
        "string"
      ]
    }
  ]
}
```

<h3 id="修改数据属性-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|branch|query|string|false|分支，不填则使用 main 分支|
|strict_mode|query|boolean|false|是否严格校验向量索引依赖的 embedding 小模型，默认为 true。为 true 时，若本次提交的数据属性中启用了向量索引（vector_config.enabled），则校验小模型是否存在、类型是否为 embedding、且维度等参数有效；为 false 时不调用小模型服务做该校验。请求体 JSON 的索引配置格式校验（如启用向量时 model_id 必填）仍会执行|
|body|body|[DataProperties](#schemadataproperties)|true|none|
|kn_id|path|string|true|业务知识网络ID|
|ob_id|path|string|true|对象类ID|
|property_names|path|string|true|数据属性名称列表|

<h3 id="修改数据属性-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|修改成功|None|

<aside class="success">
This operation does not require authentication
</aside>

## 校验对象类

`POST /api/bkn-backend/v1/knowledge-networks/{kn_id}/object-types/validation`

仅校验概念依赖存在性，不写库。用于导入前预检、批处理前自检等场景。
校验数据视图、向量索引小模型、逻辑属性绑定的指标模型与算子、概念分组等依赖是否存在（strict_mode=true 时）。

同批次引用（例如关系类引用尚未落库的对象类）：单类 validate 接口的请求体若无法包含被引用方定义，strict 下仍只认已落库资源。
此时请使用与创建接口同结构的 **整包知识网络校验**（`POST .../knowledge-networks/{kn_id}/validation`）或 **概念分组校验**（嵌套 object_types / relation_types / action_types），服务端会从现有 JSON 推导 BatchIDIndex，等价于创建事务内的同批可见性。
关系映射规则校验需要对象类数据属性；若批次内仅声明 OT ID、无属性定义，存在性可通过但映射规则校验可能降级。

**响应**：HTTP 200 时 body 中 `valid` 为 `true` 表示通过；为 `false` 时带 `detail`（服务端 error.Error()）。请求参数/鉴权/资源不存在等错误仍为非 2xx。

**内部接口**：`POST /api/bkn-backend/in/v1/.../object-types/validation` 与 `POST /api/ontology-manager/in/v1/.../object-types/validation`，其余相同；Header 解析访问者，无 OAuth。

> Body parameter

```json
{
  "entries": [
    {}
  ]
}
```

<h3 id="校验对象类-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|branch|query|string|false|分支，不填则使用 main 分支|
|strict_mode|query|boolean|false|是否严格校验依赖，默认为true|
|import_mode|query|string|false|与创建对象类接口一致；用于对象类 ID/名称与落库冲突的校验语义（normal / ignore / overwrite）。|
|body|body|object|true|none|
|» entries|body|[object]|false|待校验的对象类列表，结构与创建接口一致|

#### Enumerated Values

|Parameter|Value|
|---|---|
|import_mode|normal|
|import_mode|ignore|
|import_mode|overwrite|

> Example responses

> 200 Response

```json
{
  "valid": true,
  "detail": "string"
}
```

<h3 id="校验对象类-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|已返回校验结果（通过与否均可能为 200）|Inline|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数错误等；业务校验「不通过」由 200 + valid:false + detail 表达，而非本状态码|None|

<h3 id="校验对象类-responseschema">Response Schema</h3>

Status Code **200**

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» valid|boolean|true|none|none|
|» detail|string|false|none|当 valid 为 false 时的说明（error.Error()）|

<aside class="success">
This operation does not require authentication
</aside>

# Schemas

<h2 id="tocS_BasicInfo">BasicInfo</h2>
<!-- backwards compatibility -->
<a id="schemabasicinfo"></a>
<a id="schema_BasicInfo"></a>
<a id="tocSbasicinfo"></a>
<a id="tocsbasicinfo"></a>

```json
{
  "id": "string",
  "name": "string"
}

```

资源的基本信息，包含id和名称

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|资源ID|
|name|string|true|none|资源名称|

<h2 id="tocS_ConceptTypeResponse">ConceptTypeResponse</h2>
<!-- backwards compatibility -->
<a id="schemaconcepttyperesponse"></a>
<a id="schema_ConceptTypeResponse"></a>
<a id="tocSconcepttyperesponse"></a>
<a id="tocsconcepttyperesponse"></a>

```json
{
  "concept_type": "object_type",
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "groups": [
    "string"
  ]
}

```

对象类、关系类、行动类的查询返回结构

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|concept_type|string|true|none|概念类型|
|id|string|true|none|概念id|
|name|string|true|none|概念名称|
|tags|[string]|true|none|标签|
|groups|[string]|true|none|所属概念分组ID|

#### Enumerated Values

|Property|Value|
|---|---|
|concept_type|object_type|
|concept_type|relation_type|
|concept_type|action_type|

<h2 id="tocS_DataSource">DataSource</h2>
<!-- backwards compatibility -->
<a id="schemadatasource"></a>
<a id="schema_DataSource"></a>
<a id="tocSdatasource"></a>
<a id="tocsdatasource"></a>

```json
{
  "type": "data_view",
  "id": "string",
  "name": "string"
}

```

数据来源

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|type|string|true|none|数据来源类型为数据视图|
|id|string|true|none|数据视图ID|
|name|string|false|none|名称。查看详情时返回。|

#### Enumerated Values

|Property|Value|
|---|---|
|type|data_view|

<h2 id="tocS_DataProperty">DataProperty</h2>
<!-- backwards compatibility -->
<a id="schemadataproperty"></a>
<a id="schema_DataProperty"></a>
<a id="tocSdataproperty"></a>
<a id="tocsdataproperty"></a>

```json
{
  "name": "string",
  "display_name": "string",
  "type": "string",
  "comment": "string",
  "mapped_field": {
    "name": "string",
    "display_name": "string",
    "type": "string"
  },
  "index_config": {
    "keyword_config": {
      "enabled": true,
      "ignore_above_len": 76
    },
    "fulltext_config": {
      "analyzer": "standard",
      "enabled": true
    },
    "vector_config": {
      "enabled": true,
      "model_id": "some text"
    }
  },
  "condition_operations": [
    "string"
  ]
}

```

数据属性

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|属性名称。只能包含小写英文字母、数字、下划线（_）、连字符（-），且不能以下划线和连字符开头|
|display_name|string|true|none|属性显示名|
|type|string|true|none|属性数据类型。除了视图的字段类型之外，还有 metric、operator|
|comment|string|false|none|属性描述|
|mapped_field|[ViewField](#schemaviewfield)|false|none|属性映射到数据来源中的字段名|
|index_config|[IndexConfig](#schemaindexconfig)|false|none|索引配置|
|condition_operations|[string]|false|none|字符串类型的属性能支持的过滤条件。字符串类型有string, text。|

<h2 id="tocS_FulltextConfig">FulltextConfig</h2>
<!-- backwards compatibility -->
<a id="schemafulltextconfig"></a>
<a id="schema_FulltextConfig"></a>
<a id="tocSfulltextconfig"></a>
<a id="tocsfulltextconfig"></a>

```json
{
  "analyzer": "standard",
  "enabled": true
}

```

全文索引的配置

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|analyzer|string|true|none|分词器|
|enabled|boolean|true|none|是否启用|

#### Enumerated Values

|Property|Value|
|---|---|
|analyzer|standard|
|analyzer|ik_max_word|

<h2 id="tocS_VectorConfig">VectorConfig</h2>
<!-- backwards compatibility -->
<a id="schemavectorconfig"></a>
<a id="schema_VectorConfig"></a>
<a id="tocSvectorconfig"></a>
<a id="tocsvectorconfig"></a>

```json
{
  "enabled": true,
  "model_id": "some text"
}

```

向量索引的配置

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|enabled|boolean|true|none|是否启用|
|model_id|string|false|none|向量模型ID|

<h2 id="tocS_LogicProperty">LogicProperty</h2>
<!-- backwards compatibility -->
<a id="schemalogicproperty"></a>
<a id="schema_LogicProperty"></a>
<a id="tocSlogicproperty"></a>
<a id="tocslogicproperty"></a>

```json
{
  "name": "string",
  "display_name": "string",
  "type": "string",
  "comment": "string",
  "index": true,
  "data_source": {
    "type": "metric",
    "id": "string",
    "name": "string"
  },
  "parameters": [
    {
      "name": "string",
      "type": "string",
      "source": "string",
      "value_from": "property",
      "value": "string"
    }
  ],
  "analysis_dimensions": [
    {
      "name": "string",
      "display_name": "string",
      "type": "string",
      "comment": "string"
    }
  ]
}

```

逻辑属性

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|属性名称。只能包含小写英文字母、数字、下划线（_）、连字符（-），且不能以下划线和连字符开头|
|display_name|string|false|none|属性显示名|
|type|string|false|none|属性数据类型。除了视图的字段类型之外，还有 metric、operator|
|comment|string|false|none|属性描述|
|index|boolean|false|none|是否开启索引，默认是true|
|data_source|[LogicSource](#schemalogicsource)|true|none|逻辑来源|
|parameters|[[Parameter](#schemaparameter)]|true|none|逻辑所需的参数|
|analysis_dimensions|[[AnalysisDim](#schemaanalysisdim)]|false|none|指标模型的分析维度。当逻辑属性类型为指标（metric）时返回此字段|

<h2 id="tocS_Object">Object</h2>
<!-- backwards compatibility -->
<a id="schemaobject"></a>
<a id="schema_Object"></a>
<a id="tocSobject"></a>
<a id="tocsobject"></a>

```json
{}

```

json，字段不定

### Properties

*None*

<h2 id="tocS_ID">ID</h2>
<!-- backwards compatibility -->
<a id="schemaid"></a>
<a id="schema_ID"></a>
<a id="tocSid"></a>
<a id="tocsid"></a>

```json
{
  "id": "string"
}

```

id

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|id|

<h2 id="tocS_ListObjectTypes">ListObjectTypes</h2>
<!-- backwards compatibility -->
<a id="schemalistobjecttypes"></a>
<a id="schema_ListObjectTypes"></a>
<a id="tocSlistobjecttypes"></a>
<a id="tocslistobjecttypes"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "concept_groups": [
        {
          "id": "string",
          "name": "string"
        }
      ],
      "data_source": {
        "type": "data_view",
        "id": "string",
        "name": "string"
      },
      "data_properties": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string",
          "mapped_field": {
            "name": "string",
            "display_name": "string",
            "type": "string"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 76
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "string"
          ]
        }
      ],
      "logic_properties": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string",
          "index": true,
          "data_source": {
            "type": "metric",
            "id": "string",
            "name": "string"
          },
          "parameters": [
            {
              "name": "string",
              "type": "string",
              "source": "string",
              "value_from": "property",
              "value": "string"
            }
          ],
          "analysis_dimensions": [
            {
              "name": "string",
              "display_name": "string",
              "type": "string",
              "comment": "string"
            }
          ]
        }
      ],
      "primary_keys": [
        "string"
      ],
      "display_key": "string",
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string",
      "module_type": "object_type"
    }
  ],
  "total_count": 0
}

```

对象类列表

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[ObjectTypeDetail](#schemaobjecttypedetail)]|true|none|条目列表|
|total_count|integer|true|none|总条数|

<h2 id="tocS_KnowledgeNetworkDetail">KnowledgeNetworkDetail</h2>
<!-- backwards compatibility -->
<a id="schemaknowledgenetworkdetail"></a>
<a id="schema_KnowledgeNetworkDetail"></a>
<a id="tocSknowledgenetworkdetail"></a>
<a id="tocsknowledgenetworkdetail"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "creator": "string",
  "create_time": 0,
  "updator": "string",
  "update_time": 0,
  "detail": "string"
}

```

业务知识网络详情

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|业务知识网络ID|
|name|string|true|none|业务知识网络名称|
|tags|[string]|true|none|标签，可为空|
|comment|string|true|none|备注，可为空|
|icon|string|true|none|图标|
|color|string|true|none|颜色|
|branch|string|true|none|分支ID|
|creator|string|true|none|创建人ID|
|create_time|integer(int64)|true|none|创建时间|
|updator|string|true|none|最近一次修改人|
|update_time|integer(int64)|true|none|最近一次更新时间|
|detail|string|true|none|说明书。按需返回，若指定了include_detail=true，则返回，否则不返回|

<h2 id="tocS_ReqObjectType">ReqObjectType</h2>
<!-- backwards compatibility -->
<a id="schemareqobjecttype"></a>
<a id="schema_ReqObjectType"></a>
<a id="tocSreqobjecttype"></a>
<a id="tocsreqobjecttype"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "concept_groups": [
    {
      "id": "string",
      "name": "string"
    }
  ],
  "data_source": {
    "type": "data_view",
    "id": "string",
    "name": "string"
  },
  "data_properties": [
    {
      "name": "string",
      "display_name": "string",
      "type": "string",
      "comment": "string",
      "mapped_field": {
        "name": "string",
        "display_name": "string",
        "type": "string"
      },
      "index_config": {
        "keyword_config": {
          "enabled": true,
          "ignore_above_len": 76
        },
        "fulltext_config": {
          "analyzer": "standard",
          "enabled": true
        },
        "vector_config": {
          "enabled": true,
          "model_id": "some text"
        }
      },
      "condition_operations": [
        "string"
      ]
    }
  ],
  "logic_properties": [
    {
      "name": "string",
      "display_name": "string",
      "type": "string",
      "comment": "string",
      "index": true,
      "data_source": {
        "type": "metric",
        "id": "string",
        "name": "string"
      },
      "parameters": [
        {
          "name": "string",
          "type": "string",
          "source": "string",
          "value_from": "property",
          "value": "string"
        }
      ],
      "analysis_dimensions": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string"
        }
      ]
    }
  ],
  "primary_keys": [
    "string"
  ],
  "display_key": "string"
}

```

对象类创建信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|false|none|ID.新建后不可修改，只能包含小写英文字母、数字、下划线（_）、连字符（-），且不能以下划线和连字符开头|
|name|string|true|none|名称|
|tags|[string]|false|none|标签。用于业务标识|
|comment|string|false|none|备注|
|icon|string|false|none|图标|
|color|string|false|none|颜色|
|branch|string|true|none|分支ID|
|concept_groups|[[ConceptGroup](#schemaconceptgroup)]|false|none|概念分组|
|data_source|[DataSource](#schemadatasource)|false|none|数据来源|
|data_properties|[[DataProperty](#schemadataproperty)]|true|none|数据属性|
|logic_properties|[[LogicProperty](#schemalogicproperty)]|false|none|逻辑属性|
|primary_keys|[string]|true|none|主键，唯一标识|
|display_key|string|true|none|对象的显示属性|

<h2 id="tocS_UpdateObjectType">UpdateObjectType</h2>
<!-- backwards compatibility -->
<a id="schemaupdateobjecttype"></a>
<a id="schema_UpdateObjectType"></a>
<a id="tocSupdateobjecttype"></a>
<a id="tocsupdateobjecttype"></a>

```json
{
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "concept_groups": [
    {
      "id": "string",
      "name": "string"
    }
  ],
  "data_source": {
    "type": "data_view",
    "id": "string",
    "name": "string"
  },
  "data_properties": [
    {
      "name": "string",
      "display_name": "string",
      "type": "string",
      "comment": "string",
      "mapped_field": {
        "name": "string",
        "display_name": "string",
        "type": "string"
      },
      "index_config": {
        "keyword_config": {
          "enabled": true,
          "ignore_above_len": 76
        },
        "fulltext_config": {
          "analyzer": "standard",
          "enabled": true
        },
        "vector_config": {
          "enabled": true,
          "model_id": "some text"
        }
      },
      "condition_operations": [
        "string"
      ]
    }
  ],
  "logic_properties": [
    {
      "name": "string",
      "display_name": "string",
      "type": "string",
      "comment": "string",
      "index": true,
      "data_source": {
        "type": "metric",
        "id": "string",
        "name": "string"
      },
      "parameters": [
        {
          "name": "string",
          "type": "string",
          "source": "string",
          "value_from": "property",
          "value": "string"
        }
      ],
      "analysis_dimensions": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string"
        }
      ]
    }
  ],
  "primary_keys": [
    "string"
  ],
  "display_key": "string"
}

```

更新对象类

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|名称|
|tags|[string]|false|none|标签。用于业务标识|
|comment|string|false|none|备注|
|icon|string|false|none|图标|
|color|string|false|none|颜色|
|branch|string|true|none|分支ID|
|concept_groups|[[ConceptGroup](#schemaconceptgroup)]|false|none|概念分组|
|data_source|[DataSource](#schemadatasource)|false|none|数据来源|
|data_properties|[[DataProperty](#schemadataproperty)]|true|none|数据属性|
|logic_properties|[[LogicProperty](#schemalogicproperty)]|false|none|逻辑属性|
|primary_keys|[string]|true|none|主键，唯一标识|
|display_key|string|true|none|对象的显示属性|

<h2 id="tocS_ViewField">ViewField</h2>
<!-- backwards compatibility -->
<a id="schemaviewfield"></a>
<a id="schema_ViewField"></a>
<a id="tocSviewfield"></a>
<a id="tocsviewfield"></a>

```json
{
  "name": "string",
  "display_name": "string",
  "type": "string"
}

```

视图字段信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|字段名称|
|display_name|string|false|none|字段显示名.查看时有此字段|
|type|string|false|none|视图字段类型，查看时有此字段|

<h2 id="tocS_AnalysisDim">AnalysisDim</h2>
<!-- backwards compatibility -->
<a id="schemaanalysisdim"></a>
<a id="schema_AnalysisDim"></a>
<a id="tocSanalysisdim"></a>
<a id="tocsanalysisdim"></a>

```json
{
  "name": "string",
  "display_name": "string",
  "type": "string",
  "comment": "string"
}

```

分析维度

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|分析维度名称,与视图名称一致|
|display_name|string|false|none|字段显示名，与视图的显示名一致|
|type|string|false|none|视图字段类型，与视图的字段类型一致|
|comment|string|false|none|分析维度描述，与视图的字段描述一致|

<h2 id="tocS_LogicSource">LogicSource</h2>
<!-- backwards compatibility -->
<a id="schemalogicsource"></a>
<a id="schema_LogicSource"></a>
<a id="tocSlogicsource"></a>
<a id="tocslogicsource"></a>

```json
{
  "type": "metric",
  "id": "string",
  "name": "string"
}

```

数据来源

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|type|string|true|none|数据来源类型|
|id|string|true|none|数据来源ID|
|name|string|false|none|名称。查看详情时返回。|

#### Enumerated Values

|Property|Value|
|---|---|
|type|metric|
|type|operator|

<h2 id="tocS_ObjectTypeDetail">ObjectTypeDetail</h2>
<!-- backwards compatibility -->
<a id="schemaobjecttypedetail"></a>
<a id="schema_ObjectTypeDetail"></a>
<a id="tocSobjecttypedetail"></a>
<a id="tocsobjecttypedetail"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "kn_id": "string",
  "concept_groups": [
    {
      "id": "string",
      "name": "string"
    }
  ],
  "data_source": {
    "type": "data_view",
    "id": "string",
    "name": "string"
  },
  "data_properties": [
    {
      "name": "string",
      "display_name": "string",
      "type": "string",
      "comment": "string",
      "mapped_field": {
        "name": "string",
        "display_name": "string",
        "type": "string"
      },
      "index_config": {
        "keyword_config": {
          "enabled": true,
          "ignore_above_len": 76
        },
        "fulltext_config": {
          "analyzer": "standard",
          "enabled": true
        },
        "vector_config": {
          "enabled": true,
          "model_id": "some text"
        }
      },
      "condition_operations": [
        "string"
      ]
    }
  ],
  "logic_properties": [
    {
      "name": "string",
      "display_name": "string",
      "type": "string",
      "comment": "string",
      "index": true,
      "data_source": {
        "type": "metric",
        "id": "string",
        "name": "string"
      },
      "parameters": [
        {
          "name": "string",
          "type": "string",
          "source": "string",
          "value_from": "property",
          "value": "string"
        }
      ],
      "analysis_dimensions": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string"
        }
      ]
    }
  ],
  "primary_keys": [
    "string"
  ],
  "display_key": "string",
  "creator": "string",
  "create_time": 0,
  "updater": "string",
  "update_time": 0,
  "detail": "string",
  "module_type": "object_type"
}

```

节点（对象类）信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|对象类ID|
|name|string|true|none|对象类名称|
|tags|[string]|true|none|标签。 （可以为空）|
|comment|string|true|none|备注（可以为空）|
|icon|string|true|none|图标|
|color|string|true|none|颜色|
|branch|string|true|none|分支ID|
|kn_id|string|true|none|业务知识网络id|
|concept_groups|[[ConceptGroup](#schemaconceptgroup)]|true|none|概念分组id|
|data_source|[DataSource](#schemadatasource)|true|none|数据来源|
|data_properties|[[DataProperty](#schemadataproperty)]|true|none|数据属性|
|logic_properties|[[LogicProperty](#schemalogicproperty)]|true|none|逻辑属性|
|primary_keys|[string]|true|none|主键|
|display_key|string|true|none|对象实例的显示属性|
|creator|string|true|none|创建人ID|
|create_time|integer(int64)|true|none|创建时间|
|updater|string|true|none|最近一次修改人|
|update_time|integer(int64)|true|none|最近一次更新时间|
|detail|string|true|none|说明书。按需返回，若指定了include_detail=true，则返回，否则不返回。列表查询时不返回此字段|
|module_type|string|true|none|模块类型|

#### Enumerated Values

|Property|Value|
|---|---|
|module_type|object_type|

<h2 id="tocS_ConceptGroup">ConceptGroup</h2>
<!-- backwards compatibility -->
<a id="schemaconceptgroup"></a>
<a id="schema_ConceptGroup"></a>
<a id="tocSconceptgroup"></a>
<a id="tocsconceptgroup"></a>

```json
{
  "id": "string",
  "name": "string"
}

```

概念分组

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|概念分组ID|
|name|string|true|none|概念分组名称|

<h2 id="tocS_override">override</h2>
<!-- backwards compatibility -->
<a id="schemaoverride"></a>
<a id="schema_override"></a>
<a id="tocSoverride"></a>
<a id="tocsoverride"></a>

```json
{}

```

post 重载批量创建、对象类检索接口

### Properties

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|object|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[ReqObjectTypes](#schemareqobjecttypes)|false|none|批量创建请求体|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[override--get](#schemaoverride--get)|false|none|对象类检索请求体|

<h2 id="tocS_Sort">Sort</h2>
<!-- backwards compatibility -->
<a id="schemasort"></a>
<a id="schema_Sort"></a>
<a id="tocSsort"></a>
<a id="tocssort"></a>

```json
{
  "field": "string",
  "direction": "desc"
}

```

排序字段。默认按 _score 倒序排序

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|排序字段|
|direction|string|true|none|排序方向|

#### Enumerated Values

|Property|Value|
|---|---|
|direction|desc|
|direction|asc|

<h2 id="tocS_override--get">override--get</h2>
<!-- backwards compatibility -->
<a id="schemaoverride--get"></a>
<a id="schema_override--get"></a>
<a id="tocSoverride--get"></a>
<a id="tocsoverride--get"></a>

```json
{
  "concept_groups": [
    "string"
  ],
  "condition": {
    "operation": "and",
    "sub_conditions": [
      {
        "operation": "and",
        "sub_conditions": []
      }
    ]
  },
  "sort": [
    {
      "field": "string",
      "direction": "desc"
    }
  ],
  "limit": 0,
  "need_total": true
}

```

对象类检索请求体

### Properties

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[FirstQueryWithSearchAfter](#schemafirstquerywithsearchafter)|false|none|对象类检索第一次请求|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[PageTurnQueryWithSearchAfter](#schemapageturnquerywithsearchafter)|false|none|分页查询的后续分页查询请求|

<h2 id="tocS_ObjectTypeSearchResponse">ObjectTypeSearchResponse</h2>
<!-- backwards compatibility -->
<a id="schemaobjecttypesearchresponse"></a>
<a id="schema_ObjectTypeSearchResponse"></a>
<a id="tocSobjecttypesearchresponse"></a>
<a id="tocsobjecttypesearchresponse"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "concept_groups": [
        {
          "id": "string",
          "name": "string"
        }
      ],
      "data_source": {
        "type": "data_view",
        "id": "string",
        "name": "string"
      },
      "data_properties": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string",
          "mapped_field": {
            "name": "string",
            "display_name": "string",
            "type": "string"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 76
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "string"
          ]
        }
      ],
      "logic_properties": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string",
          "index": true,
          "data_source": {
            "type": "metric",
            "id": "string",
            "name": "string"
          },
          "parameters": [
            {
              "name": "string",
              "type": "string",
              "source": "string",
              "value_from": "property",
              "value": "string"
            }
          ],
          "analysis_dimensions": [
            {
              "name": "string",
              "display_name": "string",
              "type": "string",
              "comment": "string"
            }
          ]
        }
      ],
      "primary_keys": [
        "string"
      ],
      "display_key": "string",
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string",
      "module_type": "object_type"
    }
  ],
  "total_count": 0,
  "search_after": [
    null
  ]
}

```

对象类检索返回结果

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[ObjectTypeDetail](#schemaobjecttypedetail)]|true|none|对象实例数据|
|total_count|integer|false|none|总条数|
|search_after|[any]|true|none|表示返回的最后一个文档的排序值，获取这个用于下一次 search_after 分页。|

<h2 id="tocS_Parameter4Operator">Parameter4Operator</h2>
<!-- backwards compatibility -->
<a id="schemaparameter4operator"></a>
<a id="schema_Parameter4Operator"></a>
<a id="tocSparameter4operator"></a>
<a id="tocsparameter4operator"></a>

```json
{
  "name": "string",
  "type": "string",
  "source": "string",
  "value_from": "property",
  "value": "string"
}

```

逻辑参数

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|参数名称|
|type|string|false|none|参数类型|
|source|string|false|none|参数来源|
|value_from|string|true|none|值来源|
|value|string|false|none|参数值。value_from=property时，填入的是对象类的数据属性名称；value_from=input时，不设置此字段|

#### Enumerated Values

|Property|Value|
|---|---|
|value_from|property|
|value_from|input|

<h2 id="tocS_Parameter">Parameter</h2>
<!-- backwards compatibility -->
<a id="schemaparameter"></a>
<a id="schema_Parameter"></a>
<a id="tocSparameter"></a>
<a id="tocsparameter"></a>

```json
{
  "name": "string",
  "type": "string",
  "source": "string",
  "value_from": "property",
  "value": "string"
}

```

逻辑/指标参数

### Properties

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[Parameter4Operator](#schemaparameter4operator)|false|none|逻辑参数|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[Parameter4Metric](#schemaparameter4metric)|false|none|逻辑参数|

<h2 id="tocS_Parameter4Metric">Parameter4Metric</h2>
<!-- backwards compatibility -->
<a id="schemaparameter4metric"></a>
<a id="schema_Parameter4Metric"></a>
<a id="tocSparameter4metric"></a>
<a id="tocsparameter4metric"></a>

```json
{
  "name": "string",
  "value_from": "property",
  "value": "string",
  "operation": "in"
}

```

逻辑参数

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|参数名称|
|value_from|string|true|none|值来源|
|value|string|false|none|参数值。value_from=property时，填入的是对象类的数据属性名称；value_from=input时，不设置此字段|
|operation|string|true|none|操作符。映射指标模型的属性时，此字段必须|

#### Enumerated Values

|Property|Value|
|---|---|
|value_from|property|
|value_from|input|
|operation|in|
|operation|=|
|operation|!=|
|operation|>|
|operation|>=|
|operation|<|
|operation|<=|

<h2 id="tocS_IndexConfig">IndexConfig</h2>
<!-- backwards compatibility -->
<a id="schemaindexconfig"></a>
<a id="schema_IndexConfig"></a>
<a id="tocSindexconfig"></a>
<a id="tocsindexconfig"></a>

```json
{
  "keyword_config": {
    "enabled": true,
    "ignore_above_len": 76
  },
  "fulltext_config": {
    "analyzer": "standard",
    "enabled": true
  },
  "vector_config": {
    "enabled": true,
    "model_id": "some text"
  }
}

```

Root Type for IndexConfig

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|keyword_config|[KeywordConfig](#schemakeywordconfig)|false|none|关键字检索配置|
|fulltext_config|[FulltextConfig](#schemafulltextconfig)|false|none|全文检索配置|
|vector_config|[VectorConfig](#schemavectorconfig)|false|none|向量检索配置|

<h2 id="tocS_KeywordConfig">KeywordConfig</h2>
<!-- backwards compatibility -->
<a id="schemakeywordconfig"></a>
<a id="schema_KeywordConfig"></a>
<a id="tocSkeywordconfig"></a>
<a id="tocskeywordconfig"></a>

```json
{
  "enabled": true,
  "ignore_above_len": 52
}

```

Root Type for KeywordConfig

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|enabled|boolean|false|none|是否启用|
|ignore_above_len|integer(int32)|false|none|忽略数据的长度上限|

<h2 id="tocS_ObjectTypeDetails">ObjectTypeDetails</h2>
<!-- backwards compatibility -->
<a id="schemaobjecttypedetails"></a>
<a id="schema_ObjectTypeDetails"></a>
<a id="tocSobjecttypedetails"></a>
<a id="tocsobjecttypedetails"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "concept_groups": [
        {
          "id": "string",
          "name": "string"
        }
      ],
      "data_source": {
        "type": "data_view",
        "id": "string",
        "name": "string"
      },
      "data_properties": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string",
          "mapped_field": {
            "name": "string",
            "display_name": "string",
            "type": "string"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 76
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "string"
          ]
        }
      ],
      "logic_properties": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string",
          "index": true,
          "data_source": {
            "type": "metric",
            "id": "string",
            "name": "string"
          },
          "parameters": [
            {
              "name": "string",
              "type": "string",
              "source": "string",
              "value_from": "property",
              "value": "string"
            }
          ],
          "analysis_dimensions": [
            {
              "name": "string",
              "display_name": "string",
              "type": "string",
              "comment": "string"
            }
          ]
        }
      ],
      "primary_keys": [
        "string"
      ],
      "display_key": "string",
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string",
      "module_type": "object_type"
    }
  ]
}

```

对象类详情

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[ObjectTypeDetail](#schemaobjecttypedetail)]|true|none|对象类数组|

<h2 id="tocS_ReqObjectTypes">ReqObjectTypes</h2>
<!-- backwards compatibility -->
<a id="schemareqobjecttypes"></a>
<a id="schema_ReqObjectTypes"></a>
<a id="tocSreqobjecttypes"></a>
<a id="tocsreqobjecttypes"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "concept_groups": [
        {
          "id": "string",
          "name": "string"
        }
      ],
      "data_source": {
        "type": "data_view",
        "id": "string",
        "name": "string"
      },
      "data_properties": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string",
          "mapped_field": {
            "name": "string",
            "display_name": "string",
            "type": "string"
          },
          "index_config": {
            "keyword_config": {
              "enabled": true,
              "ignore_above_len": 76
            },
            "fulltext_config": {
              "analyzer": "standard",
              "enabled": true
            },
            "vector_config": {
              "enabled": true,
              "model_id": "some text"
            }
          },
          "condition_operations": [
            "string"
          ]
        }
      ],
      "logic_properties": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string",
          "index": true,
          "data_source": {
            "type": "metric",
            "id": "string",
            "name": "string"
          },
          "parameters": [
            {
              "name": "string",
              "type": "string",
              "source": "string",
              "value_from": "property",
              "value": "string"
            }
          ],
          "analysis_dimensions": [
            {
              "name": "string",
              "display_name": "string",
              "type": "string",
              "comment": "string"
            }
          ]
        }
      ],
      "primary_keys": [
        "string"
      ],
      "display_key": "string"
    }
  ]
}

```

批量创建请求体

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[ReqObjectType](#schemareqobjecttype)]|true|none|对象类信息|

<h2 id="tocS_DataProperties">DataProperties</h2>
<!-- backwards compatibility -->
<a id="schemadataproperties"></a>
<a id="schema_DataProperties"></a>
<a id="tocSdataproperties"></a>
<a id="tocsdataproperties"></a>

```json
{
  "entries": [
    {
      "name": "string",
      "display_name": "string",
      "type": "string",
      "comment": "string",
      "mapped_field": {
        "name": "string",
        "display_name": "string",
        "type": "string"
      },
      "index_config": {
        "keyword_config": {
          "enabled": true,
          "ignore_above_len": 76
        },
        "fulltext_config": {
          "analyzer": "standard",
          "enabled": true
        },
        "vector_config": {
          "enabled": true,
          "model_id": "some text"
        }
      },
      "condition_operations": [
        "string"
      ]
    }
  ]
}

```

数据属性集

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[DataProperty](#schemadataproperty)]|true|none|数据属性|

<h2 id="tocS_condition_or">condition_or</h2>
<!-- backwards compatibility -->
<a id="schemacondition_or"></a>
<a id="schema_condition_or"></a>
<a id="tocScondition_or"></a>
<a id="tocscondition_or"></a>

```json
{
  "operation": "or",
  "sub_conditions": [
    {
      "operation": "and",
      "sub_conditions": [
        {
          "operation": "and",
          "sub_conditions": []
        }
      ]
    }
  ]
}

```

or 的过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|operation|string|true|none|过滤操作符|
|sub_conditions|[[Condition](#schemacondition)]|true|none|子过滤条件|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|or|

<h2 id="tocS_condition_eq">condition_eq</h2>
<!-- backwards compatibility -->
<a id="schemacondition_eq"></a>
<a id="schema_condition_eq"></a>
<a id="tocScondition_eq"></a>
<a id="tocscondition_eq"></a>

```json
{
  "field": "id",
  "operation": "==",
  "value": null
}

```

等于过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，等于支持的字段类型：数值、字符串|
|operation|string|true|none|操作符|
|value|any|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|id|
|field|name|
|field|comment|
|field|detail|
|field|data_properties.name|
|field|data_properties.display_name|
|field|data_properties.comment|
|field|logic_properties.name|
|field|logic_properties.display_name|
|field|logic_properties.comment|
|operation|==|

<h2 id="tocS_condition_not_eq">condition_not_eq</h2>
<!-- backwards compatibility -->
<a id="schemacondition_not_eq"></a>
<a id="schema_condition_not_eq"></a>
<a id="tocScondition_not_eq"></a>
<a id="tocscondition_not_eq"></a>

```json
{
  "field": "id",
  "operation": "!=",
  "value": null
}

```

不等于的过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，不等于支持的字段类型：数值、字符串|
|operation|string|true|none|操作符|
|value|any|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|id|
|field|name|
|field|comment|
|field|detail|
|field|data_properties.name|
|field|data_properties.display_name|
|field|data_properties.comment|
|field|logic_properties.name|
|field|logic_properties.display_name|
|field|logic_properties.comment|
|operation|!=|

<h2 id="tocS_condition_in">condition_in</h2>
<!-- backwards compatibility -->
<a id="schemacondition_in"></a>
<a id="schema_condition_in"></a>
<a id="tocScondition_in"></a>
<a id="tocscondition_in"></a>

```json
{
  "field": "id",
  "operation": "in",
  "value": [
    null
  ]
}

```

包含过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，包含支持所有类型|
|operation|string|true|none|操作符|
|value|[any]|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|id|
|field|name|
|field|comment|
|field|detail|
|field|data_properties.name|
|field|data_properties.display_name|
|field|data_properties.comment|
|field|logic_properties.name|
|field|logic_properties.display_name|
|field|logic_properties.comment|
|operation|in|

<h2 id="tocS_condition_like">condition_like</h2>
<!-- backwards compatibility -->
<a id="schemacondition_like"></a>
<a id="schema_condition_like"></a>
<a id="tocScondition_like"></a>
<a id="tocscondition_like"></a>

```json
{
  "field": "id",
  "operation": "like",
  "value": "string"
}

```

like过滤

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，相似支持的字段类型：字符串|
|operation|string|true|none|操作符|
|value|string|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|id|
|field|name|
|field|comment|
|field|detail|
|field|data_properties.name|
|field|data_properties.display_name|
|field|data_properties.comment|
|field|logic_properties.name|
|field|logic_properties.display_name|
|field|logic_properties.comment|
|operation|like|

<h2 id="tocS_condition_not_like">condition_not_like</h2>
<!-- backwards compatibility -->
<a id="schemacondition_not_like"></a>
<a id="schema_condition_not_like"></a>
<a id="tocScondition_not_like"></a>
<a id="tocscondition_not_like"></a>

```json
{
  "field": "id",
  "operation": "not_like",
  "value": "string"
}

```

not_like过滤

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，不相似支持的字段类型：字符串|
|operation|string|true|none|操作符|
|value|string|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|id|
|field|name|
|field|comment|
|field|detail|
|field|data_properties.name|
|field|data_properties.display_name|
|field|data_properties.comment|
|field|logic_properties.name|
|field|logic_properties.display_name|
|field|logic_properties.comment|
|operation|not_like|

<h2 id="tocS_condition_regex">condition_regex</h2>
<!-- backwards compatibility -->
<a id="schemacondition_regex"></a>
<a id="schema_condition_regex"></a>
<a id="tocScondition_regex"></a>
<a id="tocscondition_regex"></a>

```json
{
  "field": "id",
  "operation": "regex",
  "value": "string"
}

```

regex过滤

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，正则支持的字段类型：字符串|
|operation|string|true|none|操作符|
|value|string|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|id|
|field|name|
|field|comment|
|field|detail|
|field|data_properties.name|
|field|data_properties.display_name|
|field|data_properties.comment|
|field|logic_properties.name|
|field|logic_properties.display_name|
|field|logic_properties.comment|
|operation|regex|

<h2 id="tocS_condition_multi_match">condition_multi_match</h2>
<!-- backwards compatibility -->
<a id="schemacondition_multi_match"></a>
<a id="schema_condition_multi_match"></a>
<a id="tocScondition_multi_match"></a>
<a id="tocscondition_multi_match"></a>

```json
{
  "fields": [
    "string"
  ],
  "operation": "multi_match",
  "value": "string",
  "match_type": "best_fields"
}

```

多字段全文匹配

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|fields|[string]|false|none|过滤字段数组，多字段全文匹配支持的字段类型：字符串。为空时，用opensearch中 index.default_field 配置的字段进行查询。当需要对所有字段进行匹配时，此参数传 ["*"].可支持的字段为 name, comment, detail, *, data_properties.name, data_properties.display_name, data_properties.comment, logic_properties.name, logic_properties.display_name, logic_properties.comment|
|operation|string|true|none|操作符|
|value|string|true|none|过滤值|
|match_type|string|false|none|全文匹配类型，默认是 best_fields|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|multi_match|
|match_type|best_fields|
|match_type|most_fields|
|match_type|cross_fields|
|match_type|phrase|
|match_type|phrase_prefix|
|match_type|bool_prefix|

<h2 id="tocS_condition_not_in">condition_not_in</h2>
<!-- backwards compatibility -->
<a id="schemacondition_not_in"></a>
<a id="schema_condition_not_in"></a>
<a id="tocScondition_not_in"></a>
<a id="tocscondition_not_in"></a>

```json
{
  "field": "id",
  "operation": "not_in",
  "value": [
    null
  ]
}

```

not_in过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，不包含支持所有类型|
|operation|string|true|none|操作符|
|value|[any]|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|id|
|field|name|
|field|comment|
|field|detail|
|field|data_properties.name|
|field|data_properties.display_name|
|field|data_properties.comment|
|field|logic_properties.name|
|field|logic_properties.display_name|
|field|logic_properties.comment|
|operation|not_in|

<h2 id="tocS_condition_match_phrase">condition_match_phrase</h2>
<!-- backwards compatibility -->
<a id="schemacondition_match_phrase"></a>
<a id="schema_condition_match_phrase"></a>
<a id="tocScondition_match_phrase"></a>
<a id="tocscondition_match_phrase"></a>

```json
{
  "field": "name",
  "operation": "match_phrase",
  "value": "string"
}

```

match_phrase 过滤，支持单个字段和*, * 表示全部字段

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，短语匹配支持的字段类型：字符串|
|operation|string|true|none|操作符|
|value|string|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|name|
|field|comment|
|field|detail|
|field|*|
|field|data_properties.name|
|field|data_properties.display_name|
|field|data_properties.comment|
|field|logic_properties.name|
|field|logic_properties.display_name|
|field|logic_properties.comment|
|operation|match_phrase|

<h2 id="tocS_condition_match">condition_match</h2>
<!-- backwards compatibility -->
<a id="schemacondition_match"></a>
<a id="schema_condition_match"></a>
<a id="tocScondition_match"></a>
<a id="tocscondition_match"></a>

```json
{
  "field": "name",
  "operation": "match",
  "value": "string"
}

```

match 过滤，支持单个字段和*, * 表示全部字段

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，全文匹配支持的字段类型：字符串|
|operation|string|true|none|操作符|
|value|string|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|name|
|field|comment|
|field|detail|
|field|*|
|field|data_properties.name|
|field|data_properties.display_name|
|field|data_properties.comment|
|field|logic_properties.name|
|field|logic_properties.display_name|
|field|logic_properties.comment|
|operation|match|

<h2 id="tocS_condition_knn">condition_knn</h2>
<!-- backwards compatibility -->
<a id="schemacondition_knn"></a>
<a id="schema_condition_knn"></a>
<a id="tocScondition_knn"></a>
<a id="tocscondition_knn"></a>

```json
{
  "field": "*",
  "operation": "knn",
  "value": 0,
  "limit_key": "k",
  "limit_value": 100,
  "sub_conditions": [
    {
      "operation": "and",
      "sub_conditions": [
        {
          "operation": "and",
          "sub_conditions": []
        }
      ]
    }
  ]
}

```

knn 过滤，支持单个字段和*, * 表示"_vector"

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段,概念索引是内部生成，不对外暴露，所以knn过滤时，field 传 * 即可|
|operation|string|true|none|操作符|
|value|number|true|none|过滤值。当limit_key为k时，limit_value为整型；当limit_key为max_distance和min_score时，limit_value为浮点型|
|limit_key|string|false|none|执行径向搜索时使用的过滤和评分行为, k:返回最相似的limit_value个结果；max_distance:返回距离小于等于limit_value的结果；min_score：返回相似度分数大于等于limit_value的结果。默认值为k|
|limit_value|number|false|none|执行径向搜索使用的值。默认值为100|
|sub_conditions|[[Condition](#schemacondition)]|false|none|knn下的子查询|

#### Enumerated Values

|Property|Value|
|---|---|
|field|*|
|operation|knn|
|limit_key|k|
|limit_key|max_distance|
|limit_key|min_score|

<h2 id="tocS_FirstQueryWithSearchAfter">FirstQueryWithSearchAfter</h2>
<!-- backwards compatibility -->
<a id="schemafirstquerywithsearchafter"></a>
<a id="schema_FirstQueryWithSearchAfter"></a>
<a id="tocSfirstquerywithsearchafter"></a>
<a id="tocsfirstquerywithsearchafter"></a>

```json
{
  "concept_groups": [
    "string"
  ],
  "condition": {
    "operation": "and",
    "sub_conditions": [
      {
        "operation": "and",
        "sub_conditions": []
      }
    ]
  },
  "sort": [
    {
      "field": "string",
      "direction": "desc"
    }
  ],
  "limit": 0,
  "need_total": true
}

```

对象类检索第一次请求

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|concept_groups|[string]|false|none|概念分组id数组|
|condition|[Condition](#schemacondition)|false|none|对象类检索条件|
|sort|[[Sort](#schemasort)]|false|none|排序字段，默认使用 _score 排序，排序方向为 desc|
|limit|integer|true|none|返回的数量，默认值 10。范围 1-10000|
|need_total|boolean|false|none|是否需要总数，默认false|

<h2 id="tocS_PageTurnQueryWithSearchAfter">PageTurnQueryWithSearchAfter</h2>
<!-- backwards compatibility -->
<a id="schemapageturnquerywithsearchafter"></a>
<a id="schema_PageTurnQueryWithSearchAfter"></a>
<a id="tocSpageturnquerywithsearchafter"></a>
<a id="tocspageturnquerywithsearchafter"></a>

```json
{
  "concept_groups": [
    "string"
  ],
  "condition": {
    "operation": "and",
    "sub_conditions": [
      {
        "operation": "and",
        "sub_conditions": []
      }
    ]
  },
  "sort": [
    {
      "field": "string",
      "direction": "desc"
    }
  ],
  "limit": 0,
  "need_total": true,
  "search_after": [
    null
  ]
}

```

分页查询的后续分页查询请求

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|concept_groups|[string]|false|none|概念分组id数组|
|condition|[Condition](#schemacondition)|true|none|过滤条件|
|sort|[[Sort](#schemasort)]|false|none|排序字段，默认使用 _score 排序，排序方向为 desc|
|limit|integer|true|none|返回的数量，默认值 10。范围 1-10000|
|need_total|boolean|false|none|是否需要总数，默认false|
|search_after|[any]|true|none|上次查询返回的最后一个文档的排序值。|

<h2 id="tocS_condition_and">condition_and</h2>
<!-- backwards compatibility -->
<a id="schemacondition_and"></a>
<a id="schema_condition_and"></a>
<a id="tocScondition_and"></a>
<a id="tocscondition_and"></a>

```json
{
  "operation": "and",
  "sub_conditions": [
    {
      "operation": "and",
      "sub_conditions": []
    }
  ]
}

```

and的过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|operation|string|true|none|过滤操作符|
|sub_conditions|[[Condition](#schemacondition)]|true|none|子过滤条件|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|and|

<h2 id="tocS_Condition">Condition</h2>
<!-- backwards compatibility -->
<a id="schemacondition"></a>
<a id="schema_Condition"></a>
<a id="tocScondition"></a>
<a id="tocscondition"></a>

```json
{
  "operation": "and",
  "sub_conditions": [
    {
      "operation": "and",
      "sub_conditions": []
    }
  ]
}

```

### Properties

anyOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_and](#schemacondition_and)|false|none|and的过滤条件|

or

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_or](#schemacondition_or)|false|none|or 的过滤条件|

or

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_eq](#schemacondition_eq)|false|none|等于过滤条件|

or

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_not_eq](#schemacondition_not_eq)|false|none|不等于的过滤条件|

or

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_in](#schemacondition_in)|false|none|包含过滤条件|

or

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_not_in](#schemacondition_not_in)|false|none|not_in过滤条件|

or

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_like](#schemacondition_like)|false|none|like过滤|

or

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_not_like](#schemacondition_not_like)|false|none|not_like过滤|

or

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_regex](#schemacondition_regex)|false|none|regex过滤|

or

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_match](#schemacondition_match)|false|none|match 过滤，支持单个字段和*, * 表示全部字段|

or

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_match_phrase](#schemacondition_match_phrase)|false|none|match_phrase 过滤，支持单个字段和*, * 表示全部字段|

or

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_knn](#schemacondition_knn)|false|none|knn 过滤，支持单个字段和*, * 表示"_vector"|

or

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_multi_match](#schemacondition_multi_match)|false|none|多字段全文匹配|



<!-- Generator: Widdershins v4.0.1 -->

<h1 id="relationtype">RelationType v0.1.0</h1>


<h1 id="relationtype-default">Default</h1>

## 获取关系类列表

`GET /api/bkn-backend/v1/knowledge-networks/{kn_id}/relation-types`

<h3 id="获取关系类列表-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|name_pattern|query|string|false|根据络名称模糊查询，默认为空|
|sort|query|string|false|排序类型，默认是update_time|
|direction|query|string|false|排序结果方向，可选asc、desc。|
|offset|query|integer(int64)|false|开始响应的项目的偏移量	|
|limit|query|integer(int64)|false|每页最多可返回的项目数；|
|tag|query|string|false|根据标签精准查询，默认为空.|
|source_object_type_id|query|string|false|起点对象类ID|
|target_object_type_id|query|string|false|终点对象类ID|
|bound_object_type_id|query|array[string]|false|绑定的对象类ID，查询起点等于此对象类或者终点等于此对象类的关系类。支持传入多个值|

#### Detailed descriptions

**direction**: 排序结果方向，可选asc、desc。
默认desc

**offset**: 开始响应的项目的偏移量	
范围需大于等于0，默认值0

**limit**: 每页最多可返回的项目数；
分页可选1-1000，-1表示不分页；
默认值10

#### Enumerated Values

|Parameter|Value|
|---|---|
|sort|update_time|
|sort|name|
|direction|asc|
|direction|desc|

> Example responses

> 200 Response

```json
{
  "entries": [
    {
      "concept_type": "relation_type",
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "source_object_type_id": "string",
      "target_object_type_id": "string",
      "type": "direct",
      "mapping_rules": [
        {
          "target_property": {
            "name": "string",
            "display_name": "string"
          },
          "source_property": {
            "name": "string",
            "display_name": "string"
          }
        }
      ],
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string"
    }
  ],
  "total_count": 0
}
```

<h3 id="获取关系类列表-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[ListRelationTypes](#schemalistrelationtypes)|

<aside class="success">
This operation does not require authentication
</aside>

## 创建或检索关系类

`POST /api/bkn-backend/v1/knowledge-networks/{kn_id}/relation-types`

> Body parameter

```json
[
  {
    "id": "service_2_pod",
    "name": "包含",
    "tags": [
      "拓扑架构"
    ],
    "comment": "服务到pod的关系，服务包含于pod",
    "icon": "",
    "color": "",
    "branch": "main",
    "source_object_type_id": "service",
    "target_object_type_id": "pod",
    "type": "direct",
    "mapping_rules": [
      {
        "source_property": {
          "name": "service_name"
        },
        "target_property": {
          "name": "service_name"
        }
      }
    ],
    "module_type": "relation_type"
  },
  {
    "id": "pod_2_node",
    "name": "属于",
    "tags": [
      "拓扑架构"
    ],
    "comment": "pod到node的关系，pod包含于节点",
    "icon": "",
    "color": "",
    "branch": "main",
    "source_object_type_id": "pod",
    "target_object_type_id": "node",
    "type": "direct",
    "mapping_rules": [
      {
        "source_property": {
          "name": "pod_node_name"
        },
        "target_property": {
          "name": "node_name"
        }
      }
    ],
    "module_type": "relation_type"
  }
]
```

<h3 id="创建或检索关系类-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|x-http-method-override|header|string|true|重载请求头|
|body|body|[override](#schemaoverride)|true|none|

#### Enumerated Values

|Parameter|Value|
|---|---|
|x-http-method-override|POST|
|x-http-method-override|GET|

> Example responses

> 重载GET, 检索关系类

```json
{
  "entries": [
    {
      "id": "pod_2_node_TEST",
      "name": "属于2",
      "tags": [
        "拓扑架构"
      ],
      "comment": "pod到node的关系，pod包含于节点",
      "icon": "",
      "color": "",
      "branch": "main",
      "detail": "",
      "creator": "a0f02238-6cec-11f0-82bb-fa1c4529a151",
      "create_time": 1758339660119,
      "updater": "a0f02238-6cec-11f0-82bb-fa1c4529a151",
      "update_time": 1758339660119,
      "kn_id": "kn_system_incident_event_network",
      "source_object_type_id": "pod",
      "source_object_type": {
        "id": "",
        "name": "",
        "icon": "",
        "color": ""
      },
      "target_object_type_id": "node",
      "target_object_type": {
        "id": "",
        "name": "",
        "icon": "",
        "color": ""
      },
      "type": "direct",
      "mapping_rules": [
        {
          "source_property": {
            "name": "pod_node_name"
          },
          "target_property": {
            "name": "node_name"
          }
        }
      ],
      "IfNameModify": false,
      "module_type": "relation_type"
    }
  ],
  "total_count": 2,
  "search_after": [
    3.7951493,
    "pod_2_node_TEST"
  ]
}
```

```json
{
  "entries": [
    {
      "id": "service_2_pod_test",
      "name": "包含2",
      "tags": [
        "拓扑架构"
      ],
      "comment": "服务到pod的关系，服务包含于pod",
      "icon": "",
      "color": "",
      "branch": "main",
      "detail": "",
      "creator": "a0f02238-6cec-11f0-82bb-fa1c4529a151",
      "create_time": 1758339660119,
      "updater": "a0f02238-6cec-11f0-82bb-fa1c4529a151",
      "update_time": 1758339660119,
      "kn_id": "kn_system_incident_event_network",
      "source_object_type_id": "service",
      "source_object_type": {
        "id": "",
        "name": "",
        "icon": "",
        "color": ""
      },
      "target_object_type_id": "pod",
      "target_object_type": {
        "id": "",
        "name": "",
        "icon": "",
        "color": ""
      },
      "type": "direct",
      "mapping_rules": [
        {
          "source_property": {
            "name": "service_name"
          },
          "target_property": {
            "name": "service_name"
          }
        }
      ],
      "IfNameModify": false,
      "module_type": "relation_type"
    }
  ],
  "search_after": [
    3.782332,
    "service_2_pod_test"
  ]
}
```

> 201 Response

```json
[
  {
    "id": "string"
  }
]
```

<h3 id="创建或检索关系类-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|重载GET, 检索关系类|[RelationTypeSearchResponse](#schemarelationtypesearchresponse)|
|201|[Created](https://tools.ietf.org/html/rfc7231#section-6.3.2)|ok|Inline|

<h3 id="创建或检索关系类-responseschema">Response Schema</h3>

Status Code **201**

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[[ID](#schemaid)]|false|none|[id]|
|» id|string|true|none|id|

<aside class="success">
This operation does not require authentication
</aside>

## 修改关系类

`PUT /api/bkn-backend/v1/knowledge-networks/{kn_id}/relation-types/{rt_id}`

> Body parameter

```json
{
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "source_object_type_id": "string",
  "target_object_type_id": "string",
  "type": "direct",
  "mapping_rules": [
    {
      "target_property": {
        "name": "string",
        "display_name": "string"
      },
      "source_property": {
        "name": "string",
        "display_name": "string"
      }
    }
  ]
}
```

<h3 id="修改关系类-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|rt_id|path|string|true|关系类ID|
|branch|query|string|false|分支，不填则使用 main 分支|
|strict_mode|query|boolean|false|是否严格校验依赖，默认为 true。为 true 时校验关联对象类、数据视图等依赖是否存在；为 false 时不做该校验|
|validate_dependency|query|boolean|false|[已废弃] 请使用 strict_mode。兼容保留，strict_mode 为空时会读取此参数|
|body|body|[UpdateRelationType](#schemaupdaterelationtype)|true|none|

<h3 id="修改关系类-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|ok|None|

<aside class="success">
This operation does not require authentication
</aside>

## 获取关系类详情

`GET /api/bkn-backend/v1/knowledge-networks/{kn_id}/relation-types/{rt_ids}`

<h3 id="获取关系类详情-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|rt_ids|path|array[string]|true|关系类ID|
|kn_id|path|string|true|业务知识网络ID|
|include_detail|query|boolean|false|是否包含说明书信息，默认false，不包含。|

> Example responses

> 200 Response

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "source_object_type_id": "string",
      "source_object_type": {
        "id": "string",
        "name": "string",
        "icon": "string",
        "color": "string"
      },
      "target_object_type_id": "string",
      "target_object_type": {
        "id": "string",
        "name": "string",
        "icon": "string",
        "color": "string"
      },
      "type": "direct",
      "mapping_rules": [
        {
          "target_property": {
            "name": "string",
            "display_name": "string"
          },
          "source_property": {
            "name": "string",
            "display_name": "string"
          }
        }
      ],
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string"
    }
  ]
}
```

<h3 id="获取关系类详情-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[RelationTypeDetails](#schemarelationtypedetails)|

<aside class="success">
This operation does not require authentication
</aside>

## 删除关系类

`DELETE /api/bkn-backend/v1/knowledge-networks/{kn_id}/relation-types/{rt_ids}`

<h3 id="删除关系类-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|rt_ids|path|array[string]|true|关系类ID|

<h3 id="删除关系类-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|ok|None|

<aside class="success">
This operation does not require authentication
</aside>

## 从起点探索概念路径

`POST /api/bkn-backend/v1/knowledge-networks/{kn_id}/relation-type-paths`

从起点探索概念路径

> Body parameter

```json
{
  "source_object_type_id": "comment",
  "direction": "backward",
  "path_length": 1
}
```

<h3 id="从起点探索概念路径-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|undefined|true|none|
|body|body|[RelationPathReqeustBody](#schemarelationpathreqeustbody)|true|none|

> Example responses

> ok

```json
[
  {
    "object_types": [
      {
        "id": "comment",
        "name": "comment",
        "data_source": {
          "type": "data_view",
          "id": "1995407615886381058",
          "name": "评论表"
        },
        "data_properties": [
          {
            "name": "c_browserused",
            "display_name": "c_browserused",
            "type": "text",
            "comment": "",
            "mapped_field": {
              "name": "c_browserused",
              "type": "text",
              "display_name": "c_browserused"
            },
            "index_config": {
              "keyword_config": {
                "enabled": true,
                "ignore_above_len": 1024
              },
              "fulltext_config": {
                "enabled": true,
                "analyzer": "standard"
              },
              "vector_config": {
                "enabled": false,
                "model_id": ""
              }
            },
            "condition_operations": [
              "==",
              "!=",
              "in",
              "not_in",
              "match"
            ]
          },
          {
            "name": "c_commentid",
            "display_name": "c_commentid",
            "type": "integer",
            "comment": "",
            "mapped_field": {
              "name": "c_commentid",
              "type": "integer",
              "display_name": "c_commentid"
            }
          },
          {
            "name": "c_content",
            "display_name": "c_content",
            "type": "text",
            "comment": "",
            "mapped_field": {
              "name": "c_content",
              "type": "text",
              "display_name": "c_content"
            },
            "index_config": {
              "keyword_config": {
                "enabled": true,
                "ignore_above_len": 1024
              },
              "fulltext_config": {
                "enabled": true,
                "analyzer": "standard"
              },
              "vector_config": {
                "enabled": false,
                "model_id": ""
              }
            },
            "condition_operations": [
              "!=",
              "in",
              "not_in",
              "match",
              "=="
            ]
          },
          {
            "name": "c_creationdate",
            "display_name": "c_creationdate",
            "type": "datetime",
            "comment": "",
            "mapped_field": {
              "name": "c_creationdate",
              "type": "datetime",
              "display_name": "c_creationdate"
            }
          },
          {
            "name": "c_length",
            "display_name": "c_length",
            "type": "integer",
            "comment": "",
            "mapped_field": {
              "name": "c_length",
              "type": "integer",
              "display_name": "c_length"
            }
          },
          {
            "name": "c_locationip",
            "display_name": "c_locationip",
            "type": "text",
            "comment": "",
            "mapped_field": {
              "name": "c_locationip",
              "type": "text",
              "display_name": "c_locationip"
            },
            "index_config": {
              "keyword_config": {
                "enabled": true,
                "ignore_above_len": 1024
              },
              "fulltext_config": {
                "enabled": true,
                "analyzer": "standard"
              },
              "vector_config": {
                "enabled": false,
                "model_id": ""
              }
            },
            "condition_operations": [
              "match",
              "==",
              "!=",
              "in",
              "not_in"
            ]
          }
        ],
        "primary_keys": [
          "c_commentid"
        ],
        "display_key": "c_browserused"
      },
      {
        "id": "comment",
        "name": "comment",
        "data_source": {
          "type": "data_view",
          "id": "1995407615886381058",
          "name": ""
        },
        "data_properties": [
          {
            "name": "c_browserused",
            "display_name": "c_browserused",
            "type": "text",
            "comment": "",
            "mapped_field": {
              "name": "c_browserused"
            }
          },
          {
            "name": "c_commentid",
            "display_name": "c_commentid",
            "type": "integer",
            "comment": "",
            "mapped_field": {
              "name": "c_commentid"
            }
          },
          {
            "name": "c_content",
            "display_name": "c_content",
            "type": "text",
            "comment": "",
            "mapped_field": {
              "name": "c_content"
            }
          },
          {
            "name": "c_creationdate",
            "display_name": "c_creationdate",
            "type": "datetime",
            "comment": "",
            "mapped_field": {
              "name": "c_creationdate"
            }
          },
          {
            "name": "c_length",
            "display_name": "c_length",
            "type": "integer",
            "comment": "",
            "mapped_field": {
              "name": "c_length"
            }
          },
          {
            "name": "c_locationip",
            "display_name": "c_locationip",
            "type": "text",
            "comment": "",
            "mapped_field": {
              "name": "c_locationip"
            }
          }
        ],
        "primary_keys": [
          "c_commentid"
        ],
        "display_key": "c_browserused"
      }
    ],
    "relation_types": [
      {
        "relation_type_id": "comment_replyof_comment",
        "relation_type": {
          "id": "comment_replyof_comment",
          "name": "comment_replyof_comment",
          "source_object_type_id": "comment",
          "target_object_type_id": "comment",
          "type": "data_view",
          "mapping_rules": {
            "backing_data_source": {
              "type": "data_view",
              "id": "1995407615768940546"
            },
            "source_mapping_rules": [
              {
                "source_property": {
                  "name": "c_commentid"
                },
                "target_property": {
                  "name": "c_commentid1"
                }
              }
            ],
            "target_mapping_rules": [
              {
                "target_property": {
                  "name": "c_commentid"
                },
                "source_property": {
                  "name": "c_commentid2"
                }
              }
            ]
          }
        },
        "source_object_type_id": "comment",
        "target_object_type_id": "comment",
        "direction": "backward"
      }
    ],
    "length": 1
  },
  {
    "object_types": [
      {
        "id": "comment",
        "name": "comment",
        "data_source": {
          "type": "data_view",
          "id": "1995407615886381058",
          "name": "评论表"
        },
        "data_properties": [
          {
            "name": "c_browserused",
            "display_name": "c_browserused",
            "type": "text",
            "comment": "",
            "mapped_field": {
              "name": "c_browserused",
              "type": "text",
              "display_name": "c_browserused"
            },
            "index_config": {
              "keyword_config": {
                "enabled": true,
                "ignore_above_len": 1024
              },
              "fulltext_config": {
                "enabled": true,
                "analyzer": "standard"
              },
              "vector_config": {
                "enabled": false,
                "model_id": ""
              }
            },
            "condition_operations": [
              "==",
              "!=",
              "in",
              "not_in",
              "match"
            ]
          },
          {
            "name": "c_commentid",
            "display_name": "c_commentid",
            "type": "integer",
            "comment": "",
            "mapped_field": {
              "name": "c_commentid",
              "type": "integer",
              "display_name": "c_commentid"
            }
          },
          {
            "name": "c_content",
            "display_name": "c_content",
            "type": "text",
            "comment": "",
            "mapped_field": {
              "name": "c_content",
              "type": "text",
              "display_name": "c_content"
            },
            "index_config": {
              "keyword_config": {
                "enabled": true,
                "ignore_above_len": 1024
              },
              "fulltext_config": {
                "enabled": true,
                "analyzer": "standard"
              },
              "vector_config": {
                "enabled": false,
                "model_id": ""
              }
            },
            "condition_operations": [
              "!=",
              "in",
              "not_in",
              "match",
              "=="
            ]
          },
          {
            "name": "c_creationdate",
            "display_name": "c_creationdate",
            "type": "datetime",
            "comment": "",
            "mapped_field": {
              "name": "c_creationdate",
              "type": "datetime",
              "display_name": "c_creationdate"
            }
          },
          {
            "name": "c_length",
            "display_name": "c_length",
            "type": "integer",
            "comment": "",
            "mapped_field": {
              "name": "c_length",
              "type": "integer",
              "display_name": "c_length"
            }
          },
          {
            "name": "c_locationip",
            "display_name": "c_locationip",
            "type": "text",
            "comment": "",
            "mapped_field": {
              "name": "c_locationip",
              "type": "text",
              "display_name": "c_locationip"
            },
            "index_config": {
              "keyword_config": {
                "enabled": true,
                "ignore_above_len": 1024
              },
              "fulltext_config": {
                "enabled": true,
                "analyzer": "standard"
              },
              "vector_config": {
                "enabled": false,
                "model_id": ""
              }
            },
            "condition_operations": [
              "match",
              "==",
              "!=",
              "in",
              "not_in"
            ]
          }
        ],
        "primary_keys": [
          "c_commentid"
        ],
        "display_key": "c_browserused"
      },
      {
        "id": "person",
        "name": "person",
        "data_source": {
          "type": "data_view",
          "id": "1995407615877992449",
          "name": ""
        },
        "data_properties": [
          {
            "name": "p_birthday",
            "display_name": "p_birthday",
            "type": "date",
            "comment": "",
            "mapped_field": {
              "name": "p_birthday",
              "type": "date",
              "display_name": "p_birthday"
            }
          },
          {
            "name": "p_browserused",
            "display_name": "p_browserused",
            "type": "text",
            "comment": "",
            "mapped_field": {
              "name": "p_browserused",
              "type": "text",
              "display_name": "p_browserused"
            },
            "condition_operations": [
              "==",
              "!=",
              "match"
            ]
          },
          {
            "name": "p_creationdate",
            "display_name": "p_creationdate",
            "type": "datetime",
            "comment": "",
            "mapped_field": {
              "name": "p_creationdate",
              "type": "datetime",
              "display_name": "p_creationdate"
            }
          },
          {
            "name": "p_firstname",
            "display_name": "p_firstname",
            "type": "text",
            "comment": "",
            "mapped_field": {
              "name": "p_firstname",
              "type": "text",
              "display_name": "p_firstname"
            },
            "condition_operations": [
              "==",
              "!=",
              "match"
            ]
          },
          {
            "name": "p_gender",
            "display_name": "p_gender",
            "type": "text",
            "comment": "",
            "mapped_field": {
              "name": "p_gender",
              "type": "text",
              "display_name": "p_gender"
            },
            "condition_operations": [
              "==",
              "!=",
              "match"
            ]
          },
          {
            "name": "p_lastname",
            "display_name": "p_lastname",
            "type": "text",
            "comment": "",
            "mapped_field": {
              "name": "p_lastname",
              "type": "text",
              "display_name": "p_lastname"
            },
            "condition_operations": [
              "==",
              "!=",
              "match"
            ]
          },
          {
            "name": "p_locationip",
            "display_name": "p_locationip",
            "type": "text",
            "comment": "",
            "mapped_field": {
              "name": "p_locationip",
              "type": "text",
              "display_name": "p_locationip"
            },
            "condition_operations": [
              "==",
              "!=",
              "match"
            ]
          },
          {
            "name": "p_personid",
            "display_name": "p_personid",
            "type": "integer",
            "comment": "",
            "mapped_field": {
              "name": "p_personid",
              "type": "integer",
              "display_name": "p_personid"
            }
          }
        ],
        "primary_keys": [
          "p_personid"
        ],
        "display_key": "p_birthday"
      }
    ],
    "relation_types": [
      {
        "relation_type_id": "person_likes_comment",
        "relation_type": {
          "id": "person_likes_comment",
          "name": "person_likes_comment",
          "source_object_type_id": "person",
          "target_object_type_id": "comment",
          "type": "data_view",
          "mapping_rules": {
            "source_mapping_rules": [
              {
                "target_property": {
                  "name": "p_personid"
                },
                "source_property": {
                  "name": "p_personid"
                }
              }
            ],
            "target_mapping_rules": [
              {
                "source_property": {
                  "name": "c_commentid"
                },
                "target_property": {
                  "name": "c_commentid"
                }
              }
            ],
            "backing_data_source": {
              "type": "data_view",
              "id": "1995407615873798146"
            }
          }
        },
        "source_object_type_id": "comment",
        "target_object_type_id": "person",
        "direction": "backward"
      }
    ],
    "length": 1
  }
]
```

<h3 id="从起点探索概念路径-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[RelationTypePaths](#schemarelationtypepaths)|

<aside class="success">
This operation does not require authentication
</aside>

## 校验关系类

`POST /api/bkn-backend/v1/knowledge-networks/{kn_id}/relation-types/validation`

仅校验关系类依赖存在性，不写库。校验起点/终点对象类、间接数据视图等依赖是否存在。

**响应**：HTTP 200 时 `valid`/`detail` 同其它 validate 接口；参数与鉴权错误为非 2xx。

**内部接口**：`POST /api/bkn-backend/in/v1/.../relation-types/validation` 与 `POST /api/ontology-manager/in/v1/.../relation-types/validation`；Header 解析访问者，无 OAuth。

> Body parameter

```json
{
  "entries": [
    {}
  ]
}
```

<h3 id="校验关系类-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|branch|query|string|false|分支，不填则使用 main 分支|
|strict_mode|query|boolean|false|是否严格校验依赖，默认为 true|
|import_mode|query|string|false|与创建关系类接口一致；用于关系类 ID 已存在等冲突的校验语义（名称维度与创建侧一致时由创建逻辑约束）。|
|body|body|object|true|none|
|» entries|body|[object]|false|待校验的关系类列表，结构与创建接口一致|

#### Enumerated Values

|Parameter|Value|
|---|---|
|import_mode|normal|
|import_mode|ignore|
|import_mode|overwrite|

> Example responses

> 200 Response

```json
{
  "valid": true,
  "detail": "string"
}
```

<h3 id="校验关系类-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|已返回校验结果（通过与否均可能为 200）|Inline|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数错误等；业务校验未通过见 200 + valid:false|None|

<h3 id="校验关系类-responseschema">Response Schema</h3>

Status Code **200**

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» valid|boolean|true|none|none|
|» detail|string|false|none|当 valid 为 false 时的说明（error.Error()）|

<aside class="success">
This operation does not require authentication
</aside>

# Schemas

<h2 id="tocS_BasicInfo">BasicInfo</h2>
<!-- backwards compatibility -->
<a id="schemabasicinfo"></a>
<a id="schema_BasicInfo"></a>
<a id="tocSbasicinfo"></a>
<a id="tocsbasicinfo"></a>

```json
{
  "id": "string",
  "name": "string"
}

```

资源的基本信息，包含id和名称

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|资源ID|
|name|string|true|none|资源名称|

<h2 id="tocS_ConceptCondition">ConceptCondition</h2>
<!-- backwards compatibility -->
<a id="schemaconceptcondition"></a>
<a id="schema_ConceptCondition"></a>
<a id="tocSconceptcondition"></a>
<a id="tocsconceptcondition"></a>

```json
{
  "field": "type_id",
  "operation": "and",
  "sub_conditions": [
    {
      "field": "type_id",
      "operation": "and",
      "sub_conditions": [],
      "value": null,
      "value_from": "const"
    }
  ],
  "value": null,
  "value_from": "const"
}

```

概念查询数据条件。可用于过滤的字段有类名、属性名和描述

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|false|none|字段名称|
|operation|string|true|none|操作符。<br><br>knn: 未对原文进行向量化的向量过滤，接收的值是数组，第一个值，过滤内容，第二个值为 int,是邻居搜索时返回的邻居个数。<br><br>knn_vector: 对原文进行向量化后的向量过滤，接收的值是数组，第一个值，向量，第二个值为 int,是邻居搜索时返回的邻居个数。|
|sub_conditions|[[ConceptCondition](#schemaconceptcondition)]|false|none|子过滤条件|
|value|any|false|none|字段值|
|value_from|string|false|none|字段值来源，当前仅支持 "const"|

#### Enumerated Values

|Property|Value|
|---|---|
|field|type_id|
|field|type_name|
|field|property_name|
|field|property_display_name|
|field|comment|
|operation|and|
|operation|or|
|operation|==|
|operation|!=|
|operation|in|
|operation|not_in|
|operation|like|
|operation|not_like|
|operation|regex|
|operation|match|
|operation|match_phrase|
|operation|knn|
|operation|knn_vector|
|value_from|const|

<h2 id="tocS_ConceptGroup">ConceptGroup</h2>
<!-- backwards compatibility -->
<a id="schemaconceptgroup"></a>
<a id="schema_ConceptGroup"></a>
<a id="tocSconceptgroup"></a>
<a id="tocsconceptgroup"></a>

```json
{
  "id": "string",
  "name": "string"
}

```

概念分组

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|概念分组ID|
|name|string|true|none|概念分组名称|

<h2 id="tocS_DataSource">DataSource</h2>
<!-- backwards compatibility -->
<a id="schemadatasource"></a>
<a id="schema_DataSource"></a>
<a id="tocSdatasource"></a>
<a id="tocsdatasource"></a>

```json
{
  "type": "data_view",
  "id": "string",
  "name": "string"
}

```

数据来源

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|type|string|true|none|数据来源类型为数据视图|
|id|string|true|none|数据视图ID|
|name|string|false|none|数据视图名称。查看详情时返回。|

#### Enumerated Values

|Property|Value|
|---|---|
|type|data_view|

<h2 id="tocS_DataViewMappingRule">DataViewMappingRule</h2>
<!-- backwards compatibility -->
<a id="schemadataviewmappingrule"></a>
<a id="schema_DataViewMappingRule"></a>
<a id="tocSdataviewmappingrule"></a>
<a id="tocsdataviewmappingrule"></a>

```json
{
  "backing_data_source": {
    "type": "data_view",
    "id": "string",
    "name": "string"
  },
  "source_mapping_rules": [
    {
      "target_property": {
        "name": "string",
        "display_name": "string"
      },
      "source_property": {
        "name": "string",
        "display_name": "string"
      }
    }
  ],
  "target_mapping_rules": [
    {
      "target_property": {
        "name": "string",
        "display_name": "string"
      },
      "source_property": {
        "name": "string",
        "display_name": "string"
      }
    }
  ]
}

```

关系类型为 data_view 时的匹配规则

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|backing_data_source|[DataSource](#schemadatasource)|true|none|数据来源视图|
|source_mapping_rules|[[Mapping](#schemamapping)]|true|none|起点对象类与数据集的匹配规则|
|target_mapping_rules|[[Mapping](#schemamapping)]|true|none|终点对象类与数据集匹配规则|

<h2 id="tocS_FilteredCrossJoinMappingRule">FilteredCrossJoinMappingRule</h2>
<!-- backwards compatibility -->
<a id="schemafilteredcrossjoinmappingrule"></a>
<a id="schema_FilteredCrossJoinMappingRule"></a>
<a id="tocSfilteredcrossjoinmappingrule"></a>
<a id="tocsfilteredcrossjoinmappingrule"></a>

```json
{
  "source_condition": {
    "operation": "and",
    "sub_conditions": [
      {
        "operation": "and",
        "sub_conditions": []
      }
    ]
  },
  "target_condition": {
    "operation": "and",
    "sub_conditions": [
      {
        "operation": "and",
        "sub_conditions": []
      }
    ]
  }
}

```

关系类型为 `filtered_cross_join`（分侧过滤全连接，FCJ）时的匹配规则。
不绑定数据视图，无数组形式的键映射；`source_condition` 与 `target_condition` 分别约束起点侧、终点侧对象实例是否参与该关系
（结构与对象实例查询的 Condition 相同）。
两侧条件均可省略：可只填一侧、或两侧皆不传/`{}`（表示该侧或两侧无额外过滤，与引擎中「无条件即全量」语义一致）。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|source_condition|[Condition](#schemacondition)|false|none|起点对象类实例需满足的条件；可省略或为空表示该侧无额外约束|
|target_condition|[Condition](#schemacondition)|false|none|终点对象类实例需满足的条件；可省略或为空表示该侧无额外约束|

<h2 id="tocS_ID">ID</h2>
<!-- backwards compatibility -->
<a id="schemaid"></a>
<a id="schema_ID"></a>
<a id="tocSid"></a>
<a id="tocsid"></a>

```json
{
  "id": "string"
}

```

id

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|id|

<h2 id="tocS_ListRelationTypes">ListRelationTypes</h2>
<!-- backwards compatibility -->
<a id="schemalistrelationtypes"></a>
<a id="schema_ListRelationTypes"></a>
<a id="tocSlistrelationtypes"></a>
<a id="tocslistrelationtypes"></a>

```json
{
  "entries": [
    {
      "concept_type": "relation_type",
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "source_object_type_id": "string",
      "target_object_type_id": "string",
      "type": "direct",
      "mapping_rules": [
        {
          "target_property": {
            "name": "string",
            "display_name": "string"
          },
          "source_property": {
            "name": "string",
            "display_name": "string"
          }
        }
      ],
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string"
    }
  ],
  "total_count": 0
}

```

关系类列表

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[RelationType](#schemarelationtype)]|true|none|条目列表|
|total_count|integer|true|none|总条数|

<h2 id="tocS_Mapping">Mapping</h2>
<!-- backwards compatibility -->
<a id="schemamapping"></a>
<a id="schema_Mapping"></a>
<a id="tocSmapping"></a>
<a id="tocsmapping"></a>

```json
{
  "target_property": {
    "name": "string",
    "display_name": "string"
  },
  "source_property": {
    "name": "string",
    "display_name": "string"
  }
}

```

关联规则

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|target_property|[SimpleProperty](#schemasimpleproperty)|true|none|起点属性|
|source_property|[SimpleProperty](#schemasimpleproperty)|true|none|终点属性|

<h2 id="tocS_SimpleProperty">SimpleProperty</h2>
<!-- backwards compatibility -->
<a id="schemasimpleproperty"></a>
<a id="schema_SimpleProperty"></a>
<a id="tocSsimpleproperty"></a>
<a id="tocssimpleproperty"></a>

```json
{
  "name": "string",
  "display_name": "string"
}

```

属性简单信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|属性名称|
|display_name|string|false|none|属性显示名。当查看详情时会返回此字段|

<h2 id="tocS_SimpleObjectType">SimpleObjectType</h2>
<!-- backwards compatibility -->
<a id="schemasimpleobjecttype"></a>
<a id="schema_SimpleObjectType"></a>
<a id="tocSsimpleobjecttype"></a>
<a id="tocssimpleobjecttype"></a>

```json
{
  "id": "string",
  "name": "string",
  "icon": "string",
  "color": "string"
}

```

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|对象类id|
|name|string|true|none|对象类名称|
|icon|string|true|none|对象类图标|
|color|string|true|none|对象类颜色|

<h2 id="tocS_Path">Path</h2>
<!-- backwards compatibility -->
<a id="schemapath"></a>
<a id="schema_Path"></a>
<a id="tocSpath"></a>
<a id="tocspath"></a>

```json
{
  "nodes": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "concept_groups": [
        {
          "id": "string",
          "name": "string"
        }
      ],
      "data_source": {
        "type": "data_view",
        "id": "string",
        "name": "string"
      },
      "data_properties": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string",
          "mapped_field": {
            "name": "string",
            "display_name": "string",
            "type": "string"
          },
          "index": true,
          "fulltext_config": {
            "analyzer": "standard",
            "field_keyword": true
          },
          "vector_config": {
            "dimension": 0
          }
        }
      ],
      "logic_properties": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string",
          "index": true,
          "data_source": {
            "type": "metric",
            "id": "string",
            "name": "string"
          },
          "parameters": [
            {
              "name": "string",
              "value_from": "property",
              "value": "string"
            }
          ]
        }
      ],
      "primary_keys": [
        "string"
      ],
      "display_key": "string",
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "module_type": "object_type"
    }
  ],
  "edges": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "source_object_type_id": "string",
      "source_object_type": {
        "id": "string",
        "name": "string",
        "icon": "string",
        "color": "string"
      },
      "target_object_type_id": "string",
      "target_object_type": {
        "id": "string",
        "name": "string",
        "icon": "string",
        "color": "string"
      },
      "type": "direct",
      "mapping_rules": [
        {
          "target_property": {
            "name": "string",
            "display_name": "string"
          },
          "source_property": {
            "name": "string",
            "display_name": "string"
          }
        }
      ],
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string"
    }
  ],
  "length": 0
}

```

路径

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|nodes|[[ObjectTypeDetail](#schemaobjecttypedetail)]|true|none|路径中的节点列表(有序)|
|edges|[[RelationTypeDetail](#schemarelationtypedetail)]|true|none|路径中的边列表(有序)|
|length|integer|true|none|路径长度(边数量)|

<h2 id="tocS_ObjectTypeDetail">ObjectTypeDetail</h2>
<!-- backwards compatibility -->
<a id="schemaobjecttypedetail"></a>
<a id="schema_ObjectTypeDetail"></a>
<a id="tocSobjecttypedetail"></a>
<a id="tocsobjecttypedetail"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "kn_id": "string",
  "concept_groups": [
    {
      "id": "string",
      "name": "string"
    }
  ],
  "data_source": {
    "type": "data_view",
    "id": "string",
    "name": "string"
  },
  "data_properties": [
    {
      "name": "string",
      "display_name": "string",
      "type": "string",
      "comment": "string",
      "mapped_field": {
        "name": "string",
        "display_name": "string",
        "type": "string"
      },
      "index": true,
      "fulltext_config": {
        "analyzer": "standard",
        "field_keyword": true
      },
      "vector_config": {
        "dimension": 0
      }
    }
  ],
  "logic_properties": [
    {
      "name": "string",
      "display_name": "string",
      "type": "string",
      "comment": "string",
      "index": true,
      "data_source": {
        "type": "metric",
        "id": "string",
        "name": "string"
      },
      "parameters": [
        {
          "name": "string",
          "value_from": "property",
          "value": "string"
        }
      ]
    }
  ],
  "primary_keys": [
    "string"
  ],
  "display_key": "string",
  "creator": "string",
  "create_time": 0,
  "updater": "string",
  "update_time": 0,
  "module_type": "object_type"
}

```

节点（对象类）信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|对象类ID|
|name|string|true|none|对象类名称|
|tags|[string]|true|none|标签。 （可以为空）|
|comment|string|true|none|备注（可以为空）|
|icon|string|true|none|图标|
|color|string|true|none|颜色|
|branch|string|true|none|分支ID|
|kn_id|string|true|none|业务知识网络id|
|concept_groups|[[ConceptGroup](#schemaconceptgroup)]|true|none|概念分组id|
|data_source|[DataSource](#schemadatasource)|true|none|数据来源|
|data_properties|[[DataProperty](#schemadataproperty)]|true|none|数据属性|
|logic_properties|[[LogicProperty](#schemalogicproperty)]|true|none|逻辑属性|
|primary_keys|[string]|true|none|主键|
|display_key|string|true|none|对象实例的显示属性|
|creator|string|true|none|创建人ID|
|create_time|integer(int64)|true|none|创建时间|
|updater|string|true|none|最近一次修改人|
|update_time|integer(int64)|true|none|最近一次更新时间|
|module_type|string|true|none|模块类型|

#### Enumerated Values

|Property|Value|
|---|---|
|module_type|object_type|

<h2 id="tocS_DataProperty">DataProperty</h2>
<!-- backwards compatibility -->
<a id="schemadataproperty"></a>
<a id="schema_DataProperty"></a>
<a id="tocSdataproperty"></a>
<a id="tocsdataproperty"></a>

```json
{
  "name": "string",
  "display_name": "string",
  "type": "string",
  "comment": "string",
  "mapped_field": {
    "name": "string",
    "display_name": "string",
    "type": "string"
  },
  "index": true,
  "fulltext_config": {
    "analyzer": "standard",
    "field_keyword": true
  },
  "vector_config": {
    "dimension": 0
  }
}

```

数据属性

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|属性名称。只能包含小写英文字母、数字、下划线（_）、连字符（-），且不能以下划线和连字符开头|
|display_name|string|true|none|属性显示名|
|type|string|true|none|属性数据类型。除了视图的字段类型之外，还有 metric、objective、event、trace、log、operator|
|comment|string|true|none|属性描述|
|mapped_field|[ViewField](#schemaviewfield)|true|none|属性映射到数据来源中的字段名|
|index|boolean|true|none|是否开启索引，默认是true|
|fulltext_config|[FulltextConfig](#schemafulltextconfig)|true|none|全文索引的配置|
|vector_config|[VectorConfig](#schemavectorconfig)|true|none|向量索引的配置|

<h2 id="tocS_FulltextConfig">FulltextConfig</h2>
<!-- backwards compatibility -->
<a id="schemafulltextconfig"></a>
<a id="schema_FulltextConfig"></a>
<a id="tocSfulltextconfig"></a>
<a id="tocsfulltextconfig"></a>

```json
{
  "analyzer": "standard",
  "field_keyword": true
}

```

全文索引的配置

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|analyzer|string|true|none|分词器|
|field_keyword|boolean|true|none|是否保留原始字符串，保留原始字符串可用于精确匹配。默认是false|

#### Enumerated Values

|Property|Value|
|---|---|
|analyzer|standard|
|analyzer|ik_max_word|

<h2 id="tocS_ViewField">ViewField</h2>
<!-- backwards compatibility -->
<a id="schemaviewfield"></a>
<a id="schema_ViewField"></a>
<a id="tocSviewfield"></a>
<a id="tocsviewfield"></a>

```json
{
  "name": "string",
  "display_name": "string",
  "type": "string"
}

```

视图字段信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|字段名称|
|display_name|string|false|none|字段显示名.查看时有此字段|
|type|string|false|none|视图字段类型，查看时有此字段|

<h2 id="tocS_VectorConfig">VectorConfig</h2>
<!-- backwards compatibility -->
<a id="schemavectorconfig"></a>
<a id="schema_VectorConfig"></a>
<a id="tocSvectorconfig"></a>
<a id="tocsvectorconfig"></a>

```json
{
  "dimension": 0
}

```

向量索引的配置

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|dimension|integer|true|none|向量维度|

<h2 id="tocS_LogicProperty">LogicProperty</h2>
<!-- backwards compatibility -->
<a id="schemalogicproperty"></a>
<a id="schema_LogicProperty"></a>
<a id="tocSlogicproperty"></a>
<a id="tocslogicproperty"></a>

```json
{
  "name": "string",
  "display_name": "string",
  "type": "string",
  "comment": "string",
  "index": true,
  "data_source": {
    "type": "metric",
    "id": "string",
    "name": "string"
  },
  "parameters": [
    {
      "name": "string",
      "value_from": "property",
      "value": "string"
    }
  ]
}

```

逻辑属性

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|属性名称。只能包含小写英文字母、数字、下划线（_）、连字符（-），且不能以下划线和连字符开头|
|display_name|string|false|none|属性显示名|
|type|string|false|none|属性数据类型。除了视图的字段类型之外，还有 metric、objective、event、trace、log、operator|
|comment|string|false|none|属性描述|
|index|boolean|false|none|是否开启索引，默认是true|
|data_source|[LogicSource](#schemalogicsource)|true|none|逻辑来源|
|parameters|[[Parameter](#schemaparameter)]|true|none|逻辑所需的参数|

<h2 id="tocS_LogicSource">LogicSource</h2>
<!-- backwards compatibility -->
<a id="schemalogicsource"></a>
<a id="schema_LogicSource"></a>
<a id="tocSlogicsource"></a>
<a id="tocslogicsource"></a>

```json
{
  "type": "metric",
  "id": "string",
  "name": "string"
}

```

数据来源

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|type|string|true|none|数据来源类型|
|id|string|true|none|数据来源ID|
|name|string|false|none|名称。查看详情时返回。|

#### Enumerated Values

|Property|Value|
|---|---|
|type|metric|
|type|operator|

<h2 id="tocS_Parameter">Parameter</h2>
<!-- backwards compatibility -->
<a id="schemaparameter"></a>
<a id="schema_Parameter"></a>
<a id="tocSparameter"></a>
<a id="tocsparameter"></a>

```json
{
  "name": "string",
  "value_from": "property",
  "value": "string"
}

```

逻辑参数

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|参数名称|
|value_from|string|true|none|值来源|
|value|string|false|none|参数值。value_from=property时，填入的是对象类的数据属性名称；value_from=input时，不设置此字段|

#### Enumerated Values

|Property|Value|
|---|---|
|value_from|property|
|value_from|input|

<h2 id="tocS_override">override</h2>
<!-- backwards compatibility -->
<a id="schemaoverride"></a>
<a id="schema_override"></a>
<a id="tocSoverride"></a>
<a id="tocsoverride"></a>

```json
{}

```

post 重载批量创建、关系类检索接口

### Properties

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|object|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[ReqRelationTypes](#schemareqrelationtypes)|false|none|批量创建请求体|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[override--get](#schemaoverride--get)|false|none|关系类检索请求体|

<h2 id="tocS_override--get">override--get</h2>
<!-- backwards compatibility -->
<a id="schemaoverride--get"></a>
<a id="schema_override--get"></a>
<a id="tocSoverride--get"></a>
<a id="tocsoverride--get"></a>

```json
{
  "concept_groups": [
    "string"
  ],
  "condition": {
    "operation": "and",
    "sub_conditions": [
      {
        "operation": "and",
        "sub_conditions": []
      }
    ]
  },
  "sort": [
    {
      "field": "string",
      "direction": "desc"
    }
  ],
  "limit": 0,
  "need_total": true
}

```

关系类检索请求体

### Properties

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[FirstQueryWithSearchAfter](#schemafirstquerywithsearchafter)|false|none|关系类检索第一次请求|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[PageTurnQueryWithSearchAfter](#schemapageturnquerywithsearchafter)|false|none|分页查询的后续分页查询请求|

<h2 id="tocS_Sort">Sort</h2>
<!-- backwards compatibility -->
<a id="schemasort"></a>
<a id="schema_Sort"></a>
<a id="tocSsort"></a>
<a id="tocssort"></a>

```json
{
  "field": "string",
  "direction": "desc"
}

```

排序字段。默认按 _score 倒序排序

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|排序字段|
|direction|string|true|none|排序方向|

#### Enumerated Values

|Property|Value|
|---|---|
|direction|desc|
|direction|asc|

<h2 id="tocS_DirectMappingRules">DirectMappingRules</h2>
<!-- backwards compatibility -->
<a id="schemadirectmappingrules"></a>
<a id="schema_DirectMappingRules"></a>
<a id="tocSdirectmappingrules"></a>
<a id="tocsdirectmappingrules"></a>

```json
[
  {
    "target_property": {
      "name": "string",
      "display_name": "string"
    },
    "source_property": {
      "name": "string",
      "display_name": "string"
    }
  }
]

```

直接关联

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[[Mapping](#schemamapping)]|false|none|直接关联|

<h2 id="tocS_ReqRelationType">ReqRelationType</h2>
<!-- backwards compatibility -->
<a id="schemareqrelationtype"></a>
<a id="schema_ReqRelationType"></a>
<a id="tocSreqrelationtype"></a>
<a id="tocsreqrelationtype"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "source_object_type_id": "string",
  "target_object_type_id": "string",
  "type": "direct",
  "mapping_rules": [
    {
      "target_property": {
        "name": "string",
        "display_name": "string"
      },
      "source_property": {
        "name": "string",
        "display_name": "string"
      }
    }
  ]
}

```

关系类创建信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|false|none|ID.新建后不可修改，只能包含小写英文字母、数字、下划线（_）、连字符（-），且不能以下划线和连字符开头|
|name|string|true|none|名称|
|tags|[string]|false|none|标签。用于业务标识|
|comment|string|false|none|备注|
|icon|string|false|none|图标|
|color|string|false|none|颜色|
|branch|string|true|none|分支ID|
|source_object_type_id|string|false|none|起点象类ID|
|target_object_type_id|string|false|none|终点对象类ID|
|type|string|false|none|关系类型|
|mapping_rules|any|false|none|映射规则。`direct` 时为 Mapping 数组；`data_view` 时为 DataViewMappingRule；`filtered_cross_join`（FCJ）时为 FilteredCrossJoinMappingRule（分侧条件，无键映射）。|

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[DirectMappingRules](#schemadirectmappingrules)|false|none|直接关联|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[DataViewMappingRule](#schemadataviewmappingrule)|false|none|关系类型为 data_view 时的匹配规则|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[FilteredCrossJoinMappingRule](#schemafilteredcrossjoinmappingrule)|false|none|关系类型为 `filtered_cross_join`（分侧过滤全连接，FCJ）时的匹配规则。<br>不绑定数据视图，无数组形式的键映射；`source_condition` 与 `target_condition` 分别约束起点侧、终点侧对象实例是否参与该关系<br>（结构与对象实例查询的 Condition 相同）。<br>两侧条件均可省略：可只填一侧、或两侧皆不传/`{}`（表示该侧或两侧无额外过滤，与引擎中「无条件即全量」语义一致）。|

#### Enumerated Values

|Property|Value|
|---|---|
|type|direct|
|type|data_view|
|type|filtered_cross_join|

<h2 id="tocS_UpdateRelationType">UpdateRelationType</h2>
<!-- backwards compatibility -->
<a id="schemaupdaterelationtype"></a>
<a id="schema_UpdateRelationType"></a>
<a id="tocSupdaterelationtype"></a>
<a id="tocsupdaterelationtype"></a>

```json
{
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "source_object_type_id": "string",
  "target_object_type_id": "string",
  "type": "direct",
  "mapping_rules": [
    {
      "target_property": {
        "name": "string",
        "display_name": "string"
      },
      "source_property": {
        "name": "string",
        "display_name": "string"
      }
    }
  ]
}

```

关系类更新信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|名称|
|tags|[string]|false|none|标签。用于业务标识|
|comment|string|false|none|备注|
|icon|string|false|none|图标|
|color|string|false|none|颜色|
|branch|string|true|none|分支ID|
|source_object_type_id|string|false|none|起点象类ID|
|target_object_type_id|string|false|none|终点对象类ID|
|type|string|false|none|关系类型|
|mapping_rules|any|false|none|关联的匹配规则。`direct` 时为 Mapping 数组；`data_view` 时为 DataViewMappingRule；`filtered_cross_join` 时为 FilteredCrossJoinMappingRule。|

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[DirectMappingRules](#schemadirectmappingrules)|false|none|直接关联|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[DataViewMappingRule](#schemadataviewmappingrule)|false|none|关系类型为 data_view 时的匹配规则|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[FilteredCrossJoinMappingRule](#schemafilteredcrossjoinmappingrule)|false|none|关系类型为 `filtered_cross_join`（分侧过滤全连接，FCJ）时的匹配规则。<br>不绑定数据视图，无数组形式的键映射；`source_condition` 与 `target_condition` 分别约束起点侧、终点侧对象实例是否参与该关系<br>（结构与对象实例查询的 Condition 相同）。<br>两侧条件均可省略：可只填一侧、或两侧皆不传/`{}`（表示该侧或两侧无额外过滤，与引擎中「无条件即全量」语义一致）。|

#### Enumerated Values

|Property|Value|
|---|---|
|type|direct|
|type|data_view|
|type|filtered_cross_join|

<h2 id="tocS_RelationTypeDetail">RelationTypeDetail</h2>
<!-- backwards compatibility -->
<a id="schemarelationtypedetail"></a>
<a id="schema_RelationTypeDetail"></a>
<a id="tocSrelationtypedetail"></a>
<a id="tocsrelationtypedetail"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "kn_id": "string",
  "source_object_type_id": "string",
  "source_object_type": {
    "id": "string",
    "name": "string",
    "icon": "string",
    "color": "string"
  },
  "target_object_type_id": "string",
  "target_object_type": {
    "id": "string",
    "name": "string",
    "icon": "string",
    "color": "string"
  },
  "type": "direct",
  "mapping_rules": [
    {
      "target_property": {
        "name": "string",
        "display_name": "string"
      },
      "source_property": {
        "name": "string",
        "display_name": "string"
      }
    }
  ],
  "creator": "string",
  "create_time": 0,
  "updater": "string",
  "update_time": 0,
  "detail": "string"
}

```

关系类

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|关系类ID|
|name|string|true|none|关系类名称|
|tags|[string]|true|none|标签。 （可以为空）|
|comment|string|true|none|备注（可以为空）|
|icon|string|true|none|图标|
|color|string|true|none|颜色|
|branch|string|true|none|分支ID|
|kn_id|string|true|none|业务知识网络ID|
|source_object_type_id|string|true|none|起点象类ID|
|source_object_type|[SimpleObjectType](#schemasimpleobjecttype)|true|none|起点象类名称，当查看详情时，此字段才会返回。|
|target_object_type_id|string|true|none|终点对象类ID|
|target_object_type|[SimpleObjectType](#schemasimpleobjecttype)|true|none|终点对象类名称，当查看详情时，此字段才会返回|
|type|string|true|none|关系类型|
|mapping_rules|any|true|none|关联的匹配规则。`direct` 时为 Mapping 数组；`data_view` 时为 DataViewMappingRule；`filtered_cross_join` 时为 FilteredCrossJoinMappingRule。|

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[DirectMappingRules](#schemadirectmappingrules)|false|none|直接关联|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[DataViewMappingRule](#schemadataviewmappingrule)|false|none|关系类型为 data_view 时的匹配规则|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[FilteredCrossJoinMappingRule](#schemafilteredcrossjoinmappingrule)|false|none|关系类型为 `filtered_cross_join`（分侧过滤全连接，FCJ）时的匹配规则。<br>不绑定数据视图，无数组形式的键映射；`source_condition` 与 `target_condition` 分别约束起点侧、终点侧对象实例是否参与该关系<br>（结构与对象实例查询的 Condition 相同）。<br>两侧条件均可省略：可只填一侧、或两侧皆不传/`{}`（表示该侧或两侧无额外过滤，与引擎中「无条件即全量」语义一致）。|

continued

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|creator|string|true|none|创建人ID|
|create_time|integer(int64)|true|none|创建时间|
|updater|string|true|none|最近一次修改人|
|update_time|integer(int64)|true|none|最近一次更新时间|
|detail|string|false|none|说明书。按需返回，若指定了include_detail=true，则返回，否则不返回。列表查询时不返回此字段。|

#### Enumerated Values

|Property|Value|
|---|---|
|type|direct|
|type|data_view|
|type|filtered_cross_join|

<h2 id="tocS_RelationType">RelationType</h2>
<!-- backwards compatibility -->
<a id="schemarelationtype"></a>
<a id="schema_RelationType"></a>
<a id="tocSrelationtype"></a>
<a id="tocsrelationtype"></a>

```json
{
  "concept_type": "relation_type",
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "branch": "string",
  "kn_id": "string",
  "source_object_type_id": "string",
  "target_object_type_id": "string",
  "type": "direct",
  "mapping_rules": [
    {
      "target_property": {
        "name": "string",
        "display_name": "string"
      },
      "source_property": {
        "name": "string",
        "display_name": "string"
      }
    }
  ],
  "creator": "string",
  "create_time": 0,
  "updater": "string",
  "update_time": 0,
  "detail": "string"
}

```

关系类

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|concept_type|string|true|none|概念类型|
|id|string|true|none|关系类ID|
|name|string|true|none|关系类名称|
|tags|[string]|true|none|标签。 （可以为空）|
|comment|string|true|none|备注（可以为空）|
|icon|string|true|none|图标|
|color|string|true|none|颜色|
|branch|string|true|none|分支ID|
|kn_id|string|true|none|业务知识网络ID|
|source_object_type_id|string|true|none|起点象类ID|
|target_object_type_id|string|true|none|终点对象类ID|
|type|string|true|none|关系类型|
|mapping_rules|any|true|none|关联的匹配规则。`direct` 时为 Mapping 数组；`data_view` 时为 DataViewMappingRule；`filtered_cross_join` 时为 FilteredCrossJoinMappingRule。|

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[DirectMappingRules](#schemadirectmappingrules)|false|none|直接关联|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[DataViewMappingRule](#schemadataviewmappingrule)|false|none|关系类型为 data_view 时的匹配规则|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[FilteredCrossJoinMappingRule](#schemafilteredcrossjoinmappingrule)|false|none|关系类型为 `filtered_cross_join`（分侧过滤全连接，FCJ）时的匹配规则。<br>不绑定数据视图，无数组形式的键映射；`source_condition` 与 `target_condition` 分别约束起点侧、终点侧对象实例是否参与该关系<br>（结构与对象实例查询的 Condition 相同）。<br>两侧条件均可省略：可只填一侧、或两侧皆不传/`{}`（表示该侧或两侧无额外过滤，与引擎中「无条件即全量」语义一致）。|

continued

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|creator|string|true|none|创建人ID|
|create_time|integer(int64)|true|none|创建时间|
|updater|string|true|none|最近一次修改人|
|update_time|integer(int64)|true|none|最近一次更新时间|
|detail|string|false|none|说明书。按需返回，若指定了include_detail=true，则返回，否则不返回。列表查询时不返回此字段。|

#### Enumerated Values

|Property|Value|
|---|---|
|concept_type|relation_type|
|type|direct|
|type|data_view|
|type|filtered_cross_join|

<h2 id="tocS_RelationTypeSearchResponse">RelationTypeSearchResponse</h2>
<!-- backwards compatibility -->
<a id="schemarelationtypesearchresponse"></a>
<a id="schema_RelationTypeSearchResponse"></a>
<a id="tocSrelationtypesearchresponse"></a>
<a id="tocsrelationtypesearchresponse"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "source_object_type_id": "string",
      "source_object_type": {
        "id": "string",
        "name": "string",
        "icon": "string",
        "color": "string"
      },
      "target_object_type_id": "string",
      "target_object_type": {
        "id": "string",
        "name": "string",
        "icon": "string",
        "color": "string"
      },
      "type": "direct",
      "mapping_rules": [
        {
          "target_property": {
            "name": "string",
            "display_name": "string"
          },
          "source_property": {
            "name": "string",
            "display_name": "string"
          }
        }
      ],
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string"
    }
  ],
  "total_count": 0,
  "search_after": [
    null
  ]
}

```

关系类检索返回结果

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[RelationTypeDetail](#schemarelationtypedetail)]|true|none|对象实例数据|
|total_count|integer|false|none|总条数|
|search_after|[any]|true|none|表示返回的最后一个文档的排序值，获取这个用于下一次 search_after 分页。|

<h2 id="tocS_RelationTypePath">RelationTypePath</h2>
<!-- backwards compatibility -->
<a id="schemarelationtypepath"></a>
<a id="schema_RelationTypePath"></a>
<a id="tocSrelationtypepath"></a>
<a id="tocsrelationtypepath"></a>

```json
{
  "object_types": [
    {
      "id": "string",
      "name": "string",
      "data_source": {
        "type": "data_view",
        "id": "string",
        "name": "string"
      },
      "data_properties": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string",
          "mapped_field": {
            "name": "string",
            "display_name": "string",
            "type": "string"
          },
          "index": true,
          "fulltext_config": {
            "analyzer": "standard",
            "field_keyword": true
          },
          "vector_config": {
            "dimension": 0
          }
        }
      ],
      "logic_properties": [
        {
          "name": "string",
          "display_name": "string",
          "type": "string",
          "comment": "string",
          "index": true,
          "data_source": {
            "type": "metric",
            "id": "string",
            "name": "string"
          },
          "parameters": [
            {
              "name": "string",
              "value_from": "property",
              "value": "string"
            }
          ]
        }
      ],
      "primary_keys": [
        "string"
      ],
      "display_key": "string"
    }
  ],
  "relation_types": [
    {
      "relation_type_id": "string",
      "relation_type": {
        "id": "string",
        "name": "string",
        "source_object_type_id": "string",
        "target_object_type_id": "string",
        "type": "direct",
        "mapping_rules": [
          {
            "target_property": {
              "name": "string",
              "display_name": "string"
            },
            "source_property": {
              "name": "string",
              "display_name": "string"
            }
          }
        ]
      },
      "source_object_type_id": "string",
      "target_object_type_id": "string",
      "direction": "forward"
    }
  ],
  "length": 0
}

```

概念路径

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_types|[[ObjectTypeWithKeyField](#schemaobjecttypewithkeyfield)]|true|none|概念路径中各节点的对象类数组|
|relation_types|[[TypeEdge](#schematypeedge)]|true|none|概念路径|
|length|integer|true|none|路径长度|

<h2 id="tocS_ObjectTypeWithKeyField">ObjectTypeWithKeyField</h2>
<!-- backwards compatibility -->
<a id="schemaobjecttypewithkeyfield"></a>
<a id="schema_ObjectTypeWithKeyField"></a>
<a id="tocSobjecttypewithkeyfield"></a>
<a id="tocsobjecttypewithkeyfield"></a>

```json
{
  "id": "string",
  "name": "string",
  "data_source": {
    "type": "data_view",
    "id": "string",
    "name": "string"
  },
  "data_properties": [
    {
      "name": "string",
      "display_name": "string",
      "type": "string",
      "comment": "string",
      "mapped_field": {
        "name": "string",
        "display_name": "string",
        "type": "string"
      },
      "index": true,
      "fulltext_config": {
        "analyzer": "standard",
        "field_keyword": true
      },
      "vector_config": {
        "dimension": 0
      }
    }
  ],
  "logic_properties": [
    {
      "name": "string",
      "display_name": "string",
      "type": "string",
      "comment": "string",
      "index": true,
      "data_source": {
        "type": "metric",
        "id": "string",
        "name": "string"
      },
      "parameters": [
        {
          "name": "string",
          "value_from": "property",
          "value": "string"
        }
      ]
    }
  ],
  "primary_keys": [
    "string"
  ],
  "display_key": "string"
}

```

对象类的关键信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|对象类id|
|name|string|true|none|对象类名称|
|data_source|[DataSource](#schemadatasource)|true|none|数据来源信息|
|data_properties|[[DataProperty](#schemadataproperty)]|true|none|数据属性|
|logic_properties|[[LogicProperty](#schemalogicproperty)]|true|none|逻辑属性|
|primary_keys|[string]|true|none|主键信息|
|display_key|string|true|none|对象类的显示属性名|

<h2 id="tocS_RelationTypeWithKeyField">RelationTypeWithKeyField</h2>
<!-- backwards compatibility -->
<a id="schemarelationtypewithkeyfield"></a>
<a id="schema_RelationTypeWithKeyField"></a>
<a id="tocSrelationtypewithkeyfield"></a>
<a id="tocsrelationtypewithkeyfield"></a>

```json
{
  "id": "string",
  "name": "string",
  "source_object_type_id": "string",
  "target_object_type_id": "string",
  "type": "direct",
  "mapping_rules": [
    {
      "target_property": {
        "name": "string",
        "display_name": "string"
      },
      "source_property": {
        "name": "string",
        "display_name": "string"
      }
    }
  ]
}

```

关系类关键信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|关系类id|
|name|string|true|none|关系类名称|
|source_object_type_id|string|true|none|起点对象类id|
|target_object_type_id|string|true|none|终点对象类id|
|type|string|true|none|关联类型。`direct` 直接键映射；`data_view` 经数据视图关联；`filtered_cross_join` 分侧过滤全连接（FCJ），无数组形式键映射|
|mapping_rules|any|false|none|none|

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[DirectMappingRules](#schemadirectmappingrules)|false|none|直接关联|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[DataViewMappingRule](#schemadataviewmappingrule)|false|none|关系类型为 data_view 时的匹配规则|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[FilteredCrossJoinMappingRule](#schemafilteredcrossjoinmappingrule)|false|none|关系类型为 `filtered_cross_join`（分侧过滤全连接，FCJ）时的匹配规则。<br>不绑定数据视图，无数组形式的键映射；`source_condition` 与 `target_condition` 分别约束起点侧、终点侧对象实例是否参与该关系<br>（结构与对象实例查询的 Condition 相同）。<br>两侧条件均可省略：可只填一侧、或两侧皆不传/`{}`（表示该侧或两侧无额外过滤，与引擎中「无条件即全量」语义一致）。|

#### Enumerated Values

|Property|Value|
|---|---|
|type|direct|
|type|data_view|
|type|filtered_cross_join|

<h2 id="tocS_TypeEdge">TypeEdge</h2>
<!-- backwards compatibility -->
<a id="schematypeedge"></a>
<a id="schema_TypeEdge"></a>
<a id="tocStypeedge"></a>
<a id="tocstypeedge"></a>

```json
{
  "relation_type_id": "string",
  "relation_type": {
    "id": "string",
    "name": "string",
    "source_object_type_id": "string",
    "target_object_type_id": "string",
    "type": "direct",
    "mapping_rules": [
      {
        "target_property": {
          "name": "string",
          "display_name": "string"
        },
        "source_property": {
          "name": "string",
          "display_name": "string"
        }
      }
    ]
  },
  "source_object_type_id": "string",
  "target_object_type_id": "string",
  "direction": "forward"
}

```

路径的边

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|relation_type_id|string|true|none|关系类id|
|relation_type|[RelationTypeWithKeyField](#schemarelationtypewithkeyfield)|true|none|关系类信息|
|source_object_type_id|string|true|none|边的起点对象类id|
|target_object_type_id|string|true|none|边的终点对象类id|
|direction|string|true|none|当前边相对于关系类的方向。正向(forward)、反向(backward)、双向(bidirectional)|

#### Enumerated Values

|Property|Value|
|---|---|
|direction|forward|
|direction|reverse|
|direction|bidirectional|

<h2 id="tocS_RelationTypeDetails">RelationTypeDetails</h2>
<!-- backwards compatibility -->
<a id="schemarelationtypedetails"></a>
<a id="schema_RelationTypeDetails"></a>
<a id="tocSrelationtypedetails"></a>
<a id="tocsrelationtypedetails"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "kn_id": "string",
      "source_object_type_id": "string",
      "source_object_type": {
        "id": "string",
        "name": "string",
        "icon": "string",
        "color": "string"
      },
      "target_object_type_id": "string",
      "target_object_type": {
        "id": "string",
        "name": "string",
        "icon": "string",
        "color": "string"
      },
      "type": "direct",
      "mapping_rules": [
        {
          "target_property": {
            "name": "string",
            "display_name": "string"
          },
          "source_property": {
            "name": "string",
            "display_name": "string"
          }
        }
      ],
      "creator": "string",
      "create_time": 0,
      "updater": "string",
      "update_time": 0,
      "detail": "string"
    }
  ]
}

```

关系类详情

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[RelationTypeDetail](#schemarelationtypedetail)]|true|none|关系类详情|

<h2 id="tocS_ReqRelationTypes">ReqRelationTypes</h2>
<!-- backwards compatibility -->
<a id="schemareqrelationtypes"></a>
<a id="schema_ReqRelationTypes"></a>
<a id="tocSreqrelationtypes"></a>
<a id="tocsreqrelationtypes"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "branch": "string",
      "source_object_type_id": "string",
      "target_object_type_id": "string",
      "type": "direct",
      "mapping_rules": [
        {
          "target_property": {
            "name": "string",
            "display_name": "string"
          },
          "source_property": {
            "name": "string",
            "display_name": "string"
          }
        }
      ]
    }
  ]
}

```

批量创建请求体

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[ReqRelationType](#schemareqrelationtype)]|true|none|关系类详情|

<h2 id="tocS_RelationTypePaths">RelationTypePaths</h2>
<!-- backwards compatibility -->
<a id="schemarelationtypepaths"></a>
<a id="schema_RelationTypePaths"></a>
<a id="tocSrelationtypepaths"></a>
<a id="tocsrelationtypepaths"></a>

```json
{
  "entries": [
    {
      "object_types": [
        {
          "id": "string",
          "name": "string",
          "data_source": {
            "type": "data_view",
            "id": "string",
            "name": "string"
          },
          "data_properties": [
            {
              "name": "string",
              "display_name": "string",
              "type": "string",
              "comment": "string",
              "mapped_field": {
                "name": "string",
                "display_name": "string",
                "type": "string"
              },
              "index": true,
              "fulltext_config": {
                "analyzer": "standard",
                "field_keyword": true
              },
              "vector_config": {
                "dimension": 0
              }
            }
          ],
          "logic_properties": [
            {
              "name": "string",
              "display_name": "string",
              "type": "string",
              "comment": "string",
              "index": true,
              "data_source": {
                "type": "metric",
                "id": "string",
                "name": "string"
              },
              "parameters": [
                {
                  "name": "string",
                  "value_from": "property",
                  "value": "string"
                }
              ]
            }
          ],
          "primary_keys": [
            "string"
          ],
          "display_key": "string"
        }
      ],
      "relation_types": [
        {
          "relation_type_id": "string",
          "relation_type": {
            "id": "string",
            "name": "string",
            "source_object_type_id": "string",
            "target_object_type_id": "string",
            "type": "direct",
            "mapping_rules": [
              {
                "target_property": {
                  "name": "string",
                  "display_name": "string"
                },
                "source_property": {
                  "name": "string",
                  "display_name": "string"
                }
              }
            ]
          },
          "source_object_type_id": "string",
          "target_object_type_id": "string",
          "direction": "forward"
        }
      ],
      "length": 0
    }
  ]
}

```

概念路径

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[RelationTypePath](#schemarelationtypepath)]|true|none|[概念路径]|

<h2 id="tocS_FirstQueryWithSearchAfter">FirstQueryWithSearchAfter</h2>
<!-- backwards compatibility -->
<a id="schemafirstquerywithsearchafter"></a>
<a id="schema_FirstQueryWithSearchAfter"></a>
<a id="tocSfirstquerywithsearchafter"></a>
<a id="tocsfirstquerywithsearchafter"></a>

```json
{
  "concept_groups": [
    "string"
  ],
  "condition": {
    "operation": "and",
    "sub_conditions": [
      {
        "operation": "and",
        "sub_conditions": []
      }
    ]
  },
  "sort": [
    {
      "field": "string",
      "direction": "desc"
    }
  ],
  "limit": 0,
  "need_total": true
}

```

关系类检索第一次请求

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|concept_groups|[string]|false|none|概念分组id数组|
|condition|[Condition](#schemacondition)|true|none|关系类检索条件|
|sort|[[Sort](#schemasort)]|false|none|排序字段，默认使用 _score 排序，排序方向为 desc|
|limit|integer|true|none|返回的数量，默认值 10。范围 1-10000|
|need_total|boolean|false|none|是否需要总数，默认false|

<h2 id="tocS_PageTurnQueryWithSearchAfter">PageTurnQueryWithSearchAfter</h2>
<!-- backwards compatibility -->
<a id="schemapageturnquerywithsearchafter"></a>
<a id="schema_PageTurnQueryWithSearchAfter"></a>
<a id="tocSpageturnquerywithsearchafter"></a>
<a id="tocspageturnquerywithsearchafter"></a>

```json
{
  "concept_groups": [
    "string"
  ],
  "condition": {
    "operation": "and",
    "sub_conditions": [
      {
        "operation": "and",
        "sub_conditions": []
      }
    ]
  },
  "sort": [
    {
      "field": "string",
      "direction": "desc"
    }
  ],
  "limit": 0,
  "need_total": true,
  "search_after": [
    null
  ]
}

```

分页查询的后续分页查询请求

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|concept_groups|[string]|false|none|概念分组id数组|
|condition|[Condition](#schemacondition)|true|none|过滤条件|
|sort|[[Sort](#schemasort)]|false|none|排序字段，默认使用 _score 排序，排序方向为 desc|
|limit|integer|true|none|返回的数量，默认值 10。范围 1-10000|
|need_total|boolean|false|none|是否需要总数，默认false|
|search_after|[any]|true|none|上次查询返回的最后一个文档的排序值。|

<h2 id="tocS_RelationPathReqeustBody">RelationPathReqeustBody</h2>
<!-- backwards compatibility -->
<a id="schemarelationpathreqeustbody"></a>
<a id="schema_RelationPathReqeustBody"></a>
<a id="tocSrelationpathreqeustbody"></a>
<a id="tocsrelationpathreqeustbody"></a>

```json
{
  "concept_groups": [
    "string"
  ],
  "source_object_type_id": "string",
  "path_length": 0,
  "direction": "forward"
}

```

路径查询的请求体

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|concept_groups|[string]|false|none|概念分组id数组|
|source_object_type_id|string|true|none|起点对象类ID|
|path_length|integer|true|none|路径长度,不超过3.|
|direction|string|true|none|方向：正向(forward)、反向(backward)、双向(bidirectional)|

#### Enumerated Values

|Property|Value|
|---|---|
|direction|forward|
|direction|reverse|
|direction|bidirectional|

<h2 id="tocS_condition_or">condition_or</h2>
<!-- backwards compatibility -->
<a id="schemacondition_or"></a>
<a id="schema_condition_or"></a>
<a id="tocScondition_or"></a>
<a id="tocscondition_or"></a>

```json
{
  "operation": "or",
  "sub_conditions": [
    {
      "operation": "and",
      "sub_conditions": [
        {
          "operation": "and",
          "sub_conditions": []
        }
      ]
    }
  ]
}

```

or 的过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|operation|string|true|none|过滤操作符|
|sub_conditions|[[Condition](#schemacondition)]|true|none|子过滤条件|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|or|

<h2 id="tocS_condition_eq">condition_eq</h2>
<!-- backwards compatibility -->
<a id="schemacondition_eq"></a>
<a id="schema_condition_eq"></a>
<a id="tocScondition_eq"></a>
<a id="tocscondition_eq"></a>

```json
{
  "field": "id",
  "operation": "==",
  "value": null
}

```

等于过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，等于支持的字段类型：数值、字符串|
|operation|string|true|none|操作符|
|value|any|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|id|
|field|name|
|field|comment|
|field|detail|
|operation|==|

<h2 id="tocS_condition_not_eq">condition_not_eq</h2>
<!-- backwards compatibility -->
<a id="schemacondition_not_eq"></a>
<a id="schema_condition_not_eq"></a>
<a id="tocScondition_not_eq"></a>
<a id="tocscondition_not_eq"></a>

```json
{
  "field": "id",
  "operation": "!=",
  "value": null
}

```

不等于的过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，不等于支持的字段类型：数值、字符串|
|operation|string|true|none|操作符|
|value|any|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|id|
|field|name|
|field|comment|
|field|detail|
|operation|!=|

<h2 id="tocS_condition_in">condition_in</h2>
<!-- backwards compatibility -->
<a id="schemacondition_in"></a>
<a id="schema_condition_in"></a>
<a id="tocScondition_in"></a>
<a id="tocscondition_in"></a>

```json
{
  "field": "id",
  "operation": "in",
  "value": [
    null
  ]
}

```

包含过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，包含支持所有类型|
|operation|string|true|none|操作符|
|value|[any]|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|id|
|field|name|
|field|comment|
|field|detail|
|operation|in|

<h2 id="tocS_condition_like">condition_like</h2>
<!-- backwards compatibility -->
<a id="schemacondition_like"></a>
<a id="schema_condition_like"></a>
<a id="tocScondition_like"></a>
<a id="tocscondition_like"></a>

```json
{
  "field": "id",
  "operation": "like",
  "value": "string"
}

```

like过滤

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，相似支持的字段类型：字符串|
|operation|string|true|none|操作符|
|value|string|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|id|
|field|name|
|field|comment|
|field|detail|
|operation|like|

<h2 id="tocS_condition_not_like">condition_not_like</h2>
<!-- backwards compatibility -->
<a id="schemacondition_not_like"></a>
<a id="schema_condition_not_like"></a>
<a id="tocScondition_not_like"></a>
<a id="tocscondition_not_like"></a>

```json
{
  "field": "id",
  "operation": "not_like",
  "value": "string"
}

```

not_like过滤

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，不相似支持的字段类型：字符串|
|operation|string|true|none|操作符|
|value|string|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|id|
|field|name|
|field|comment|
|field|detail|
|operation|not_like|

<h2 id="tocS_condition_regex">condition_regex</h2>
<!-- backwards compatibility -->
<a id="schemacondition_regex"></a>
<a id="schema_condition_regex"></a>
<a id="tocScondition_regex"></a>
<a id="tocscondition_regex"></a>

```json
{
  "field": "id",
  "operation": "regex",
  "value": "string"
}

```

regex过滤

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，正则支持的字段类型：字符串|
|operation|string|true|none|操作符|
|value|string|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|id|
|field|name|
|field|comment|
|field|detail|
|operation|regex|

<h2 id="tocS_condition_multi_match">condition_multi_match</h2>
<!-- backwards compatibility -->
<a id="schemacondition_multi_match"></a>
<a id="schema_condition_multi_match"></a>
<a id="tocScondition_multi_match"></a>
<a id="tocscondition_multi_match"></a>

```json
{
  "fields": [
    "string"
  ],
  "operation": "multi_match",
  "value": "string",
  "match_type": "best_fields"
}

```

多字段全文匹配

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|fields|[string]|false|none|过滤字段数组，多字段全文匹配支持的字段类型：字符串。为空时，用opensearch中 index.default_field 配置的字段进行查询。当需要对所有字段进行匹配时，此参数传 ["*"]。支持的字段有： name, comment, detail, *|
|operation|string|true|none|操作符|
|value|string|true|none|过滤值|
|match_type|string|false|none|全文匹配类型，默认是 best_fields|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|multi_match|
|match_type|best_fields|
|match_type|most_fields|
|match_type|cross_fields|
|match_type|phrase|
|match_type|phrase_prefix|
|match_type|bool_prefix|

<h2 id="tocS_condition_not_in">condition_not_in</h2>
<!-- backwards compatibility -->
<a id="schemacondition_not_in"></a>
<a id="schema_condition_not_in"></a>
<a id="tocScondition_not_in"></a>
<a id="tocscondition_not_in"></a>

```json
{
  "field": "id",
  "operation": "not_in",
  "value": [
    null
  ]
}

```

not_in过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，不包含支持所有类型|
|operation|string|true|none|操作符|
|value|[any]|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|id|
|field|name|
|field|comment|
|field|detail|
|operation|not_in|

<h2 id="tocS_condition_match_phrase">condition_match_phrase</h2>
<!-- backwards compatibility -->
<a id="schemacondition_match_phrase"></a>
<a id="schema_condition_match_phrase"></a>
<a id="tocScondition_match_phrase"></a>
<a id="tocscondition_match_phrase"></a>

```json
{
  "field": "name",
  "operation": "match_phrase",
  "value": "string"
}

```

match_phrase 过滤，支持单个字段和*, * 表示全部字段

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，短语匹配支持的字段类型：字符串|
|operation|string|true|none|操作符|
|value|string|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|name|
|field|comment|
|field|detail|
|field|*|
|operation|match_phrase|

<h2 id="tocS_condition_match">condition_match</h2>
<!-- backwards compatibility -->
<a id="schemacondition_match"></a>
<a id="schema_condition_match"></a>
<a id="tocScondition_match"></a>
<a id="tocscondition_match"></a>

```json
{
  "field": "name",
  "operation": "match",
  "value": "string"
}

```

match 过滤，支持单个字段和*, * 表示全部字段

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，全文匹配支持的字段类型：字符串|
|operation|string|true|none|操作符|
|value|string|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|field|name|
|field|comment|
|field|detail|
|field|*|
|operation|match|

<h2 id="tocS_condition_knn">condition_knn</h2>
<!-- backwards compatibility -->
<a id="schemacondition_knn"></a>
<a id="schema_condition_knn"></a>
<a id="tocScondition_knn"></a>
<a id="tocscondition_knn"></a>

```json
{
  "field": "*",
  "operation": "knn",
  "value": 0,
  "limit_key": "k",
  "limit_value": 100,
  "sub_conditions": [
    {
      "operation": "and",
      "sub_conditions": [
        {
          "operation": "and",
          "sub_conditions": []
        }
      ]
    }
  ]
}

```

knn 过滤，支持单个字段和*, * 表示"_vector"

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，,概念索引是内部生成，不对外暴露，所以knn过滤时，field 传 * 即可|
|operation|string|true|none|操作符|
|value|number|true|none|过滤值。当limit_key为k时，limit_value为整型；当limit_key为max_distance和min_score时，limit_value为浮点型|
|limit_key|string|false|none|执行径向搜索时使用的过滤和评分行为, k:返回最相似的limit_value个结果；max_distance:返回距离小于等于limit_value的结果；min_score：返回相似度分数大于等于limit_value的结果。默认值为k|
|limit_value|number|false|none|执行径向搜索使用的值。默认值为100|
|sub_conditions|[[Condition](#schemacondition)]|false|none|knn下的子查询|

#### Enumerated Values

|Property|Value|
|---|---|
|field|*|
|operation|knn|
|limit_key|k|
|limit_key|max_distance|
|limit_key|min_score|

<h2 id="tocS_condition_and">condition_and</h2>
<!-- backwards compatibility -->
<a id="schemacondition_and"></a>
<a id="schema_condition_and"></a>
<a id="tocScondition_and"></a>
<a id="tocscondition_and"></a>

```json
{
  "operation": "and",
  "sub_conditions": [
    {
      "operation": "and",
      "sub_conditions": []
    }
  ]
}

```

and的过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|operation|string|true|none|过滤操作符|
|sub_conditions|[[Condition](#schemacondition)]|true|none|子过滤条件|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|and|

<h2 id="tocS_Condition">Condition</h2>
<!-- backwards compatibility -->
<a id="schemacondition"></a>
<a id="schema_Condition"></a>
<a id="tocScondition"></a>
<a id="tocscondition"></a>

```json
{
  "operation": "and",
  "sub_conditions": [
    {
      "operation": "and",
      "sub_conditions": []
    }
  ]
}

```

### Properties

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_and](#schemacondition_and)|false|none|and的过滤条件|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_or](#schemacondition_or)|false|none|or 的过滤条件|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_eq](#schemacondition_eq)|false|none|等于过滤条件|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_not_eq](#schemacondition_not_eq)|false|none|不等于的过滤条件|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_in](#schemacondition_in)|false|none|包含过滤条件|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_not_in](#schemacondition_not_in)|false|none|not_in过滤条件|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_like](#schemacondition_like)|false|none|like过滤|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_not_like](#schemacondition_not_like)|false|none|not_like过滤|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_regex](#schemacondition_regex)|false|none|regex过滤|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_match](#schemacondition_match)|false|none|match 过滤，支持单个字段和*, * 表示全部字段|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_match_phrase](#schemacondition_match_phrase)|false|none|match_phrase 过滤，支持单个字段和*, * 表示全部字段|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_knn](#schemacondition_knn)|false|none|knn 过滤，支持单个字段和*, * 表示"_vector"|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_multi_match](#schemacondition_multi_match)|false|none|多字段全文匹配|



<!-- Generator: Widdershins v4.0.1 -->

<h1 id="risktype">RiskType v0.1.0</h1>


风险类（Risk Type）管理。风险类是一个轻量概念，仅含名称、标签、备注与展示元数据
（icon/color），不绑定行动类或阈值。所有接口在 `/api/bkn-backend/v1` 外网面下，
需 OAuth2 认证；`branch` 查询参数默认 `main`。

# Authentication

- oAuth2 authentication. OAuth2 认证，用于外网接口

    - Flow: clientCredentials

    - Token URL = [/oauth2/token](/oauth2/token)

|Scope|Scope Description|
|---|---|

<h1 id="risktype-risktype">RiskType</h1>

## 创建风险类（批量）或按条件搜索

<a id="opIdcreateOrSearchRiskTypes"></a>

`POST /api/bkn-backend/v1/knowledge-networks/{kn_id}/risk-types`

本端点通过请求头 `x-http-method-override` 分发到两种操作：
- 头缺省或为 `POST`：**批量创建 / 导入**风险类（见请求体 `entries`）。
- 头为 `GET`：**按条件 / 语义搜索**风险类（请求体为查询条件 `ConceptsQuery`）。
- 其它取值：返回 400。

需 `Content-Type: application/json`。

> Body parameter

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string"
    }
  ]
}
```

<h3 id="创建风险类（批量）或按条件搜索-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络 ID|
|branch|query|string|false|分支名称，默认 main|
|x-http-method-override|header|string|false|方法覆盖：空或 `POST` 走批量创建；`GET` 走搜索|
|import_mode|query|string|false|仅创建时生效。`normal`=已存在则报错；`ignore`=跳过已存在；`overwrite`=按 id 覆盖更新。|
|body|body|any|true|none|

#### Enumerated Values

|Parameter|Value|
|---|---|
|x-http-method-override|POST|
|x-http-method-override|GET|
|import_mode|normal|
|import_mode|ignore|
|import_mode|overwrite|

> Example responses

> 200 Response

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "kn_id": "string",
      "branch": "string",
      "module_type": "string",
      "creator": {
        "id": "string",
        "type": "string",
        "name": "string"
      },
      "create_time": 0,
      "updater": {
        "id": "string",
        "type": "string",
        "name": "string"
      },
      "update_time": 0
    }
  ],
  "total_count": 0,
  "search_after": [
    null
  ],
  "overall_ms": 0
}
```

<h3 id="创建风险类（批量）或按条件搜索-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|搜索结果（仅当搜索操作）|[RiskTypesSearchResult](#schemarisktypessearchresult)|
|201|[Created](https://tools.ietf.org/html/rfc7231#section-6.3.2)|创建成功，返回各风险类 id 的数组（仅当创建操作）|Inline|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|参数错误|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|知识网络不存在|None|

<h3 id="创建风险类（批量）或按条件搜索-responseschema">Response Schema</h3>

Status Code **201**

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» id|string|false|none|none|

<aside class="warning">
To perform this operation, you must be authenticated by means of one of the following methods:
OAuth2
</aside>

## 列出风险类

<a id="opIdlistRiskTypes"></a>

`GET /api/bkn-backend/v1/knowledge-networks/{kn_id}/risk-types`

分页列出知识网络内的风险类，支持按名称 / 标签过滤。

<h3 id="列出风险类-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络 ID|
|branch|query|string|false|分支名称，默认 main|
|name_pattern|query|string|false|按名称或 id 模糊匹配，默认空|
|tag|query|string|false|按标签模糊匹配，默认空|
|offset|query|integer(int64)|false|偏移量，>= 0，默认 0|
|limit|query|integer(int64)|false|每页条数，范围 [1,1000]；`-1` 表示不分页返回全部|
|sort|query|string|false|排序字段|
|direction|query|string|false|排序方向|

#### Enumerated Values

|Parameter|Value|
|---|---|
|sort|name|
|sort|update_time|
|direction|asc|
|direction|desc|

> Example responses

> 200 Response

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "kn_id": "string",
      "branch": "string",
      "module_type": "string",
      "creator": {
        "id": "string",
        "type": "string",
        "name": "string"
      },
      "create_time": 0,
      "updater": {
        "id": "string",
        "type": "string",
        "name": "string"
      },
      "update_time": 0
    }
  ],
  "total_count": 0
}
```

<h3 id="列出风险类-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|Inline|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|参数错误|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|知识网络不存在|None|

<h3 id="列出风险类-responseschema">Response Schema</h3>

Status Code **200**

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» entries|[[RiskType](#schemarisktype)]|false|none|[风险类对象]|
|»» id|string|false|none|none|
|»» name|string|false|none|名称，最长 40|
|»» tags|[string]|false|none|标签，最多 5 个|
|»» comment|string|false|none|备注，最长 1000|
|»» icon|string|false|none|none|
|»» color|string|false|none|none|
|»» kn_id|string|false|none|由服务端按路径填充|
|»» branch|string|false|none|由服务端按 branch 查询参数填充|
|»» module_type|string|false|none|恒为 `risk_type`|
|»» creator|[AccountInfo](#schemaaccountinfo)|false|none|账户信息（创建者 / 更新者）|
|»»» id|string|false|none|none|
|»»» type|string|false|none|none|
|»»» name|string|false|none|none|
|»» create_time|integer(int64)|false|none|创建时间（Unix 毫秒）|
|»» updater|[AccountInfo](#schemaaccountinfo)|false|none|账户信息（创建者 / 更新者）|
|»» update_time|integer(int64)|false|none|更新时间（Unix 毫秒）|
|» total_count|integer(int64)|false|none|none|

<aside class="warning">
To perform this operation, you must be authenticated by means of one of the following methods:
OAuth2
</aside>

## 更新风险类

<a id="opIdupdateRiskType"></a>

`PUT /api/bkn-backend/v1/knowledge-networks/{kn_id}/risk-types/{rt_id}`

全量更新单个风险类。需 `Content-Type: application/json`。

> Body parameter

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string"
}
```

<h3 id="更新风险类-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络 ID|
|rt_id|path|string|true|风险类 ID|
|branch|query|string|false|分支名称，默认 main|
|body|body|[RiskTypeInput](#schemarisktypeinput)|true|none|

> Example responses

<h3 id="更新风险类-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|更新成功，无响应体|None|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|参数错误（含名称已存在）|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|知识网络或风险类不存在|None|

<h3 id="更新风险类-responseschema">Response Schema</h3>

<aside class="warning">
To perform this operation, you must be authenticated by means of one of the following methods:
OAuth2
</aside>

## 按 id 批量获取风险类

<a id="opIdgetRiskTypes"></a>

`GET /api/bkn-backend/v1/knowledge-networks/{kn_id}/risk-types/{rt_ids}`

批量获取指定 id 的风险类；任一 id 不存在则整体 404。

<h3 id="按-id-批量获取风险类-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络 ID|
|rt_ids|path|string|true|风险类 ID 列表，逗号分隔|
|branch|query|string|false|分支名称，默认 main|

> Example responses

> 200 Response

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "kn_id": "string",
      "branch": "string",
      "module_type": "string",
      "creator": {
        "id": "string",
        "type": "string",
        "name": "string"
      },
      "create_time": 0,
      "updater": {
        "id": "string",
        "type": "string",
        "name": "string"
      },
      "update_time": 0
    }
  ]
}
```

<h3 id="按-id-批量获取风险类-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|Inline|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|知识网络或某个风险类不存在|None|

<h3 id="按-id-批量获取风险类-responseschema">Response Schema</h3>

Status Code **200**

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» entries|[[RiskType](#schemarisktype)]|false|none|[风险类对象]|
|»» id|string|false|none|none|
|»» name|string|false|none|名称，最长 40|
|»» tags|[string]|false|none|标签，最多 5 个|
|»» comment|string|false|none|备注，最长 1000|
|»» icon|string|false|none|none|
|»» color|string|false|none|none|
|»» kn_id|string|false|none|由服务端按路径填充|
|»» branch|string|false|none|由服务端按 branch 查询参数填充|
|»» module_type|string|false|none|恒为 `risk_type`|
|»» creator|[AccountInfo](#schemaaccountinfo)|false|none|账户信息（创建者 / 更新者）|
|»»» id|string|false|none|none|
|»»» type|string|false|none|none|
|»»» name|string|false|none|none|
|»» create_time|integer(int64)|false|none|创建时间（Unix 毫秒）|
|»» updater|[AccountInfo](#schemaaccountinfo)|false|none|账户信息（创建者 / 更新者）|
|»» update_time|integer(int64)|false|none|更新时间（Unix 毫秒）|

<aside class="warning">
To perform this operation, you must be authenticated by means of one of the following methods:
OAuth2
</aside>

## 批量删除风险类

<a id="opIddeleteRiskTypes"></a>

`DELETE /api/bkn-backend/v1/knowledge-networks/{kn_id}/risk-types/{rt_ids}`

按 id 列表删除一个或多个风险类。

<h3 id="批量删除风险类-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络 ID|
|rt_ids|path|string|true|风险类 ID 列表，逗号分隔|
|branch|query|string|false|分支名称，默认 main|

> Example responses

<h3 id="批量删除风险类-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|删除成功，无响应体|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|知识网络或某个风险类不存在|None|

<h3 id="批量删除风险类-responseschema">Response Schema</h3>

<aside class="warning">
To perform this operation, you must be authenticated by means of one of the following methods:
OAuth2
</aside>

# Schemas

<h2 id="tocS_AccountInfo">AccountInfo</h2>
<!-- backwards compatibility -->
<a id="schemaaccountinfo"></a>
<a id="schema_AccountInfo"></a>
<a id="tocSaccountinfo"></a>
<a id="tocsaccountinfo"></a>

```json
{
  "id": "string",
  "type": "string",
  "name": "string"
}

```

账户信息（创建者 / 更新者）

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|false|none|none|
|type|string|false|none|none|
|name|string|false|none|none|

<h2 id="tocS_RiskType">RiskType</h2>
<!-- backwards compatibility -->
<a id="schemarisktype"></a>
<a id="schema_RiskType"></a>
<a id="tocSrisktype"></a>
<a id="tocsrisktype"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string",
  "kn_id": "string",
  "branch": "string",
  "module_type": "string",
  "creator": {
    "id": "string",
    "type": "string",
    "name": "string"
  },
  "create_time": 0,
  "updater": {
    "id": "string",
    "type": "string",
    "name": "string"
  },
  "update_time": 0
}

```

风险类对象

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|false|none|none|
|name|string|false|none|名称，最长 40|
|tags|[string]|false|none|标签，最多 5 个|
|comment|string|false|none|备注，最长 1000|
|icon|string|false|none|none|
|color|string|false|none|none|
|kn_id|string|false|none|由服务端按路径填充|
|branch|string|false|none|由服务端按 branch 查询参数填充|
|module_type|string|false|none|恒为 `risk_type`|
|creator|[AccountInfo](#schemaaccountinfo)|false|none|账户信息（创建者 / 更新者）|
|create_time|integer(int64)|false|none|创建时间（Unix 毫秒）|
|updater|[AccountInfo](#schemaaccountinfo)|false|none|账户信息（创建者 / 更新者）|
|update_time|integer(int64)|false|none|更新时间（Unix 毫秒）|

<h2 id="tocS_RiskTypeInput">RiskTypeInput</h2>
<!-- backwards compatibility -->
<a id="schemarisktypeinput"></a>
<a id="schema_RiskTypeInput"></a>
<a id="tocSrisktypeinput"></a>
<a id="tocsrisktypeinput"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "comment": "string",
  "icon": "string",
  "color": "string"
}

```

风险类可写字段（创建 / 更新的入参）

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|false|none|可选；创建时留空则自动生成|
|name|string|true|none|none|
|tags|[string]|false|none|none|
|comment|string|false|none|none|
|icon|string|false|none|none|
|color|string|false|none|none|

<h2 id="tocS_RiskTypeCreateRequest">RiskTypeCreateRequest</h2>
<!-- backwards compatibility -->
<a id="schemarisktypecreaterequest"></a>
<a id="schema_RiskTypeCreateRequest"></a>
<a id="tocSrisktypecreaterequest"></a>
<a id="tocsrisktypecreaterequest"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string"
    }
  ]
}

```

批量创建请求

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[RiskTypeInput](#schemarisktypeinput)]|true|none|[风险类可写字段（创建 / 更新的入参）]|

<h2 id="tocS_RiskTypesSearchResult">RiskTypesSearchResult</h2>
<!-- backwards compatibility -->
<a id="schemarisktypessearchresult"></a>
<a id="schema_RiskTypesSearchResult"></a>
<a id="tocSrisktypessearchresult"></a>
<a id="tocsrisktypessearchresult"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "comment": "string",
      "icon": "string",
      "color": "string",
      "kn_id": "string",
      "branch": "string",
      "module_type": "string",
      "creator": {
        "id": "string",
        "type": "string",
        "name": "string"
      },
      "create_time": 0,
      "updater": {
        "id": "string",
        "type": "string",
        "name": "string"
      },
      "update_time": 0
    }
  ],
  "total_count": 0,
  "search_after": [
    null
  ],
  "overall_ms": 0
}

```

搜索结果信封

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[RiskType](#schemarisktype)]|false|none|[风险类对象]|
|total_count|integer(int64)|false|none|仅当请求 need_total 时返回|
|search_after|[any]|false|none|游标，用于翻页|
|overall_ms|integer(int64)|false|none|none|

<h2 id="tocS_ConceptsQuery">ConceptsQuery</h2>
<!-- backwards compatibility -->
<a id="schemaconceptsquery"></a>
<a id="schema_ConceptsQuery"></a>
<a id="tocSconceptsquery"></a>
<a id="tocsconceptsquery"></a>

```json
{
  "concept_groups": [
    "string"
  ],
  "condition": {
    "field": "string",
    "operation": "string",
    "sub_conditions": [
      {}
    ],
    "value_from": "string",
    "value": null
  },
  "need_total": true,
  "limit": 0,
  "sort": [
    {
      "field": "string",
      "direction": "asc"
    }
  ],
  "search_after": [
    null
  ]
}

```

概念搜索查询体（用于 POST + x-http-method-override:GET）

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|concept_groups|[string]|false|none|none|
|condition|[Condition](#schemacondition)|false|none|过滤条件树|
|need_total|boolean|false|none|为 true 时响应返回 total_count|
|limit|integer|false|none|默认 10|
|sort|[object]|false|none|none|
|» field|string|false|none|none|
|» direction|string|false|none|none|
|search_after|[any]|false|none|游标翻页|

#### Enumerated Values

|Property|Value|
|---|---|
|direction|asc|
|direction|desc|

<h2 id="tocS_Condition">Condition</h2>
<!-- backwards compatibility -->
<a id="schemacondition"></a>
<a id="schema_Condition"></a>
<a id="tocScondition"></a>
<a id="tocscondition"></a>

```json
{
  "field": "string",
  "operation": "string",
  "sub_conditions": [
    {
      "field": "string",
      "operation": "string",
      "sub_conditions": [],
      "value_from": "string",
      "value": null
    }
  ],
  "value_from": "string",
  "value": null
}

```

过滤条件树

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|false|none|none|
|operation|string|false|none|none|
|sub_conditions|[[Condition](#schemacondition)]|false|none|[过滤条件树]|
|value_from|string|false|none|none|
|value|any|false|none|类型不固定|



