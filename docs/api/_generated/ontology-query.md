<!-- Generator: Widdershins v4.0.1 -->

<h1 id="ontology-query">ontology-query v0.2.0</h1>


<h1 id="ontology-query-default">Default</h1>

## 检索指定对象类的对象的详细数据

`POST /api/ontology-query/v1/knowledge-networks/{kn_id}/object-types/{ot_id}`

> Body parameter

```json
{
  "condition": {
    "operation": "and",
    "sub_conditions": [
      {
        "field": "pod_namespace",
        "operation": "==",
        "value": "anyshare",
        "value_from": "const"
      },
      {
        "field": "pod_status",
        "operation": "==",
        "value": "Running",
        "value_from": "const"
      }
    ]
  },
  "need_total": true,
  "limit": 10
}
```

<h3 id="检索指定对象类的对象的详细数据-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|ot_id|path|string|true|对象类ID|
|include_type_info|query|boolean|false|是否包含对象类信息, 默认false，不包含|
|include_logic_params|query|boolean|false|包含逻辑属性的计算参数，默认false，返回结果不包含逻辑属性的字段和值|
|exclude_system_properties|query|array[string]|false|需要排除的系统字段列表。可选值：_instance_id（实例ID）、_instance_identity（实例唯一标识）、_display（显示值）。如果指定了某个字段，则在返回的对象数据中不包含该字段。可以传递多个值，例如：?exclude_system_properties=_instance_id&exclude_system_properties=_display|
|ignoring_store_cache|query|boolean|false|是否忽略索引查询，默认false，不忽略，即走索引查询|
|X-HTTP-Method-Override|header|string|true|重载 post，实际上是 get 方法|
|body|body|any|true|none|

#### Enumerated Values

|Parameter|Value|
|---|---|
|exclude_system_properties|_instance_id|
|exclude_system_properties|_instance_identity|
|exclude_system_properties|_display|
|X-HTTP-Method-Override|GET|

> Example responses

> ok

```json
{
  "object_type": {
    "id": "pod_has_metric_and_operator",
    "name": "pod_has_metric_and_operator",
    "tags": [],
    "comment": "",
    "icon": "icon-dip-suanziguanli",
    "color": "#0e5fc5",
    "branch": "main",
    "detail": "",
    "kn_id": "topologytest",
    "data_source": {
      "type": "data_view",
      "id": "d3riul5r3eoemkcu9sn0"
    },
    "data_properties": [
      {
        "name": "component",
        "display_name": "component",
        "type": "varchar",
        "comment": "",
        "mapped_field": {
          "name": "component",
          "type": "varchar",
          "display_name": "component"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "id",
        "display_name": "id",
        "type": "int",
        "comment": "",
        "mapped_field": {
          "name": "id",
          "type": "int",
          "display_name": "id"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "node_id",
        "display_name": "node_id",
        "type": "varchar",
        "comment": "",
        "mapped_field": {
          "name": "node_id",
          "type": "varchar",
          "display_name": "node_id"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "pod_cluster_id",
        "display_name": "pod_cluster_id",
        "type": "varchar",
        "comment": "",
        "mapped_field": {
          "name": "pod_cluster_id",
          "type": "varchar",
          "display_name": "pod_cluster_id"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "pod_create_time",
        "display_name": "pod_create_time",
        "type": "varchar",
        "comment": "",
        "mapped_field": {
          "name": "pod_create_time",
          "type": "varchar",
          "display_name": "pod_create_time"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "pod_delete_time",
        "display_name": "pod_delete_time",
        "type": "varchar",
        "comment": "",
        "mapped_field": {
          "name": "pod_delete_time",
          "type": "varchar",
          "display_name": "pod_delete_time"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "pod_ip",
        "display_name": "pod_ip",
        "type": "varchar",
        "comment": "",
        "mapped_field": {
          "name": "pod_ip",
          "type": "varchar",
          "display_name": "pod_ip"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "pod_name",
        "display_name": "pod_name",
        "type": "varchar",
        "comment": "",
        "mapped_field": {
          "name": "pod_name",
          "type": "varchar",
          "display_name": "pod_name"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "pod_namespace",
        "display_name": "pod_namespace",
        "type": "varchar",
        "comment": "",
        "mapped_field": {
          "name": "pod_namespace",
          "type": "varchar",
          "display_name": "pod_namespace"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "pod_node_name",
        "display_name": "pod_node_name",
        "type": "varchar",
        "comment": "",
        "mapped_field": {
          "name": "pod_node_name",
          "type": "varchar",
          "display_name": "pod_node_name"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "pod_port",
        "display_name": "pod_port",
        "type": "varchar",
        "comment": "",
        "mapped_field": {
          "name": "pod_port",
          "type": "varchar",
          "display_name": "pod_port"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "pod_status",
        "display_name": "pod_status",
        "type": "varchar",
        "comment": "",
        "mapped_field": {
          "name": "pod_status",
          "type": "varchar",
          "display_name": "pod_status"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "s_create_time",
        "display_name": "s_create_time",
        "type": "timestamp",
        "comment": "",
        "mapped_field": {
          "name": "s_create_time",
          "type": "timestamp",
          "display_name": "s_create_time"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "s_update_time",
        "display_name": "s_update_time",
        "type": "timestamp",
        "comment": "",
        "mapped_field": {
          "name": "s_update_time",
          "type": "timestamp",
          "display_name": "s_update_time"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "service_ip",
        "display_name": "service_ip",
        "type": "varchar",
        "comment": "",
        "mapped_field": {
          "name": "service_ip",
          "type": "varchar",
          "display_name": "service_ip"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "service_name",
        "display_name": "service_name",
        "type": "varchar",
        "comment": "",
        "mapped_field": {
          "name": "service_name",
          "type": "varchar",
          "display_name": "service_name"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "timestamp",
        "display_name": "timestamp",
        "type": "timestamp",
        "comment": "",
        "mapped_field": {
          "name": "timestamp",
          "type": "timestamp",
          "display_name": "timestamp"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      }
    ],
    "logic_properties": [
      {
        "name": "operator_test",
        "display_name": "operator_test",
        "type": "operator",
        "comment": "pod的算子",
        "index": false,
        "data_source": {
          "type": "operator",
          "id": "657a114e-2061-4314-8853-f84afecc5113"
        },
        "parameters": [
          {
            "name": "Authorization",
            "value_from": "input"
          },
          {
            "name": "string",
            "value_from": "property",
            "value": "pod_ip"
          }
        ]
      },
      {
        "name": "metric_test",
        "display_name": "metric_test",
        "type": "metric",
        "comment": "pod的指标",
        "index": false,
        "data_source": {
          "type": "metric",
          "id": "pod_metric"
        },
        "parameters": [
          {
            "name": "pod_ip",
            "value_from": "property",
            "value": "pod_ip"
          },
          {
            "name": "pod_name",
            "value_from": "property",
            "value": "pod_name"
          },
          {
            "name": "instant",
            "value_from": "input"
          },
          {
            "name": "start",
            "value_from": "input"
          },
          {
            "name": "end",
            "value_from": "input"
          },
          {
            "name": "step",
            "value_from": "input"
          }
        ]
      },
      {
        "name": "a_action",
        "display_name": "_action",
        "type": "varchar",
        "comment": "",
        "index": false,
        "data_source": {
          "type": "",
          "id": ""
        }
      }
    ],
    "primary_keys": [],
    "display_key": "",
    "creator": "d5cf219c-a7fd-11f0-9094-f63c61a25eef",
    "create_time": 1761717480956,
    "updater": "e584a6d8-b550-11f0-a6f7-f63c61a25eef",
    "update_time": 1761882424382,
    "IfNameModify": false,
    "module_type": "object_type"
  },
  "datas": [
    {
      "pod_cluster_id": "",
      "timestamp": "2025-06-20 11:15:53.000",
      "pod_port": "9998",
      "s_update_time": "2025-08-26 11:21:01.000",
      "service_ip": "",
      "service_name": "aladdin-tika-deploy",
      "s_create_time": "2025-06-20 11:15:53.000",
      "pod_node_name": "node-111-175",
      "pod_create_time": "1750389421667",
      "operator_test": {
        "property_type": "operator",
        "mapping_source_id": "657a114e-2061-4314-8853-f84afecc5113",
        "has_any_unfilled_params": false,
        "parameters": {
          "string": "192.169.37.17"
        },
        "dynamic_params": {}
      },
      "metric_test": {
        "property_type": "metric",
        "mapping_source_id": "pod_metric",
        "has_any_unfilled_params": false,
        "parameters": {
          "filters": [
            {
              "name": "pod_ip",
              "operation": "",
              "value": "192.169.37.17"
            },
            {
              "name": "pod_name",
              "operation": "",
              "value": "aladdin-tika-deploy-6564c49fbb-2nb9k"
            }
          ]
        },
        "dynamic_params": {}
      },
      "id": 1471,
      "pod_delete_time": "",
      "pod_name": "aladdin-tika-deploy-6564c49fbb-2nb9k",
      "pod_namespace": "anyshare",
      "pod_ip": "192.169.37.17",
      "pod_status": "Running",
      "component": "aladdin-tika-2.7.0"
    }
  ],
  "total_count": 1,
  "search_after": [
    "20251103_070016_00004_5pjz5",
    "x6f100cb5d44b4bb19facd4036759b509",
    "1"
  ]
}
```

```json
{
  "object_type": {
    "id": "pod_has_metric_and_operator",
    "name": "pod_has_metric_and_operator",
    "tags": [],
    "comment": "",
    "icon": "icon-dip-suanziguanli",
    "color": "#0e5fc5",
    "branch": "main",
    "detail": "",
    "kn_id": "topologytest",
    "data_source": {
      "type": "data_view",
      "id": "d3riul5r3eoemkcu9sn0"
    },
    "data_properties": [
      {
        "name": "component",
        "display_name": "component",
        "type": "varchar",
        "comment": "",
        "mapped_field": {
          "name": "component",
          "type": "varchar",
          "display_name": "component"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "id",
        "display_name": "id",
        "type": "int",
        "comment": "",
        "mapped_field": {
          "name": "id",
          "type": "int",
          "display_name": "id"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "node_id",
        "display_name": "node_id",
        "type": "varchar",
        "comment": "",
        "mapped_field": {
          "name": "node_id",
          "type": "varchar",
          "display_name": "node_id"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "pod_cluster_id",
        "display_name": "pod_cluster_id",
        "type": "varchar",
        "comment": "",
        "mapped_field": {
          "name": "pod_cluster_id",
          "type": "varchar",
          "display_name": "pod_cluster_id"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "pod_create_time",
        "display_name": "pod_create_time",
        "type": "varchar",
        "comment": "",
        "mapped_field": {
          "name": "pod_create_time",
          "type": "varchar",
          "display_name": "pod_create_time"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "pod_delete_time",
        "display_name": "pod_delete_time",
        "type": "varchar",
        "comment": "",
        "mapped_field": {
          "name": "pod_delete_time",
          "type": "varchar",
          "display_name": "pod_delete_time"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "pod_ip",
        "display_name": "pod_ip",
        "type": "varchar",
        "comment": "",
        "mapped_field": {
          "name": "pod_ip",
          "type": "varchar",
          "display_name": "pod_ip"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "pod_name",
        "display_name": "pod_name",
        "type": "varchar",
        "comment": "",
        "mapped_field": {
          "name": "pod_name",
          "type": "varchar",
          "display_name": "pod_name"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "pod_namespace",
        "display_name": "pod_namespace",
        "type": "varchar",
        "comment": "",
        "mapped_field": {
          "name": "pod_namespace",
          "type": "varchar",
          "display_name": "pod_namespace"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "pod_node_name",
        "display_name": "pod_node_name",
        "type": "varchar",
        "comment": "",
        "mapped_field": {
          "name": "pod_node_name",
          "type": "varchar",
          "display_name": "pod_node_name"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "pod_port",
        "display_name": "pod_port",
        "type": "varchar",
        "comment": "",
        "mapped_field": {
          "name": "pod_port",
          "type": "varchar",
          "display_name": "pod_port"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "pod_status",
        "display_name": "pod_status",
        "type": "varchar",
        "comment": "",
        "mapped_field": {
          "name": "pod_status",
          "type": "varchar",
          "display_name": "pod_status"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "s_create_time",
        "display_name": "s_create_time",
        "type": "timestamp",
        "comment": "",
        "mapped_field": {
          "name": "s_create_time",
          "type": "timestamp",
          "display_name": "s_create_time"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "s_update_time",
        "display_name": "s_update_time",
        "type": "timestamp",
        "comment": "",
        "mapped_field": {
          "name": "s_update_time",
          "type": "timestamp",
          "display_name": "s_update_time"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "service_ip",
        "display_name": "service_ip",
        "type": "varchar",
        "comment": "",
        "mapped_field": {
          "name": "service_ip",
          "type": "varchar",
          "display_name": "service_ip"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "service_name",
        "display_name": "service_name",
        "type": "varchar",
        "comment": "",
        "mapped_field": {
          "name": "service_name",
          "type": "varchar",
          "display_name": "service_name"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      },
      {
        "name": "timestamp",
        "display_name": "timestamp",
        "type": "timestamp",
        "comment": "",
        "mapped_field": {
          "name": "timestamp",
          "type": "timestamp",
          "display_name": "timestamp"
        },
        "fulltext_config": {
          "analyzer": "",
          "field_keyword": false
        },
        "vector_config": {
          "dimension": 0
        }
      }
    ],
    "logic_properties": [
      {
        "name": "operator_test",
        "display_name": "operator_test",
        "type": "operator",
        "comment": "pod的算子",
        "index": false,
        "data_source": {
          "type": "operator",
          "id": "657a114e-2061-4314-8853-f84afecc5113"
        },
        "parameters": [
          {
            "name": "Authorization",
            "value_from": "input"
          },
          {
            "name": "string",
            "value_from": "property",
            "value": "pod_ip"
          }
        ]
      },
      {
        "name": "metric_test",
        "display_name": "metric_test",
        "type": "metric",
        "comment": "pod的指标",
        "index": false,
        "data_source": {
          "type": "metric",
          "id": "pod_metric"
        },
        "parameters": [
          {
            "name": "pod_ip",
            "value_from": "property",
            "value": "pod_ip"
          },
          {
            "name": "pod_name",
            "value_from": "property",
            "value": "pod_name"
          },
          {
            "name": "instant",
            "value_from": "input"
          },
          {
            "name": "start",
            "value_from": "input"
          },
          {
            "name": "end",
            "value_from": "input"
          },
          {
            "name": "step",
            "value_from": "input"
          }
        ]
      },
      {
        "name": "a_action",
        "display_name": "_action",
        "type": "varchar",
        "comment": "",
        "index": false,
        "data_source": {
          "type": "",
          "id": ""
        }
      }
    ],
    "primary_keys": [],
    "display_key": "",
    "creator": "d5cf219c-a7fd-11f0-9094-f63c61a25eef",
    "create_time": 1761717480956,
    "updater": "e584a6d8-b550-11f0-a6f7-f63c61a25eef",
    "update_time": 1761882424382,
    "IfNameModify": false,
    "module_type": "object_type"
  },
  "datas": [
    {
      "pod_cluster_id": "",
      "timestamp": "2025-06-20 11:15:53.000",
      "pod_port": "9998",
      "s_update_time": "2025-08-26 11:21:01.000",
      "service_ip": "",
      "service_name": "aladdin-tika-deploy",
      "s_create_time": "2025-06-20 11:15:53.000",
      "pod_node_name": "node-111-175",
      "pod_create_time": "1750389421667",
      "operator_test": {
        "property_type": "operator",
        "mapping_source_id": "657a114e-2061-4314-8853-f84afecc5113",
        "has_any_unfilled_params": false,
        "parameters": {
          "string": "192.169.37.17"
        },
        "dynamic_params": {}
      },
      "metric_test": {
        "property_type": "metric",
        "mapping_source_id": "pod_metric",
        "has_any_unfilled_params": false,
        "parameters": {
          "filters": [
            {
              "name": "pod_ip",
              "operation": "",
              "value": "192.169.37.17"
            },
            {
              "name": "pod_name",
              "operation": "",
              "value": "aladdin-tika-deploy-6564c49fbb-2nb9k"
            }
          ]
        },
        "dynamic_params": {}
      },
      "id": 1471,
      "pod_delete_time": "",
      "pod_name": "aladdin-tika-deploy-6564c49fbb-2nb9k",
      "pod_namespace": "anyshare",
      "pod_ip": "192.169.37.17",
      "pod_status": "Running",
      "component": "aladdin-tika-2.7.0"
    }
  ],
  "total_count": 1,
  "search_after": [
    "20251103_070016_00004_5pjz5",
    "x6f100cb5d44b4bb19facd4036759b509",
    "1"
  ]
}
```

<h3 id="检索指定对象类的对象的详细数据-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[ObjectDataResponse](#schemaobjectdataresponse)|

<aside class="success">
This operation does not require authentication
</aside>

## 行动查询

`POST /api/ontology-query/v1/knowledge-networks/{kn_id}/action-types/{at_id}`

> Body parameter

```json
{
  "_instance_identities": [
    {
      "c_commentid": "32485"
    },
    {
      "c_commentid": "32696"
    }
  ],
  "dynamic_params": {
    "Authorization": "Bearer xxx"
  }
}
```

<h3 id="行动查询-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络id|
|at_id|path|string|true|行动类id|
|branch|query|string|false|分支名称，默认 main|
|include_type_info|query|boolean|false|是否包含行动类信息（`true`/`false`）。未传时按 `false` 处理。|
|exclude_system_properties|query|array[string]|false|需要排除的系统字段列表。可选值：_instance_id（实例ID）、_instance_identity（实例唯一标识）、_display（显示值）。如果指定了某个字段，则在返回的对象数据中不包含该字段。可以传递多个值，例如：?exclude_system_properties=_instance_id&exclude_system_properties=_display|
|x-http-method-override|header|string|true|必须为 `GET`，与 POST 语义组合以实现「POST 载荷 + GET 语义」的行动查询。|
|body|body|[ActionQuery](#schemaactionquery)|true|none|

#### Enumerated Values

|Parameter|Value|
|---|---|
|exclude_system_properties|_instance_id|
|exclude_system_properties|_instance_identity|
|exclude_system_properties|_display|
|x-http-method-override|GET|

> Example responses

> ok

```json
{
  "action_type": {
    "id": "comment_update",
    "name": "comment_update",
    "action_type": "modify",
    "object_type_id": "comment",
    "condition": {
      "object_type_id": "comment",
      "field": "c_browserused",
      "operation": "==",
      "value_from": "const",
      "value": "Firefox"
    },
    "action_source": {
      "type": "tool",
      "box_id": "939bb1db-9239-43f5-8100-ba03eed683db",
      "tool_id": "13d6a740-2d04-4834-8b09-ed047cd2391f"
    },
    "parameters": [
      {
        "name": "props.data_source",
        "type": "object",
        "source": "Body",
        "value_from": "property",
        "value": "c_commentid"
      },
      {
        "name": "props.headers",
        "type": "object",
        "source": "Body",
        "value_from": "input"
      },
      {
        "name": "query",
        "type": "string",
        "source": "Body",
        "value_from": "const",
        "value": "asasddd"
      }
    ],
    "schedule": {
      "type": "",
      "expression": ""
    }
  },
  "action_source": {
    "type": "tool",
    "box_id": "939bb1db-9239-43f5-8100-ba03eed683db",
    "tool_id": "13d6a740-2d04-4834-8b09-ed047cd2391f"
  },
  "actions": [
    {
      "parameters": {
        "props": {
          "data_source": 32485
        },
        "query": "asasddd"
      },
      "dynamic_params": {
        "props": {}
      }
    }
  ],
  "total_count": 1,
  "overall_ms": 1163
}
```

<h3 id="行动查询-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[Actions](#schemaactions)|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数错误。例如行动类存在 value_from 为 input 的参数时，须在请求体 dynamic_params 中传入全部对应取值，否则返回 400（如 OntologyQuery.ActionType.InvalidParameter.DynamicParams）。|None|

<h3 id="行动查询-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 子图查询

`POST /api/ontology-query/v1/knowledge-networks/{kn_id}/subgraph`

子图查询，可以是基于起点、方向和路径长度获取对象子图，有可以是基于路径获取对象子图，查询请求方式和返回体不同

> Body parameter

```json
{
  "source_object_type_id": "comment",
  "direction": "forward",
  "path_length": 2,
  "need_total": true,
  "limit": 2
}
```

<h3 id="子图查询-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|branch|query|string|false|分支名称，默认 main（与实现 DefaultQuery branch 一致）|
|include_logic_params|query|boolean|false|包含逻辑属性的计算参数，默认false，返回结果不包含逻辑属性的字段和值|
|exclude_system_properties|query|array[string]|false|需要排除的系统字段列表。可选值：_instance_id（实例ID）、_instance_identity（实例唯一标识）、_display（显示值）。如果指定了某个字段，则在返回的对象数据中不包含该字段。可以传递多个值，例如：?exclude_system_properties=_instance_id&exclude_system_properties=_display|
|query_type|query|string|false|查询类型，默认是子图探索，即基于起点、方向和路径长度获取对象子图；若是relation_path，则表示基于路径获取对象子图|
|ignoring_store_cache|query|boolean|false|是否忽略索引查询，默认false，不忽略，即走索引查询|
|x-http-method-override|header|string|true|重载 POST，须为 GET（与对象实例查询等接口一致，实现中强制校验）|
|body|body|any|true|子图查询请求体|

#### Enumerated Values

|Parameter|Value|
|---|---|
|exclude_system_properties|_instance_id|
|exclude_system_properties|_instance_identity|
|exclude_system_properties|_display|
|query_type||
|query_type|relation_path|
|x-http-method-override|GET|

> Example responses

> 对象子图

```json
{
  "objects": {
    "person-870": {
      "id": "person-870",
      "_instance_identities": {
        "p_personid": 870
      },
      "object_type_id": "person",
      "object_type_name": "person",
      "display": "male",
      "properties": {
        "p_creationdate": "2010-02-22 00:14:54.000",
        "p_locationip": "91.216.206.97",
        "p_browserused": "Firefox",
        "p_personid": 870,
        "p_firstname": "Dionysis",
        "p_lastname": "Karvelas",
        "p_gender": "male",
        "p_birthday": "1987-05-23"
      }
    },
    "place-78": {
      "id": "place-78",
      "_instance_identities": {
        "p_placeid": 78
      },
      "object_type_id": "place",
      "object_type_name": "place",
      "display": "Greece",
      "properties": {
        "p_placeid": 78,
        "p_name": "Greece",
        "p_url": "http://dbpedia.org/resource/Greece",
        "p_type": "country"
      }
    },
    "post-4.81036361318e+11": {
      "id": "post-4.81036361318e+11",
      "_instance_identities": {
        "ps_postid": 481036361318
      },
      "object_type_id": "post",
      "object_type_name": "post",
      "display": "",
      "properties": {
        "ps_creationdate": "2011-04-27 04:12:24.000",
        "ps_locationip": "31.216.154.26",
        "ps_browserused": "Chrome",
        "ps_language": "",
        "ps_content": "",
        "ps_length": 0,
        "ps_postid": 481036361318,
        "ps_imagefile": "photo481036361318.jpg"
      }
    },
    "post-4.12316884425e+11": {
      "id": "post-4.12316884425e+11",
      "_instance_identities": {
        "ps_postid": 412316884425
      },
      "object_type_id": "post",
      "object_type_name": "post",
      "display": "",
      "properties": {
        "ps_content": "",
        "ps_length": 0,
        "ps_postid": 412316884425,
        "ps_imagefile": "photo412316884425.jpg",
        "ps_creationdate": "2011-03-05 14:51:46.000",
        "ps_locationip": "31.216.154.26",
        "ps_browserused": "Chrome",
        "ps_language": ""
      }
    },
    "comment-2.06158448803e+11": {
      "id": "comment-2.06158448803e+11",
      "_instance_identities": {
        "c_commentid": 206158448803
      },
      "object_type_id": "comment",
      "object_type_name": "comment",
      "display": "About Titian, stic manner cAbout Leona Lewis, tively. A mulAbout Walt Disney, fornia, constAbout Sarah Palin, i",
      "properties": {
        "c_commentid": 206158448803,
        "c_creationdate": "2010-07-24 00:04:01.000",
        "c_locationip": "186.169.83.109",
        "c_browserused": "Firefox",
        "c_content": "About Titian, stic manner cAbout Leona Lewis, tively. A mulAbout Walt Disney, fornia, constAbout Sarah Palin, i",
        "c_length": 111
      }
    },
    "comment-7.55914262659e+11": {
      "id": "comment-7.55914262659e+11",
      "_instance_identities": {
        "c_commentid": 755914262659
      },
      "object_type_id": "comment",
      "object_type_name": "comment",
      "display": "Original of the Species is a song by rock band U2 and the tenth track from their 2004 album, How to Dismantle an Atomic Bomb.About Original of the Spec",
      "properties": {
        "c_length": 151,
        "c_commentid": 755914262659,
        "c_creationdate": "2011-12-12 12:04:24.000",
        "c_locationip": "186.169.83.109",
        "c_browserused": "Firefox",
        "c_content": "Original of the Species is a song by rock band U2 and the tenth track from their 2004 album, How to Dismantle an Atomic Bomb.About Original of the Spec"
      }
    },
    "comment-1.030792168521e+12": {
      "id": "comment-1.030792168521e+12",
      "_instance_identities": {
        "c_commentid": 1030792168521
      },
      "object_type_id": "comment",
      "object_type_name": "comment",
      "display": "About Polish–Lithuanian Commonwealth, 1573; however, the degree of religious freedom var",
      "properties": {
        "c_commentid": 1030792168521,
        "c_creationdate": "2012-08-02 04:19:17.000",
        "c_locationip": "213.55.122.73",
        "c_browserused": "Firefox",
        "c_content": "About Polish–Lithuanian Commonwealth, 1573; however, the degree of religious freedom var",
        "c_length": 88
      }
    },
    "comment-32485": {
      "id": "comment-32485",
      "_instance_identities": {
        "c_commentid": 32485
      },
      "object_type_id": "comment",
      "object_type_name": "comment",
      "display": "great",
      "properties": {
        "c_locationip": "62.217.119.183",
        "c_browserused": "Firefox",
        "c_content": "great",
        "c_length": 5,
        "c_commentid": 32485,
        "c_creationdate": "2010-01-23 06:20:25.000"
      }
    },
    "post-32484": {
      "id": "post-32484",
      "_instance_identities": {
        "ps_postid": 32484
      },
      "object_type_id": "post",
      "object_type_name": "post",
      "display": "About Augustine of Hippo,  also considered a saint, his feast day being celebrated on 15 June.. He carries the additional",
      "properties": {
        "ps_imagefile": "",
        "ps_creationdate": "2010-01-23 06:20:15.000",
        "ps_locationip": "1.4.5.106",
        "ps_browserused": "Firefox",
        "ps_language": "uz",
        "ps_content": "About Augustine of Hippo,  also considered a saint, his feast day being celebrated on 15 June.. He carries the additional",
        "ps_length": 121,
        "ps_postid": 32484
      }
    },
    "person-555": {
      "id": "person-555",
      "_instance_identities": {
        "p_personid": 555
      },
      "object_type_id": "person",
      "object_type_name": "person",
      "display": "female",
      "properties": {
        "p_birthday": "1981-11-08",
        "p_creationdate": "2010-01-08 06:41:35.000",
        "p_locationip": "1.4.5.106",
        "p_browserused": "Firefox",
        "p_personid": 555,
        "p_firstname": "Chen",
        "p_lastname": "Yang",
        "p_gender": "female"
      }
    },
    "place-1456": {
      "id": "place-1456",
      "_instance_identities": {
        "p_placeid": 1456
      },
      "object_type_id": "place",
      "object_type_name": "place",
      "display": "Europe",
      "properties": {
        "p_name": "Europe",
        "p_url": "http://dbpedia.org/resource/Europe",
        "p_type": "continent",
        "p_placeid": 1456
      }
    },
    "tag-1672": {
      "id": "tag-1672",
      "_instance_identities": {
        "t_tagid": 1672
      },
      "object_type_id": "tag",
      "object_type_name": "tag",
      "display": "Nicholas_II_of_Russia",
      "properties": {
        "t_tagid": 1672,
        "t_name": "Nicholas_II_of_Russia",
        "t_url": "http://dbpedia.org/resource/Nicholas_II_of_Russia"
      }
    },
    "post-3.43597402501e+11": {
      "id": "post-3.43597402501e+11",
      "_instance_identities": {
        "ps_postid": 343597402501
      },
      "object_type_id": "post",
      "object_type_name": "post",
      "display": "",
      "properties": {
        "ps_creationdate": "2011-01-02 19:20:12.000",
        "ps_locationip": "31.177.57.251",
        "ps_browserused": "Firefox",
        "ps_language": "",
        "ps_content": "",
        "ps_length": 0,
        "ps_postid": 343597402501,
        "ps_imagefile": "photo343597402501.jpg"
      }
    },
    "comment-32486": {
      "id": "comment-32486",
      "_instance_identities": {
        "c_commentid": 32486
      },
      "object_type_id": "comment",
      "object_type_name": "comment",
      "display": "ok",
      "properties": {
        "c_length": 2,
        "c_commentid": 32486,
        "c_creationdate": "2010-01-23 08:14:18.000",
        "c_locationip": "62.217.119.183",
        "c_browserused": "Firefox",
        "c_content": "ok"
      }
    },
    "tag-6": {
      "id": "tag-6",
      "_instance_identities": {
        "t_tagid": 6
      },
      "object_type_id": "tag",
      "object_type_name": "tag",
      "display": "Augustine_of_Hippo",
      "properties": {
        "t_name": "Augustine_of_Hippo",
        "t_url": "http://dbpedia.org/resource/Augustine_of_Hippo",
        "t_tagid": 6
      }
    },
    "person-143": {
      "id": "person-143",
      "_instance_identities": {
        "p_personid": 143
      },
      "object_type_id": "person",
      "object_type_name": "person",
      "display": "female",
      "properties": {
        "p_locationip": "62.217.119.183",
        "p_browserused": "Firefox",
        "p_personid": 143,
        "p_firstname": "Maria",
        "p_lastname": "Alkaios",
        "p_gender": "female",
        "p_birthday": "1983-01-06",
        "p_creationdate": "2010-01-06 07:19:10.000"
      }
    },
    "person-345": {
      "id": "person-345",
      "_instance_identities": {
        "p_personid": 345
      },
      "object_type_id": "person",
      "object_type_name": "person",
      "display": "male",
      "properties": {
        "p_lastname": "Herzigová",
        "p_gender": "male",
        "p_birthday": "1985-03-17",
        "p_creationdate": "2010-02-27 10:35:06.000",
        "p_locationip": "31.222.0.232",
        "p_browserused": "Internet Explorer",
        "p_personid": 345,
        "p_firstname": "David"
      }
    },
    "tag-273": {
      "id": "tag-273",
      "_instance_identities": {
        "t_tagid": 273
      },
      "object_type_id": "tag",
      "object_type_name": "tag",
      "display": "Aung_San_Suu_Kyi",
      "properties": {
        "t_name": "Aung_San_Suu_Kyi",
        "t_url": "http://dbpedia.org/resource/Aung_San_Suu_Kyi",
        "t_tagid": 273
      }
    },
    "tag-779": {
      "id": "tag-779",
      "_instance_identities": {
        "t_tagid": 779
      },
      "object_type_id": "tag",
      "object_type_name": "tag",
      "display": "George_Frideric_Handel",
      "properties": {
        "t_url": "http://dbpedia.org/resource/George_Frideric_Handel",
        "t_tagid": 779,
        "t_name": "George_Frideric_Handel"
      }
    },
    "tag-1024": {
      "id": "tag-1024",
      "_instance_identities": {
        "t_tagid": 1024
      },
      "object_type_id": "tag",
      "object_type_name": "tag",
      "display": "George_Orwell",
      "properties": {
        "t_tagid": 1024,
        "t_name": "George_Orwell",
        "t_url": "http://dbpedia.org/resource/George_Orwell"
      }
    },
    "post-6.87194784952e+11": {
      "id": "post-6.87194784952e+11",
      "_instance_identities": {
        "ps_postid": 687194784952
      },
      "object_type_id": "post",
      "object_type_name": "post",
      "display": "",
      "properties": {
        "ps_content": "",
        "ps_length": 0,
        "ps_postid": 687194784952,
        "ps_imagefile": "photo687194784952.jpg",
        "ps_creationdate": "2011-10-08 07:00:36.000",
        "ps_locationip": "196.32.227.213",
        "ps_browserused": "Firefox",
        "ps_language": ""
      }
    },
    "comment-3.43597407147e+11": {
      "id": "comment-3.43597407147e+11",
      "_instance_identities": {
        "c_commentid": 343597407147
      },
      "object_type_id": "comment",
      "object_type_name": "comment",
      "display": "About Stevie Wonder,  also include Talking Book, Innervisions and Songs in the Key of Life",
      "properties": {
        "c_browserused": "Firefox",
        "c_content": "About Stevie Wonder,  also include Talking Book, Innervisions and Songs in the Key of Life",
        "c_length": 90,
        "c_commentid": 343597407147,
        "c_creationdate": "2010-12-19 21:58:28.000",
        "c_locationip": "41.138.42.48"
      }
    },
    "person-318": {
      "id": "person-318",
      "_instance_identities": {
        "p_personid": 318
      },
      "object_type_id": "person",
      "object_type_name": "person",
      "display": "female",
      "properties": {
        "p_browserused": "Internet Explorer",
        "p_personid": 318,
        "p_firstname": "Claude",
        "p_lastname": "Radafison",
        "p_gender": "female",
        "p_birthday": "1989-04-17",
        "p_creationdate": "2010-01-30 23:52:35.000",
        "p_locationip": "41.188.54.178"
      }
    },
    "organisation-2941": {
      "id": "organisation-2941",
      "_instance_identities": {
        "o_organisationid": 2941
      },
      "object_type_id": "organisation",
      "object_type_name": "organisation",
      "display": "National_and_Kapodistrian_University_of_Athens",
      "properties": {
        "o_organisationid": 2941,
        "o_type": "university",
        "o_name": "National_and_Kapodistrian_University_of_Athens",
        "o_url": "http://dbpedia.org/resource/National_and_Kapodistrian_University_of_Athens"
      }
    },
    "place-1142": {
      "id": "place-1142",
      "_instance_identities": {
        "p_placeid": 1142
      },
      "object_type_id": "place",
      "object_type_name": "place",
      "display": "Athens",
      "properties": {
        "p_url": "http://dbpedia.org/resource/Athens",
        "p_type": "city",
        "p_placeid": 1142,
        "p_name": "Athens"
      }
    },
    "place-1": {
      "id": "place-1",
      "_instance_identities": {
        "p_placeid": 1
      },
      "object_type_id": "place",
      "object_type_name": "place",
      "display": "China",
      "properties": {
        "p_type": "country",
        "p_placeid": 1,
        "p_name": "China",
        "p_url": "http://dbpedia.org/resource/China"
      }
    }
  },
  "relation_paths": [
    {
      "relations": [
        {
          "relation_type_id": "comment_replyof_post",
          "relation_type_name": "comment_replyof_post",
          "source_object_id": "comment-32485",
          "target_object_id": "post-32484"
        },
        {
          "relation_type_id": "post_hastag_tag",
          "relation_type_name": "post_hastag_tag",
          "source_object_id": "post-32484",
          "target_object_id": "tag-6"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_replyof_post",
          "relation_type_name": "comment_replyof_post",
          "source_object_id": "comment-32486",
          "target_object_id": "post-32484"
        },
        {
          "relation_type_id": "post_hastag_tag",
          "relation_type_name": "post_hastag_tag",
          "source_object_id": "post-32484",
          "target_object_id": "tag-6"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32485",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_knows_person",
          "relation_type_name": "person_knows_person",
          "source_object_id": "person-143",
          "target_object_id": "person-318"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32485",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_knows_person",
          "relation_type_name": "person_knows_person",
          "source_object_id": "person-143",
          "target_object_id": "person-345"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32485",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_knows_person",
          "relation_type_name": "person_knows_person",
          "source_object_id": "person-143",
          "target_object_id": "person-555"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32485",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_knows_person",
          "relation_type_name": "person_knows_person",
          "source_object_id": "person-143",
          "target_object_id": "person-870"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32486",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_knows_person",
          "relation_type_name": "person_knows_person",
          "source_object_id": "person-143",
          "target_object_id": "person-318"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32486",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_knows_person",
          "relation_type_name": "person_knows_person",
          "source_object_id": "person-143",
          "target_object_id": "person-345"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32486",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_knows_person",
          "relation_type_name": "person_knows_person",
          "source_object_id": "person-143",
          "target_object_id": "person-555"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32486",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_knows_person",
          "relation_type_name": "person_knows_person",
          "source_object_id": "person-143",
          "target_object_id": "person-870"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_islocatedin_place",
          "relation_type_name": "comment_islocatedin_place",
          "source_object_id": "comment-32485",
          "target_object_id": "place-78"
        },
        {
          "relation_type_id": "place_ispartof_place",
          "relation_type_name": "place_ispartof_place",
          "source_object_id": "place-78",
          "target_object_id": "place-1456"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_islocatedin_place",
          "relation_type_name": "comment_islocatedin_place",
          "source_object_id": "comment-32486",
          "target_object_id": "place-78"
        },
        {
          "relation_type_id": "place_ispartof_place",
          "relation_type_name": "place_ispartof_place",
          "source_object_id": "place-78",
          "target_object_id": "place-1456"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32485",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_hasinterest_tag",
          "relation_type_name": "person_hasinterest_tag",
          "source_object_id": "person-143",
          "target_object_id": "tag-273"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32485",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_hasinterest_tag",
          "relation_type_name": "person_hasinterest_tag",
          "source_object_id": "person-143",
          "target_object_id": "tag-779"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32485",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_hasinterest_tag",
          "relation_type_name": "person_hasinterest_tag",
          "source_object_id": "person-143",
          "target_object_id": "tag-1024"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32485",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_hasinterest_tag",
          "relation_type_name": "person_hasinterest_tag",
          "source_object_id": "person-143",
          "target_object_id": "tag-1672"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32486",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_hasinterest_tag",
          "relation_type_name": "person_hasinterest_tag",
          "source_object_id": "person-143",
          "target_object_id": "tag-273"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32486",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_hasinterest_tag",
          "relation_type_name": "person_hasinterest_tag",
          "source_object_id": "person-143",
          "target_object_id": "tag-779"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32486",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_hasinterest_tag",
          "relation_type_name": "person_hasinterest_tag",
          "source_object_id": "person-143",
          "target_object_id": "tag-1024"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32486",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_hasinterest_tag",
          "relation_type_name": "person_hasinterest_tag",
          "source_object_id": "person-143",
          "target_object_id": "tag-1672"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32485",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_studyat_organisation",
          "relation_type_name": "person_studyat_organisation",
          "source_object_id": "person-143",
          "target_object_id": "organisation-2941"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32486",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_studyat_organisation",
          "relation_type_name": "person_studyat_organisation",
          "source_object_id": "person-143",
          "target_object_id": "organisation-2941"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32485",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_islocatedin_place",
          "relation_type_name": "person_islocatedin_place",
          "source_object_id": "person-143",
          "target_object_id": "place-1142"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32486",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_islocatedin_place",
          "relation_type_name": "person_islocatedin_place",
          "source_object_id": "person-143",
          "target_object_id": "place-1142"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_replyof_post",
          "relation_type_name": "comment_replyof_post",
          "source_object_id": "comment-32485",
          "target_object_id": "post-32484"
        },
        {
          "relation_type_id": "post_islocatedin_place",
          "relation_type_name": "post_islocatedin_place",
          "source_object_id": "post-32484",
          "target_object_id": "place-1"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_replyof_post",
          "relation_type_name": "comment_replyof_post",
          "source_object_id": "comment-32486",
          "target_object_id": "post-32484"
        },
        {
          "relation_type_id": "post_islocatedin_place",
          "relation_type_name": "post_islocatedin_place",
          "source_object_id": "post-32484",
          "target_object_id": "place-1"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32485",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_likes_post",
          "relation_type_name": "person_likes_post",
          "source_object_id": "person-143",
          "target_object_id": "post-3.43597402501e+11"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32485",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_likes_post",
          "relation_type_name": "person_likes_post",
          "source_object_id": "person-143",
          "target_object_id": "post-4.12316884425e+11"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32485",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_likes_post",
          "relation_type_name": "person_likes_post",
          "source_object_id": "person-143",
          "target_object_id": "post-4.81036361318e+11"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32485",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_likes_post",
          "relation_type_name": "person_likes_post",
          "source_object_id": "person-143",
          "target_object_id": "post-6.87194784952e+11"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32486",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_likes_post",
          "relation_type_name": "person_likes_post",
          "source_object_id": "person-143",
          "target_object_id": "post-3.43597402501e+11"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32486",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_likes_post",
          "relation_type_name": "person_likes_post",
          "source_object_id": "person-143",
          "target_object_id": "post-4.12316884425e+11"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32486",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_likes_post",
          "relation_type_name": "person_likes_post",
          "source_object_id": "person-143",
          "target_object_id": "post-4.81036361318e+11"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32486",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_likes_post",
          "relation_type_name": "person_likes_post",
          "source_object_id": "person-143",
          "target_object_id": "post-6.87194784952e+11"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_replyof_post",
          "relation_type_name": "comment_replyof_post",
          "source_object_id": "comment-32485",
          "target_object_id": "post-32484"
        },
        {
          "relation_type_id": "post_hascreator_person",
          "relation_type_name": "post_hascreator_person",
          "source_object_id": "post-32484",
          "target_object_id": "person-555"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_replyof_post",
          "relation_type_name": "comment_replyof_post",
          "source_object_id": "comment-32486",
          "target_object_id": "post-32484"
        },
        {
          "relation_type_id": "post_hascreator_person",
          "relation_type_name": "post_hascreator_person",
          "source_object_id": "post-32484",
          "target_object_id": "person-555"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32485",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_likes_comment",
          "relation_type_name": "person_likes_comment",
          "source_object_id": "person-143",
          "target_object_id": "comment-2.06158448803e+11"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32485",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_likes_comment",
          "relation_type_name": "person_likes_comment",
          "source_object_id": "person-143",
          "target_object_id": "comment-3.43597407147e+11"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32485",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_likes_comment",
          "relation_type_name": "person_likes_comment",
          "source_object_id": "person-143",
          "target_object_id": "comment-7.55914262659e+11"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32485",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_likes_comment",
          "relation_type_name": "person_likes_comment",
          "source_object_id": "person-143",
          "target_object_id": "comment-1.030792168521e+12"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32486",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_likes_comment",
          "relation_type_name": "person_likes_comment",
          "source_object_id": "person-143",
          "target_object_id": "comment-2.06158448803e+11"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32486",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_likes_comment",
          "relation_type_name": "person_likes_comment",
          "source_object_id": "person-143",
          "target_object_id": "comment-3.43597407147e+11"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32486",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_likes_comment",
          "relation_type_name": "person_likes_comment",
          "source_object_id": "person-143",
          "target_object_id": "comment-7.55914262659e+11"
        }
      ],
      "length": 2
    },
    {
      "relations": [
        {
          "relation_type_id": "comment_hascreator_person",
          "relation_type_name": "comment_hascreator_person",
          "source_object_id": "comment-32486",
          "target_object_id": "person-143"
        },
        {
          "relation_type_id": "person_likes_comment",
          "relation_type_name": "person_likes_comment",
          "source_object_id": "person-143",
          "target_object_id": "comment-1.030792168521e+12"
        }
      ],
      "length": 2
    }
  ],
  "total_count": 151043,
  "search_after": [
    "20251111_011029_00002_5pjz5",
    "xf97a0a1dcbab4f26b1944122eed84b98",
    "2"
  ],
  "current_path_number": 44,
  "overall_ms": 4204
}
```

```json
{
  "entries": [
    {
      "objects": {
        "comment-32487": {
          "id": "comment-32487",
          "_instance_identities": {
            "c_commentid": 32487
          },
          "object_type_id": "comment",
          "object_type_name": "comment",
          "display": "thanks",
          "properties": {
            "c_creationdate": "2010-01-23 07:15:07.000",
            "c_locationip": "62.217.119.183",
            "c_browserused": "Firefox",
            "c_content": "thanks",
            "c_length": 6,
            "c_commentid": 32487
          }
        },
        "comment-32489": {
          "id": "comment-32489",
          "_instance_identities": {
            "c_commentid": 32489
          },
          "object_type_id": "comment",
          "object_type_name": "comment",
          "display": "About Johnny Depp, . Depp has gained acclaim for his portrayals of people such as Ed Woo",
          "properties": {
            "c_commentid": 32489,
            "c_creationdate": "2010-01-23 08:17:35.000",
            "c_locationip": "62.217.119.183",
            "c_browserused": "Firefox",
            "c_content": "About Johnny Depp, . Depp has gained acclaim for his portrayals of people such as Ed Woo",
            "c_length": 88
          }
        },
        "comment-32490": {
          "id": "comment-32490",
          "_instance_identities": {
            "c_commentid": 32490
          },
          "object_type_id": "comment",
          "object_type_name": "comment",
          "display": "right",
          "properties": {
            "c_browserused": "Firefox",
            "c_content": "right",
            "c_length": 5,
            "c_commentid": 32490,
            "c_creationdate": "2010-01-23 17:39:22.000",
            "c_locationip": "62.217.119.183"
          }
        },
        "comment-32492": {
          "id": "comment-32492",
          "_instance_identities": {
            "c_commentid": 32492
          },
          "object_type_id": "comment",
          "object_type_name": "comment",
          "display": "About Gold Cobra, which featured the single Shotgun and received mixed to p",
          "properties": {
            "c_commentid": 32492,
            "c_creationdate": "2010-01-23 08:17:45.000",
            "c_locationip": "62.217.119.183",
            "c_browserused": "Firefox",
            "c_content": "About Gold Cobra, which featured the single Shotgun and received mixed to p",
            "c_length": 75
          }
        },
        "comment-32486": {
          "id": "comment-32486",
          "_instance_identities": {
            "c_commentid": 32486
          },
          "object_type_id": "comment",
          "object_type_name": "comment",
          "display": "ok",
          "properties": {
            "c_commentid": 32486,
            "c_creationdate": "2010-01-23 08:14:18.000",
            "c_locationip": "62.217.119.183",
            "c_browserused": "Firefox",
            "c_content": "ok",
            "c_length": 2
          }
        },
        "comment-32485": {
          "id": "comment-32485",
          "_instance_identities": {
            "c_commentid": 32485
          },
          "object_type_id": "comment",
          "object_type_name": "comment",
          "display": "great",
          "properties": {
            "c_commentid": 32485,
            "c_creationdate": "2010-01-23 06:20:25.000",
            "c_locationip": "62.217.119.183",
            "c_browserused": "Firefox",
            "c_content": "great",
            "c_length": 5
          }
        },
        "comment-32488": {
          "id": "comment-32488",
          "_instance_identities": {
            "c_commentid": 32488
          },
          "object_type_id": "comment",
          "object_type_name": "comment",
          "display": "About Charlton Heston,  later supported conservative Republican policies and w",
          "properties": {
            "c_content": "About Charlton Heston,  later supported conservative Republican policies and w",
            "c_length": 78,
            "c_commentid": 32488,
            "c_creationdate": "2010-01-23 07:05:10.000",
            "c_locationip": "62.217.119.183",
            "c_browserused": "Firefox"
          }
        },
        "comment-32491": {
          "id": "comment-32491",
          "_instance_identities": {
            "c_commentid": 32491
          },
          "object_type_id": "comment",
          "object_type_name": "comment",
          "display": "great",
          "properties": {
            "c_content": "great",
            "c_length": 5,
            "c_commentid": 32491,
            "c_creationdate": "2010-01-23 11:50:25.000",
            "c_locationip": "175.103.89.192",
            "c_browserused": "Firefox"
          }
        },
        "comment-32494": {
          "id": "comment-32494",
          "_instance_identities": {
            "c_commentid": 32494
          },
          "object_type_id": "comment",
          "object_type_name": "comment",
          "display": "right",
          "properties": {
            "c_browserused": "Firefox",
            "c_content": "right",
            "c_length": 5,
            "c_commentid": 32494,
            "c_creationdate": "2010-01-23 07:06:10.000",
            "c_locationip": "62.217.119.183"
          }
        },
        "comment-32493": {
          "id": "comment-32493",
          "_instance_identities": {
            "c_commentid": 32493
          },
          "object_type_id": "comment",
          "object_type_name": "comment",
          "display": "About Gold Cobra,  full original lineup since 2000's Chocolate StarfisAbout 12 X 5, ure R&B covers; how",
          "properties": {
            "c_locationip": "62.217.119.183",
            "c_browserused": "Firefox",
            "c_content": "About Gold Cobra,  full original lineup since 2000's Chocolate StarfisAbout 12 X 5, ure R&B covers; how",
            "c_length": 103,
            "c_commentid": 32493,
            "c_creationdate": "2010-01-23 11:17:59.000"
          }
        }
      },
      "relation_paths": [
        {
          "relations": [
            {
              "relation_type_id": "comment_replyof_comment",
              "relation_type_name": "comment_replyof_comment",
              "source_object_id": "comment-32493",
              "target_object_id": "comment-32492"
            },
            {
              "relation_type_id": "comment_replyof_comment",
              "relation_type_name": "comment_replyof_comment",
              "source_object_id": "comment-32492",
              "target_object_id": "comment-32489"
            }
          ],
          "length": 2
        }
      ],
      "total_count": 58415,
      "search_after": [
        "20251111_011249_00079_5pjz5",
        "xec0f2bb02b7a46e99548d56096dad813",
        "1"
      ],
      "current_path_number": 1,
      "overall_ms": 0
    }
  ]
}
```

<h3 id="子图查询-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|对象子图|Inline|

<h3 id="子图查询-responseschema">Response Schema</h3>

#### Enumerated Values

|Property|Value|
|---|---|
|property_type|metric|
|operation|in|
|operation|=|
|operation|!=|
|operation|range|
|operation|out_range|
|operation|like|
|operation|not_like|
|operation|>|
|operation|>=|
|operation|<|
|operation|<=|
|property_type|metric|

<aside class="success">
This operation does not require authentication
</aside>

## 基于一组对象实例组织关系子图

`POST /api/ontology-query/v1/knowledge-networks/{kn_id}/subgraph/objects`

给定一组对象实例，按概念定义的关系，组织这些对象实例的关系子图。

> Body parameter

```json
{
  "entries": [
    {
      "object_type_id": "comment",
      "_instance_identity": {
        "c_commentid": 206158448803
      }
    },
    {
      "object_type_id": "comment",
      "_instance_identity": {
        "c_commentid": 755914262659
      }
    },
    {
      "object_type_id": "post",
      "_instance_identity": {
        "ps_postid": 481036361318
      }
    }
  ]
}
```

<h3 id="基于一组对象实例组织关系子图-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|branch|query|string|false|分支名称|
|include_type_info|query|boolean|false|是否包含对象类信息，默认false，不包含|
|include_logic_params|query|boolean|false|包含逻辑属性的计算参数，默认false，返回结果不包含逻辑属性的字段和值|
|ignoring_store_cache|query|boolean|false|是否忽略索引查询，默认是false，不忽略，即走索引查询|
|exclude_system_properties|query|array[string]|false|需要排除的系统字段列表。可选值：_instance_id（实例ID）、_instance_identity（实例唯一标识）、_display（显示值）。如果指定了某个字段，则在返回的对象数据中不包含该字段。可以传递多个值，例如：?exclude_system_properties=_instance_id&exclude_system_properties=_display|
|body|body|[SubGraphQueryBaseOnObjects](#schemasubgraphquerybaseonobjects)|true|基于一组对象实例探索关系子图的请求体|

#### Enumerated Values

|Parameter|Value|
|---|---|
|exclude_system_properties|_instance_id|
|exclude_system_properties|_instance_identity|
|exclude_system_properties|_display|

> Example responses

> 成功返回关系子图

```json
{
  "objects": {
    "comment-2.06158448803e+11": {
      "_instance_id": "comment-2.06158448803e+11",
      "_instance_identity": {
        "c_commentid": 206158448803
      },
      "object_type_id": "comment",
      "object_type_name": "comment",
      "_display": "About Titian, stic manner cAbout Leona Lewis...",
      "properties": {
        "c_commentid": 206158448803,
        "c_creationdate": "2010-07-24 00:04:01.000",
        "c_locationip": "186.169.83.109",
        "c_browserused": "Firefox",
        "c_content": "About Titian, stic manner cAbout Leona Lewis...",
        "c_length": 111
      }
    },
    "comment-7.55914262659e+11": {
      "_instance_id": "comment-7.55914262659e+11",
      "_instance_identity": {
        "c_commentid": 755914262659
      },
      "object_type_id": "comment",
      "object_type_name": "comment",
      "_display": "Original of the Species is a song by rock band U2...",
      "properties": {
        "c_commentid": 755914262659,
        "c_creationdate": "2011-12-12 12:04:24.000",
        "c_locationip": "186.169.83.109",
        "c_browserused": "Firefox",
        "c_content": "Original of the Species is a song by rock band U2...",
        "c_length": 151
      }
    }
  },
  "isolated_objects": {
    "post-4.81036361318e+11": {
      "_instance_id": "post-4.81036361318e+11",
      "_instance_identity": {
        "ps_postid": 481036361318
      },
      "object_type_id": "post",
      "object_type_name": "post",
      "_display": "",
      "properties": {
        "ps_postid": 481036361318,
        "ps_creationdate": "2011-04-27 04:12:24.000",
        "ps_locationip": "31.216.154.26",
        "ps_browserused": "Chrome",
        "ps_language": "",
        "ps_content": "",
        "ps_length": 0
      }
    }
  },
  "relation_paths": [
    {
      "relations": [
        {
          "relation_type_id": "comment_replyof_comment",
          "relation_type_name": "comment_replyof_comment",
          "source_object_id": "comment-2.06158448803e+11",
          "target_object_id": "comment-7.55914262659e+11"
        }
      ],
      "length": 1
    }
  ],
  "overall_ms": 150
}
```

<h3 id="基于一组对象实例组织关系子图-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|成功返回关系子图|[ObjectSubGraphResponse](#schemaobjectsubgraphresponse)|

<aside class="success">
This operation does not require authentication
</aside>

## 对象属性值查询

`POST /api/ontology-query/v1/knowledge-networks/{kn_id}/object-types/{ot_id}/properties`

> Body parameter

```json
{
  "_instance_identities": [
    {
      "id": 1479,
      "pod_ip": "192.169.37.60"
    },
    {
      "id": 1480,
      "pod_ip": "192.169.37.72"
    }
  ],
  "properties": [
    "pod_name",
    "pod_status",
    "metric_test",
    "a_action"
  ],
  "dynamic_params": {
    "metric_test": {
      "start": 1731460342241,
      "end": 1762996342241,
      "instant": false,
      "step": "month",
      "analysis_dimensions": [
        "pod_name",
        "pod_node_name"
      ],
      "order_by_fields": [
        {
          "name": "__value",
          "type": "number",
          "direction": "desc"
        }
      ],
      "having_condition": {
        "field": "__value",
        "operation": ">",
        "value": 100
      },
      "metrics": {
        "type": "sameperiod",
        "sameperiod_config": {
          "method": [
            "growth_rate"
          ],
          "offset": 1,
          "time_granularity": "day"
        }
      }
    },
    "a_action": {
      "Authorization": "aasssdffgg"
    }
  }
}
```

<h3 id="对象属性值查询-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|ot_id|path|string|true|对象类ID|
|branch|query|string|false|分支名称，默认 main|
|include_type_info|query|boolean|false|是否包含对象类信息，默认 false|
|exclude_system_properties|query|array[string]|false|需要排除的系统字段列表。可选值：_instance_id（实例ID）、_instance_identity（实例唯一标识）、_display（显示值）。如果指定了某个字段，则在返回的对象数据中不包含该字段。可以传递多个值，例如：?exclude_system_properties=_instance_id&exclude_system_properties=_display|
|x-http-method-override|header|string|true|重载 post，实际上是 get 方法|
|body|body|[PropertyQueryBody](#schemapropertyquerybody)|true|属性查询请求体|

#### Enumerated Values

|Parameter|Value|
|---|---|
|exclude_system_properties|_instance_id|
|exclude_system_properties|_instance_identity|
|exclude_system_properties|_display|
|x-http-method-override|GET|

> Example responses

> ok

```json
{
  "datas": [
    {
      "id": 1479,
      "pod_status": "Succeeded",
      "pod_name": "communication-config-679xd",
      "pod_ip": "192.169.37.60",
      "metric_test": {
        "model": {
          "unit_type": "numUnit",
          "unit": "none"
        },
        "datas": [
          {
            "labels": {
              "pod_name": "ar-k8s-event-executor-6ffcc7884c-9wgsb",
              "pod_node_name": "node-111-175",
              "pod_ip": "192.169.37.60"
            },
            "times": [
              1730390400000,
              1732982400000,
              1735660800000,
              1738339200000,
              1740758400000,
              1743436800000,
              1746028800000,
              1748707200000,
              1751299200000,
              1753977600000,
              1756656000000,
              1759248000000,
              1761926400000
            ],
            "values": [
              null,
              null,
              null,
              null,
              null,
              null,
              null,
              1,
              null,
              null,
              null,
              null,
              null
            ]
          },
          {
            "labels": {
              "pod_name": "communication-config-679xd",
              "pod_node_name": "node-111-175",
              "pod_ip": "192.169.37.60"
            },
            "times": [
              1730390400000,
              1732982400000,
              1735660800000,
              1738339200000,
              1740758400000,
              1743436800000,
              1746028800000,
              1748707200000,
              1751299200000,
              1753977600000,
              1756656000000,
              1759248000000,
              1761926400000
            ],
            "values": [
              null,
              null,
              null,
              null,
              null,
              null,
              null,
              1,
              null,
              null,
              null,
              null,
              null
            ]
          }
        ],
        "step": "month",
        "is_variable": false,
        "is_calendar": false
      },
      "a_action": {}
    },
    {
      "id": 1480,
      "metric_test": {
        "model": {
          "unit_type": "numUnit",
          "unit": "none"
        },
        "datas": [
          {
            "labels": {
              "pod_ip": "192.169.37.72",
              "pod_name": "content-data-lake-dm-pre-verify-gr2gj",
              "pod_node_name": "node-111-175"
            },
            "times": [
              1730390400000,
              1732982400000,
              1735660800000,
              1738339200000,
              1740758400000,
              1743436800000,
              1746028800000,
              1748707200000,
              1751299200000,
              1753977600000,
              1756656000000,
              1759248000000,
              1761926400000
            ],
            "values": [
              null,
              null,
              null,
              null,
              null,
              null,
              null,
              1,
              null,
              null,
              null,
              null,
              null
            ]
          }
        ],
        "step": "month",
        "is_variable": false,
        "is_calendar": false
      },
      "pod_name": "content-data-lake-dm-pre-verify-gr2gj",
      "pod_ip": "192.169.37.72",
      "pod_status": "Succeeded",
      "a_action": {}
    }
  ],
  "overall_ms": 2588
}
```

<h3 id="对象属性值查询-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[ObjectPropertiesValuesResponse](#schemaobjectpropertiesvaluesresponse)|

<aside class="success">
This operation does not require authentication
</aside>

## 执行行动类

`POST /api/ontology-query/v1/knowledge-networks/{kn_id}/action-types/{at_id}/execute`

> Body parameter

```json
{
  "_instance_identities": [
    {
      "pod_ip": "192.168.1.1",
      "id": 1
    },
    {
      "pod_ip": "192.168.1.2",
      "id": 2
    }
  ],
  "dynamic_params": {
    "Authorization": "Bearer xxx"
  }
}
```

<h3 id="执行行动类-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|at_id|path|string|true|行动类ID|
|branch|query|string|false|分支名称，默认 main|
|body|body|[ActionExecutionRequest](#schemaactionexecutionrequest)|true|none|

> Example responses

> accepted

```json
{
  "execution_id": "cqq2g8h4d2fg00fvm8dg",
  "status": "pending",
  "message": "Action execution started",
  "created_at": 1704067200000
}
```

<h3 id="执行行动类-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|202|[Accepted](https://tools.ietf.org/html/rfc7231#section-6.3.3)|accepted|[ActionExecutionResponse](#schemaactionexecutionresponse)|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数错误|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|行动类不存在|None|

<h3 id="执行行动类-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 获取行动执行状态

`GET /api/ontology-query/v1/knowledge-networks/{kn_id}/action-executions/{execution_id}`

<h3 id="获取行动执行状态-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|execution_id|path|string|true|执行ID|

> Example responses

> ok

```json
{
  "id": "cqq2g8h4d2fg00fvm8dg",
  "kn_id": "kn_xxx",
  "action_type_id": "at_xxx",
  "action_type_name": "restart_pod",
  "action_source_type": "tool",
  "object_type_id": "ot_xxx",
  "trigger_type": "manual",
  "status": "completed",
  "total_count": 2,
  "success_count": 1,
  "failed_count": 1,
  "results": [
    {
      "instance_identity": {
        "pod_ip": "192.168.1.1",
        "id": 1
      },
      "status": "success",
      "parameters": {
        "pod_ip": "192.168.1.1"
      },
      "result": {
        "message": "Pod restarted successfully"
      },
      "duration_ms": 1200
    },
    {
      "instance_identity": {
        "pod_ip": "192.168.1.2",
        "id": 2
      },
      "status": "failed",
      "parameters": {
        "pod_ip": "192.168.1.2"
      },
      "error_message": "Connection timeout",
      "duration_ms": 5000
    }
  ],
  "executor_id": "user_xxx",
  "executor": {
    "id": "user_xxx",
    "type": "user",
    "name": "张三"
  },
  "start_time": 1704067200000,
  "end_time": 1704067206200,
  "duration_ms": 6200
}
```

<h3 id="获取行动执行状态-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[ActionExecution](#schemaactionexecution)|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|执行记录不存在|None|

<h3 id="获取行动执行状态-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 查询行动执行日志

`GET /api/ontology-query/v1/knowledge-networks/{kn_id}/action-logs`

<h3 id="查询行动执行日志-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|action_type_id|query|string|false|行动类ID（可选）|
|status|query|string|false|执行状态（可选）|
|trigger_type|query|string|false|触发类型（可选）|
|start_time_from|query|integer(int64)|false|开始时间范围-起始（毫秒时间戳，可选）|
|start_time_to|query|integer(int64)|false|开始时间范围-结束（毫秒时间戳，可选）|
|limit|query|integer|false|返回数量限制，默认20，最大1000|
|need_total|query|boolean|false|是否需要返回总数|
|search_after|query|string|false|分页游标（逗号分隔字符串，如 "1704067200000,cqq2g8h4d2fg00fvm8dg"）|

#### Enumerated Values

|Parameter|Value|
|---|---|
|status|pending|
|status|running|
|status|completed|
|status|failed|
|status|cancelled|
|trigger_type|manual|
|trigger_type|scheduled|

> Example responses

> ok

```json
{
  "entries": [
    {
      "id": "cqq2g8h4d2fg00fvm8dg",
      "action_type_id": "at_xxx",
      "action_type_name": "restart_pod",
      "status": "completed",
      "trigger_type": "manual",
      "total_count": 2,
      "success_count": 2,
      "failed_count": 0,
      "start_time": 1704067200000,
      "duration_ms": 6200
    }
  ],
  "total_count": 100,
  "search_after": [
    1704067200000,
    "cqq2g8h4d2fg00fvm8dg"
  ]
}
```

<h3 id="查询行动执行日志-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[ActionExecutionList](#schemaactionexecutionlist)|

<aside class="success">
This operation does not require authentication
</aside>

## 获取单条行动执行日志

`GET /api/ontology-query/v1/knowledge-networks/{kn_id}/action-logs/{log_id}`

<h3 id="获取单条行动执行日志-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|log_id|path|string|true|日志ID (即执行ID)|
|results_limit|query|integer|false|results 分页大小，默认100，最大1000|
|results_offset|query|integer|false|results 偏移量，默认0|
|results_status|query|string|false|按 results 中的状态过滤（可选）|

#### Enumerated Values

|Parameter|Value|
|---|---|
|results_status|success|
|results_status|failed|

> Example responses

> 200 Response

```json
{
  "id": "string",
  "kn_id": "string",
  "action_type_id": "string",
  "action_type_name": "string",
  "action_source_type": "tool",
  "object_type_id": "string",
  "trigger_type": "manual",
  "status": "pending",
  "total_count": 0,
  "success_count": 0,
  "failed_count": 0,
  "results": [
    {
      "instance_identity": {},
      "status": "pending",
      "parameters": {},
      "result": {},
      "error_message": "string",
      "duration_ms": 0
    }
  ],
  "results_total": 0,
  "results_offset": 0,
  "results_limit": 0,
  "dynamic_params": {},
  "executor_id": "string",
  "executor": {
    "id": "string",
    "type": "string",
    "name": "string"
  },
  "start_time": 0,
  "end_time": 0,
  "duration_ms": 0,
  "action_type_snapshot": {}
}
```

<h3 id="获取单条行动执行日志-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[ActionExecution](#schemaactionexecution)|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|日志不存在|None|

<h3 id="获取单条行动执行日志-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 取消正在执行的任务

`POST /api/ontology-query/v1/knowledge-networks/{kn_id}/action-logs/{log_id}/cancel`

> Body parameter

```json
{
  "reason": "string"
}
```

<h3 id="取消正在执行的任务-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|log_id|path|string|true|日志ID (即执行ID)|
|body|body|[CancelExecutionRequest](#schemacancelexecutionrequest)|false|none|

> Example responses

> ok

```json
{
  "execution_id": "cqq2g8h4d2fg00fvm8dg",
  "status": "cancelled",
  "message": "任务已取消",
  "cancelled_count": 5,
  "completed_count": 10
}
```

<h3 id="取消正在执行的任务-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[CancelExecutionResponse](#schemacancelexecutionresponse)|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|任务已完成或已取消，无法取消|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|执行记录不存在|None|

<h3 id="取消正在执行的任务-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 查询指标数据（BKN 原生指标）

`POST /api/ontology-query/v1/knowledge-networks/{kn_id}/metrics/{metric_id}/data`

> Body parameter

```json
{
  "time": {
    "start": 0,
    "end": 0,
    "instant": true,
    "step": "string"
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
  "analysis_dimensions": [
    "string"
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
  },
  "metrics": {
    "type": "sameperiod",
    "sameperiod_config": {
      "method": [
        "growth_value",
        "growth_rate"
      ],
      "offset": 0,
      "time_granularity": "day"
    }
  },
  "limit": 1
}
```

<h3 id="查询指标数据（bkn-原生指标）-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|业务知识网络ID|
|metric_id|path|string|true|指标 ID（BKN MetricDefinition.id）|
|branch|query|string|false|分支，默认 main|
|fill_null|query|boolean|false|趋势（区间）查询时，将各序列值对齐到 [time.start, time.end] 的完整分桶时间轴，缺失分桶以 null 填充；与 mdl-uniquery 指标数据查询的 fill_null 一致，为 URL 查询参数（非 body）。仅当 instant 为 false 或省略时有效。|
|body|body|[MetricQueryRequestBody](#schemametricqueryrequestbody)|true|none|

> Example responses

> 200 Response

```json
{
  "model": {
    "unit_type": "numUnit",
    "unit": "none"
  },
  "datas": [
    {
      "labels": {
        "property1": "string",
        "property2": "string"
      },
      "times": [
        null
      ],
      "values": [
        null
      ],
      "growth_values": [
        null
      ],
      "growth_rates": [
        null
      ],
      "proportions": [
        null
      ]
    }
  ],
  "step": "string",
  "is_variable": true,
  "is_calendar": true
}
```

<h3 id="查询指标数据（bkn-原生指标）-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[MetricData](#schemametricdata)|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|bad request|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|指标或对象类不存在|None|

<h3 id="查询指标数据（bkn-原生指标）-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 指标试算（不落库）

`POST /api/ontology-query/v1/knowledge-networks/{kn_id}/metrics/dry-run`

> Body parameter

```json
{
  "metric_config": {
    "id": "string",
    "kn_id": "string",
    "branch": "string",
    "name": "string",
    "comment": "string",
    "tags": [
      "string"
    ],
    "unit_type": "numUnit",
    "unit": "none",
    "metric_type": "string",
    "scope_type": "string",
    "scope_ref": "string",
    "time_dimension": {},
    "calculation_formula": {},
    "analysis_dimensions": [
      {}
    ]
  },
  "time": {},
  "condition": {
    "operation": "and",
    "sub_conditions": [
      {
        "operation": "and",
        "sub_conditions": []
      }
    ]
  },
  "analysis_dimensions": [
    "string"
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
  },
  "metrics": {
    "type": "sameperiod",
    "sameperiod_config": {
      "method": [
        "growth_value",
        "growth_rate"
      ],
      "offset": 0,
      "time_granularity": "day"
    }
  },
  "limit": 1
}
```

<h3 id="指标试算（不落库）-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|kn_id|path|string|true|none|
|branch|query|string|false|none|
|fill_null|query|boolean|false|与「查询指标数据」接口相同；URL 查询参数，非 body。|
|body|body|[MetricDryRun](#schemametricdryrun)|true|none|

> Example responses

> 200 Response

```json
{
  "model": {
    "unit_type": "numUnit",
    "unit": "none"
  },
  "datas": [
    {
      "labels": {
        "property1": "string",
        "property2": "string"
      },
      "times": [
        null
      ],
      "values": [
        null
      ],
      "growth_values": [
        null
      ],
      "growth_rates": [
        null
      ],
      "proportions": [
        null
      ]
    }
  ],
  "step": "string",
  "is_variable": true,
  "is_calendar": true
}
```

<h3 id="指标试算（不落库）-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[MetricData](#schemametricdata)|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|bad request|None|

<h3 id="指标试算（不落库）-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

# Schemas

<h2 id="tocS_Object">Object</h2>
<!-- backwards compatibility -->
<a id="schemaobject"></a>
<a id="schema_Object"></a>
<a id="tocSobject"></a>
<a id="tocsobject"></a>

```json
{}

```

对象的json，字段不定，随对象实例动态变化

### Properties

*None*

<h2 id="tocS_ObjectTypeNode">ObjectTypeNode</h2>
<!-- backwards compatibility -->
<a id="schemaobjecttypenode"></a>
<a id="schema_ObjectTypeNode"></a>
<a id="tocSobjecttypenode"></a>
<a id="tocsobjecttypenode"></a>

```json
{
  "id": "string",
  "name": "string",
  "condition": {
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

节点（对象类）信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|节点(对象类)ID|
|name|string|true|none|节点（对象类）名称|
|condition|[Condition](#schemacondition)|false|none|过滤条件。当在当前节点上有过滤条件时才需要填入。|

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

排序字段

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

<h2 id="tocS_ObjectQueryBaseOnObjectType">ObjectQueryBaseOnObjectType</h2>
<!-- backwards compatibility -->
<a id="schemaobjectquerybaseonobjecttype"></a>
<a id="schema_ObjectQueryBaseOnObjectType"></a>
<a id="tocSobjectquerybaseonobjecttype"></a>
<a id="tocsobjectquerybaseonobjecttype"></a>

```json
{
  "condition": {
    "operation": "and",
    "sub_conditions": [
      {
        "operation": "and",
        "sub_conditions": []
      }
    ]
  },
  "sort": {
    "field": "string",
    "direction": "desc"
  },
  "limit": 0,
  "need_total": true
}

```

对象实例查询请求体

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|condition|[Condition](#schemacondition)|false|none|过滤条件|
|sort|[Sort](#schemasort)|true|none|排序字段，默认使用 @timestamp排序，排序方向为 desc|
|limit|integer|true|none|返回的数量，默认值 10。范围 1-10000|
|need_total|boolean|false|none|是否需要总数，默认false|

<h2 id="tocS_FirstQueryWithSearchAfter">FirstQueryWithSearchAfter</h2>
<!-- backwards compatibility -->
<a id="schemafirstquerywithsearchafter"></a>
<a id="schema_FirstQueryWithSearchAfter"></a>
<a id="tocSfirstquerywithsearchafter"></a>
<a id="tocsfirstquerywithsearchafter"></a>

```json
{
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
  "properties": [
    "string"
  ]
}

```

分页查询的第一次查询请求

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|condition|[Condition](#schemacondition)|false|none|过滤条件|
|sort|[[Sort](#schemasort)]|false|none|排序字段，默认使用 _score 倒序，主键字段正序|
|limit|integer|true|none|返回的数量，默认值 10。范围 1-10000|
|need_total|boolean|false|none|是否需要总数，默认false|
|properties|[string]|false|none|指定需要输出的属性集。默认为全部数据属性|

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

<h2 id="tocS_MetricProperty">MetricProperty</h2>
<!-- backwards compatibility -->
<a id="schemametricproperty"></a>
<a id="schema_MetricProperty"></a>
<a id="tocSmetricproperty"></a>
<a id="tocsmetricproperty"></a>

```json
{
  "property_type": "metric",
  "mapping_source_id": "string",
  "has_any_unfilled_params": true,
  "parameters": {
    "Filters": [
      {
        "name": "string",
        "value": [
          null
        ],
        "operation": "in"
      }
    ]
  },
  "dynamic_params": {
    "instant": true,
    "start": 0,
    "end": 0
  }
}

```

指标属性

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|property_type|string|true|none|属性类型|
|mapping_source_id|string|true|none|映射的指标模型id|
|has_any_unfilled_params|boolean|true|none|是否有未填充的参数，默认是false|
|parameters|[MetricFilters](#schemametricfilters)|true|none|实例化了的参数|
|dynamic_params|[MetricRequiredParams](#schemametricrequiredparams)|true|none|动态参数|

#### Enumerated Values

|Property|Value|
|---|---|
|property_type|metric|

<h2 id="tocS_MetricFilters">MetricFilters</h2>
<!-- backwards compatibility -->
<a id="schemametricfilters"></a>
<a id="schema_MetricFilters"></a>
<a id="tocSmetricfilters"></a>
<a id="tocsmetricfilters"></a>

```json
{
  "Filters": [
    {
      "name": "string",
      "value": [
        null
      ],
      "operation": "in"
    }
  ]
}

```

指标模型属性的计算参数

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|Filters|[[Filter](#schemafilter)]|true|none|指标模型过滤条件|

<h2 id="tocS_Filter">Filter</h2>
<!-- backwards compatibility -->
<a id="schemafilter"></a>
<a id="schema_Filter"></a>
<a id="tocSfilter"></a>
<a id="tocsfilter"></a>

```json
{
  "name": "string",
  "value": [
    null
  ],
  "operation": "in"
}

```

过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|过滤字段名称|
|value|[any]|true|none|过滤的值。当 operation 是 in 时，value 为任意基本类型的数组，且长度大于等于1；当 operation 是 = 或 != 时，value 为任意基本类型的值；当 operation 是 range 时，value 是个由范围的下边界和上边界组成的长度为 2 的数值型数组。当 operation 是 out_range 时, value 是一个长度为 2 的数组，对应过滤条件时是 字段 < value[0] || 字段 >= value[1]|
|operation|string|true|none|操作符。|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|in|
|operation|=|
|operation|!=|
|operation|range|
|operation|out_range|
|operation|like|
|operation|not_like|
|operation|>|
|operation|>=|
|operation|<|
|operation|<=|

<h2 id="tocS_MetricRequiredParams">MetricRequiredParams</h2>
<!-- backwards compatibility -->
<a id="schemametricrequiredparams"></a>
<a id="schema_MetricRequiredParams"></a>
<a id="tocSmetricrequiredparams"></a>
<a id="tocsmetricrequiredparams"></a>

```json
{
  "instant": true,
  "start": 0,
  "end": 0
}

```

指标模型查询的动态参数

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|instant|boolean|true|none|是否即时查询。默认为false，非即时查询，即为趋势查询|
|start|integer|true|none|开始时间戳|
|end|integer|true|none|结束时间戳|

<h2 id="tocS_MetricPropertyDynamicParams">MetricPropertyDynamicParams</h2>
<!-- backwards compatibility -->
<a id="schemametricpropertydynamicparams"></a>
<a id="schema_MetricPropertyDynamicParams"></a>
<a id="tocSmetricpropertydynamicparams"></a>
<a id="tocsmetricpropertydynamicparams"></a>

```json
{
  "start": 0,
  "end": 0,
  "instant": true,
  "step": "string",
  "analysis_dimensions": [
    "string"
  ],
  "order_by_fields": [
    {
      "name": "string",
      "direction": "asc"
    }
  ],
  "having_condition": {
    "field": "__value",
    "operation": "==",
    "value": null
  },
  "metrics": {
    "type": "sameperiod",
    "sameperiod_config": {
      "method": [
        "growth_value",
        "growth_rate"
      ],
      "offset": 0,
      "time_granularity": "day"
    }
  },
  "property1": "string",
  "property2": "string"
}

```

指标属性的 dynamic_params 结构。当属性是指标属性时，dynamic_params 应遵循此 schema。
包含时间参数、分析参数以及指标属性配置中定义的其他动态参数。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|**additionalProperties**|any|false|none|none|

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|string|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|number|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|boolean|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|integer|false|none|none|

continued

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|start|integer(int64)|false|none|指标查询的开始时间。start=< unix_timestamp >，单位到毫秒。例如: 1646360670123|
|end|integer(int64)|false|none|指标查询的结束时间。end=< unix_timestamp >，单位到毫秒。例如: 1646471470123|
|instant|boolean|false|none|是否是即时查询。可选，默认为 false。当 instant = true 时，表示即时查询；当 instant = false 时，表示范围查询。|
|step|string|false|none|范围查询的步长。当 instant 为 false 时，建议提供。step=< time_durations >，用一个数字，后面跟时间单位来定义。<br>若不填单位，则默认按秒计算。时间单位可以是如下之一：ms - 毫秒；s - 秒；m - 分钟；h - 小时；d - 天；w - 周；y - 年；|
|analysis_dimensions|[string]|false|none|分析维度。即在指标查询时可指定的下钻维度集合|
|order_by_fields|[[OrderField](#schemaorderfield)]|false|none|排序字段。即在指标查询时可指定的排序字段集合|
|having_condition|[HavingCondition](#schemahavingcondition)|false|none|having值过滤。即在指标查询时可指定的值过滤的过滤条件|
|metrics|[Metrics](#schemametrics)|false|none|同环比、占比分析|

<h2 id="tocS_OrderField">OrderField</h2>
<!-- backwards compatibility -->
<a id="schemaorderfield"></a>
<a id="schema_OrderField"></a>
<a id="tocSorderfield"></a>
<a id="tocsorderfield"></a>

```json
{
  "name": "string",
  "direction": "asc"
}

```

排序字段

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|字段名|
|direction|string|true|none|排序方向|

#### Enumerated Values

|Property|Value|
|---|---|
|direction|asc|
|direction|desc|

<h2 id="tocS_HavingCondition">HavingCondition</h2>
<!-- backwards compatibility -->
<a id="schemahavingcondition"></a>
<a id="schema_HavingCondition"></a>
<a id="tocShavingcondition"></a>
<a id="tocshavingcondition"></a>

```json
{
  "field": "__value",
  "operation": "==",
  "value": null
}

```

having值数据过滤

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|字段名称。只有 __value|
|operation|string|true|none|操作符|
|value|any|true|none|字段值|

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

<h2 id="tocS_Metrics">Metrics</h2>
<!-- backwards compatibility -->
<a id="schemametrics"></a>
<a id="schema_Metrics"></a>
<a id="tocSmetrics"></a>
<a id="tocsmetrics"></a>

```json
{
  "type": "sameperiod",
  "sameperiod_config": {
    "method": [
      "growth_value",
      "growth_rate"
    ],
    "offset": 0,
    "time_granularity": "day"
  }
}

```

同环比、占比分析

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|type|string|true|none|同环比占比类型。同环比：sameperiod；占比：proportion|
|sameperiod_config|[SameperiodConfig](#schemasameperiodconfig)|false|none|同环比配置|

#### Enumerated Values

|Property|Value|
|---|---|
|type|sameperiod|
|type|proportion|

<h2 id="tocS_SameperiodConfig">SameperiodConfig</h2>
<!-- backwards compatibility -->
<a id="schemasameperiodconfig"></a>
<a id="schema_SameperiodConfig"></a>
<a id="tocSsameperiodconfig"></a>
<a id="tocssameperiodconfig"></a>

```json
{
  "method": [
    "growth_value",
    "growth_rate"
  ],
  "offset": 0,
  "time_granularity": "day"
}

```

同环比配置

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|method|[string]|false|none|计算方法，增长值或增长率|
|offset|integer|true|none|偏移量|
|time_granularity|string|true|none|时间粒度|

#### Enumerated Values

|Property|Value|
|---|---|
|time_granularity|day|
|time_granularity|month|
|time_granularity|quarter|
|time_granularity|year|

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

<h2 id="tocS_PageTurnQueryWithSearchAfter">PageTurnQueryWithSearchAfter</h2>
<!-- backwards compatibility -->
<a id="schemapageturnquerywithsearchafter"></a>
<a id="schema_PageTurnQueryWithSearchAfter"></a>
<a id="tocSpageturnquerywithsearchafter"></a>
<a id="tocspageturnquerywithsearchafter"></a>

```json
{
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
  ],
  "properties": [
    "string"
  ]
}

```

分页查询的第一次查询请求

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|condition|[Condition](#schemacondition)|false|none|过滤条件|
|sort|[[Sort](#schemasort)]|false|none|排序字段，默认使用 _score 倒序，主键字段正序|
|limit|integer|true|none|返回的数量，默认值 10。范围 1-10000|
|need_total|boolean|false|none|是否需要总数，默认false|
|search_after|[any]|true|none|上次查询返回的最后一个文档的排序值。|
|properties|[string]|false|none|指定需要输出的属性集。默认为全部数据属性|

<h2 id="tocS_RelationPath">RelationPath</h2>
<!-- backwards compatibility -->
<a id="schemarelationpath"></a>
<a id="schema_RelationPath"></a>
<a id="tocSrelationpath"></a>
<a id="tocsrelationpath"></a>

```json
{
  "relations": [
    {
      "relation_type_id": "string",
      "relation_type_name": "string",
      "source_object_id": "string",
      "target_object_id": "string"
    }
  ],
  "length": 0
}

```

对象的关系路径

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|relations|[[Relation](#schemarelation)]|true|none|路径的边集合，沿着路径顺序出现的边|
|length|integer|true|none|当前路径的长度|

<h2 id="tocS_Relation">Relation</h2>
<!-- backwards compatibility -->
<a id="schemarelation"></a>
<a id="schema_Relation"></a>
<a id="tocSrelation"></a>
<a id="tocsrelation"></a>

```json
{
  "relation_type_id": "string",
  "relation_type_name": "string",
  "source_object_id": "string",
  "target_object_id": "string"
}

```

一度关系（边）

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|relation_type_id|string|true|none|关系类id|
|relation_type_name|string|true|none|关系类名称|
|source_object_id|string|true|none|起点对象id|
|target_object_id|string|true|none|终点对象id|

<h2 id="tocS_ObjectTypeOnPath">ObjectTypeOnPath</h2>
<!-- backwards compatibility -->
<a id="schemaobjecttypeonpath"></a>
<a id="schema_ObjectTypeOnPath"></a>
<a id="tocSobjecttypeonpath"></a>
<a id="tocsobjecttypeonpath"></a>

```json
{
  "id": "string",
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
  "limit": 0
}

```

路径中的对象类信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|对象类id|
|condition|[Condition](#schemacondition)|false|none|当前对象类的过滤条件|
|sort|[[Sort](#schemasort)]|false|none|对当前对象类的排序字段|
|limit|integer|false|none|对象类获取对象数量的限制,默认10|

<h2 id="tocS_TypeEdge">TypeEdge</h2>
<!-- backwards compatibility -->
<a id="schematypeedge"></a>
<a id="schema_TypeEdge"></a>
<a id="tocStypeedge"></a>
<a id="tocstypeedge"></a>

```json
{
  "relation_type_id": "string",
  "source_object_type_id": "string",
  "target_object_type_id": "string"
}

```

路径中的边信息。通过关系类id确定边，通过路径的起点和终点来确定当前路径的方向为正向还是反向，与关系类的起终点一致为正向，相反则为反向。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|relation_type_id|string|true|none|关系类id|
|source_object_type_id|string|true|none|路径的起点对象类|
|target_object_type_id|string|true|none|路径的终点对象类id|

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
      "limit": 0
    }
  ],
  "relation_types": [
    {
      "relation_type_id": "string",
      "source_object_type_id": "string",
      "target_object_type_id": "string"
    }
  ],
  "limit": 0
}

```

基于路径获取对象子图

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_types|[[ObjectTypeOnPath](#schemaobjecttypeonpath)]|true|none|路径中的对象类集合，顺序跟路径中节点出现顺序保持一致。|
|relation_types|[[TypeEdge](#schematypeedge)]|true|none|路径的边集合，沿着路径顺序出现的边。边上引用的关系类类型可为 direct、data_view 或 filtered_cross_join（分侧过滤全连接，FCJ）；<br>FCJ 边在子图查询中按该关系类在知识网络中的 `mapping_rules`（`source_condition` / `target_condition`）与引擎策略展开。|
|limit|integer|false|none|当前路径返回的路径数量的限制。默认10|

<h2 id="tocS_ObjectSubGraphResponse">ObjectSubGraphResponse</h2>
<!-- backwards compatibility -->
<a id="schemaobjectsubgraphresponse"></a>
<a id="schema_ObjectSubGraphResponse"></a>
<a id="tocSobjectsubgraphresponse"></a>
<a id="tocsobjectsubgraphresponse"></a>

```json
{
  "objects": {
    "property1": {
      "object_type_id": "string",
      "object_type_name": "string",
      "properties": {
        "property1": "string",
        "property2": "string"
      },
      "_instance_id": "string",
      "_display": null,
      "_instance_identity": {
        "property1": "string",
        "property2": "string"
      }
    },
    "property2": {
      "object_type_id": "string",
      "object_type_name": "string",
      "properties": {
        "property1": "string",
        "property2": "string"
      },
      "_instance_id": "string",
      "_display": null,
      "_instance_identity": {
        "property1": "string",
        "property2": "string"
      }
    }
  },
  "relation_paths": [
    {
      "relations": [
        {
          "relation_type_id": "string",
          "relation_type_name": "string",
          "source_object_id": "string",
          "target_object_id": "string"
        }
      ],
      "length": 0
    }
  ],
  "isolated_objects": {
    "property1": {
      "object_type_id": "string",
      "object_type_name": "string",
      "properties": {
        "property1": "string",
        "property2": "string"
      },
      "_instance_id": "string",
      "_display": null,
      "_instance_identity": {
        "property1": "string",
        "property2": "string"
      }
    },
    "property2": {
      "object_type_id": "string",
      "object_type_name": "string",
      "properties": {
        "property1": "string",
        "property2": "string"
      },
      "_instance_id": "string",
      "_display": null,
      "_instance_identity": {
        "property1": "string",
        "property2": "string"
      }
    }
  },
  "total_count": 0,
  "search_after": [
    null
  ]
}

```

对象子图

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|objects|object|true|none|子图中参与关系的对象map，key是对象id，value是对象信息。动态数据字段，其值可以是基本类型、MetricProperty或OperatorProperty|
|» **additionalProperties**|[ObjectInfoInSubgraph](#schemaobjectinfoinsubgraph)|false|none|子图中的对象信息|
|relation_paths|[[RelationPath](#schemarelationpath)]|true|none|对象的关系路径集合|
|isolated_objects|object|false|none|子图中未建立关系的孤立对象map，key是对象id，value是对象信息。动态数据字段，其值可以是基本类型、MetricProperty或OperatorProperty|
|» **additionalProperties**|[ObjectInfoInSubgraph](#schemaobjectinfoinsubgraph)|false|none|子图中的对象信息|
|total_count|integer|true|none|起点对象类的总条数|
|search_after|[any]|true|none|表示返回的最后一个起点类对象的排序值，获取这个用于下一次 search_after 分页|

<h2 id="tocS_ObjectSubGraphWithTypePathsResponse">ObjectSubGraphWithTypePathsResponse</h2>
<!-- backwards compatibility -->
<a id="schemaobjectsubgraphwithtypepathsresponse"></a>
<a id="schema_ObjectSubGraphWithTypePathsResponse"></a>
<a id="tocSobjectsubgraphwithtypepathsresponse"></a>
<a id="tocsobjectsubgraphwithtypepathsresponse"></a>

```json
[
  {
    "objects": {
      "property1": {
        "object_type_id": "string",
        "object_type_name": "string",
        "properties": {
          "property1": "string",
          "property2": "string"
        },
        "_instance_id": "string",
        "_display": null,
        "_instance_identity": {
          "property1": "string",
          "property2": "string"
        }
      },
      "property2": {
        "object_type_id": "string",
        "object_type_name": "string",
        "properties": {
          "property1": "string",
          "property2": "string"
        },
        "_instance_id": "string",
        "_display": null,
        "_instance_identity": {
          "property1": "string",
          "property2": "string"
        }
      }
    },
    "relation_paths": [
      {
        "relations": [
          {
            "relation_type_id": "string",
            "relation_type_name": "string",
            "source_object_id": "string",
            "target_object_id": "string"
          }
        ],
        "length": 0
      }
    ],
    "isolated_objects": {
      "property1": {
        "object_type_id": "string",
        "object_type_name": "string",
        "properties": {
          "property1": "string",
          "property2": "string"
        },
        "_instance_id": "string",
        "_display": null,
        "_instance_identity": {
          "property1": "string",
          "property2": "string"
        }
      },
      "property2": {
        "object_type_id": "string",
        "object_type_name": "string",
        "properties": {
          "property1": "string",
          "property2": "string"
        },
        "_instance_id": "string",
        "_display": null,
        "_instance_identity": {
          "property1": "string",
          "property2": "string"
        }
      }
    },
    "total_count": 0,
    "search_after": [
      null
    ]
  }
]

```

基于路径获取对象子图的返回体

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[[ObjectSubGraphResponse](#schemaobjectsubgraphresponse)]|false|none|基于路径获取对象子图的返回体|

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

行动资源

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

<h2 id="tocS_ExpectedImpactOperation">ExpectedImpactOperation</h2>
<!-- backwards compatibility -->
<a id="schemaexpectedimpactoperation"></a>
<a id="schema_ExpectedImpactOperation"></a>
<a id="tocSexpectedimpactoperation"></a>
<a id="tocsexpectedimpactoperation"></a>

```json
"add"

```

与 `action_intent` / `action_type` 同一枚举（add / modify / delete）。`impact_contracts[].expected_operation` 必填；`affect.expected_operation` 若填写须合法。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|string|false|none|与 `action_intent` / `action_type` 同一枚举（add / modify / delete）。`impact_contracts[].expected_operation` 必填；`affect.expected_operation` 若填写须合法。|

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

行动影响契约单条（与 bkn-backend `impact_contracts` 一致）。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_type_id|string|false|none|none|
|expected_operation|[ExpectedImpactOperation](#schemaexpectedimpactoperation)|false|none|与 `action_intent` / `action_type` 同一枚举（add / modify / delete）。`impact_contracts[].expected_operation` 必填；`affect.expected_operation` 若填写须合法。|
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
  "comment": "string",
  "object_type_id": "string",
  "expected_operation": "add",
  "affected_fields": [
    "string"
  ]
}

```

**[已废弃]** 使用 `impact_contracts`。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|comment|string|false|none|影响描述|
|object_type_id|string|false|none|影响的对象类ID|
|expected_operation|[ExpectedImpactOperation](#schemaexpectedimpactoperation)|false|none|若填写须合法；折行时服务端仍以 `action_type` 写入 `impact_contracts`。|
|affected_fields|[string]|false|none|none|

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
|expression|string|true|none|执行表达式。<br><br>1.固定频率指以固定周期执行持久化, frequency=< time_durations >, 用一个数字, 后面跟时间单位来定义。时间单位可以是如下之一: m - 分钟; h - 小时; d - 天|

#### Enumerated Values

|Property|Value|
|---|---|
|type|FIX_RATE|
|type|CRON|

<h2 id="tocS_ActionTypeParams">ActionTypeParams</h2>
<!-- backwards compatibility -->
<a id="schemaactiontypeparams"></a>
<a id="schema_ActionTypeParams"></a>
<a id="tocSactiontypeparams"></a>
<a id="tocsactiontypeparams"></a>

```json
{
  "name": "string",
  "type": "string",
  "source": "string",
  "value_from": "property",
  "value": "string"
}

```

行动类绑定的行动资源参数

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

<h2 id="tocS_PathEntries">PathEntries</h2>
<!-- backwards compatibility -->
<a id="schemapathentries"></a>
<a id="schema_PathEntries"></a>
<a id="tocSpathentries"></a>
<a id="tocspathentries"></a>

```json
{
  "entries": [
    {
      "objects": {
        "property1": {
          "object_type_id": "string",
          "object_type_name": "string",
          "properties": {
            "property1": "string",
            "property2": "string"
          },
          "_instance_id": "string",
          "_display": null,
          "_instance_identity": {
            "property1": "string",
            "property2": "string"
          }
        },
        "property2": {
          "object_type_id": "string",
          "object_type_name": "string",
          "properties": {
            "property1": "string",
            "property2": "string"
          },
          "_instance_id": "string",
          "_display": null,
          "_instance_identity": {
            "property1": "string",
            "property2": "string"
          }
        }
      },
      "relation_paths": [
        {
          "relations": [
            {
              "relation_type_id": "string",
              "relation_type_name": "string",
              "source_object_id": "string",
              "target_object_id": "string"
            }
          ],
          "length": 0
        }
      ],
      "isolated_objects": {
        "property1": {
          "object_type_id": "string",
          "object_type_name": "string",
          "properties": {
            "property1": "string",
            "property2": "string"
          },
          "_instance_id": "string",
          "_display": null,
          "_instance_identity": {
            "property1": "string",
            "property2": "string"
          }
        },
        "property2": {
          "object_type_id": "string",
          "object_type_name": "string",
          "properties": {
            "property1": "string",
            "property2": "string"
          },
          "_instance_id": "string",
          "_display": null,
          "_instance_identity": {
            "property1": "string",
            "property2": "string"
          }
        }
      },
      "total_count": 0,
      "search_after": [
        null
      ]
    }
  ]
}

```

路径子图返回体

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[ObjectSubGraphResponse](#schemaobjectsubgraphresponse)]|true|none|路径子图|

<h2 id="tocS_SubGraphQueryBaseOnTypePath">SubGraphQueryBaseOnTypePath</h2>
<!-- backwards compatibility -->
<a id="schemasubgraphquerybaseontypepath"></a>
<a id="schema_SubGraphQueryBaseOnTypePath"></a>
<a id="tocSsubgraphquerybaseontypepath"></a>
<a id="tocssubgraphquerybaseontypepath"></a>

```json
{
  "relation_type_paths": [
    {
      "object_types": [
        {
          "id": "string",
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
          "limit": 0
        }
      ],
      "relation_types": [
        {
          "relation_type_id": "string",
          "source_object_type_id": "string",
          "target_object_type_id": "string"
        }
      ],
      "limit": 0
    }
  ]
}

```

基于路径获取对象子图

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|relation_type_paths|[[RelationTypePath](#schemarelationtypepath)]|false|none|关系类路径集合|

<h2 id="tocS_MetricUnitType">MetricUnitType</h2>
<!-- backwards compatibility -->
<a id="schemametricunittype"></a>
<a id="schema_MetricUnitType"></a>
<a id="tocSmetricunittype"></a>
<a id="tocsmetricunittype"></a>

```json
"numUnit"

```

指标单位类型，与 bkn-backend `interfaces.ValidMetricUnitTypes`（及 bkn-metrics OpenAPI）一致。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|string|false|none|指标单位类型，与 bkn-backend `interfaces.ValidMetricUnitTypes`（及 bkn-metrics OpenAPI）一致。|

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

指标度量单位，与 bkn-backend `interfaces.ValidMetricUnits`（及 bkn-metrics OpenAPI）一致。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|string|false|none|指标度量单位，与 bkn-backend `interfaces.ValidMetricUnits`（及 bkn-metrics OpenAPI）一致。|

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

<h2 id="tocS_MetricPropertyValue">MetricPropertyValue</h2>
<!-- backwards compatibility -->
<a id="schemametricpropertyvalue"></a>
<a id="schema_MetricPropertyValue"></a>
<a id="tocSmetricpropertyvalue"></a>
<a id="tocsmetricpropertyvalue"></a>

```json
{
  "model": {
    "unit_type": "numUnit",
    "unit": "none"
  },
  "datas": [
    {
      "model": {
        "unit_type": "numUnit",
        "unit": "none"
      },
      "datas": [
        {
          "labels": {
            "property1": "string",
            "property2": "string"
          },
          "times": [
            null
          ],
          "values": [
            null
          ],
          "growth_values": [
            null
          ],
          "growth_rates": [
            null
          ],
          "proportions": [
            null
          ]
        }
      ],
      "step": "string",
      "is_variable": true,
      "is_calendar": true
    }
  ]
}

```

指标属性值

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|model|[MetricModel](#schemametricmodel)|true|none|指标模型的信息|
|datas|[[MetricData](#schemametricdata)]|true|none|指标数据|

<h2 id="tocS_MetricModel">MetricModel</h2>
<!-- backwards compatibility -->
<a id="schemametricmodel"></a>
<a id="schema_MetricModel"></a>
<a id="tocSmetricmodel"></a>
<a id="tocsmetricmodel"></a>

```json
{
  "unit_type": "numUnit",
  "unit": "none"
}

```

指标模型信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|unit_type|[MetricUnitType](#schemametricunittype)|true|none|指标单位类型，与 bkn-backend `interfaces.ValidMetricUnitTypes`（及 bkn-metrics OpenAPI）一致。|
|unit|[MetricUnit](#schemametricunit)|true|none|指标度量单位，与 bkn-backend `interfaces.ValidMetricUnits`（及 bkn-metrics OpenAPI）一致。|

<h2 id="tocS_ObjectDataResponse">ObjectDataResponse</h2>
<!-- backwards compatibility -->
<a id="schemaobjectdataresponse"></a>
<a id="schema_ObjectDataResponse"></a>
<a id="tocSobjectdataresponse"></a>
<a id="tocsobjectdataresponse"></a>

```json
{
  "object_type": {
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
  },
  "datas": [
    {
      "_instance_id": "string",
      "_instance_identity": {
        "property1": "string",
        "property2": "string"
      },
      "_display": null,
      "property1": "string",
      "property2": "string"
    }
  ],
  "total_count": 0,
  "search_after": [
    null
  ]
}

```

节点（对象类）信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_type|[ObjectTypeDetail](#schemaobjecttypedetail)|false|none|对象类信息|
|datas|[[ObjectInfo](#schemaobjectinfo)]|true|none|对象实例数据。动态数据字段，其值可以是基本类型、MetricProperty或OperatorProperty|
|total_count|integer|false|none|总条数|
|search_after|[any]|true|none|表示返回的最后一个文档的排序值，获取这个用于下一次 search_after 分页|

<h2 id="tocS_ObjectPropertiesValuesResponse">ObjectPropertiesValuesResponse</h2>
<!-- backwards compatibility -->
<a id="schemaobjectpropertiesvaluesresponse"></a>
<a id="schema_ObjectPropertiesValuesResponse"></a>
<a id="tocSobjectpropertiesvaluesresponse"></a>
<a id="tocsobjectpropertiesvaluesresponse"></a>

```json
{
  "datas": [
    {
      "_instance_id": "string",
      "_instance_identity": {
        "property1": "string",
        "property2": "string"
      },
      "_display": null,
      "property1": "string",
      "property2": "string"
    }
  ]
}

```

节点（对象类）信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|datas|[[ObjectPropertyValue](#schemaobjectpropertyvalue)]|true|none|对象实例数据。动态数据字段，其值可以是基本类型、MetricProperty或OperatorProperty|

<h2 id="tocS_ActionParam">ActionParam</h2>
<!-- backwards compatibility -->
<a id="schemaactionparam"></a>
<a id="schema_ActionParam"></a>
<a id="tocSactionparam"></a>
<a id="tocsactionparam"></a>

```json
{
  "parameters": {
    "property1": "string",
    "property2": "string"
  },
  "dynamic_params": {
    "property1": null,
    "property2": null
  },
  "_instance_id": "string",
  "_instance_identity": {
    "property1": "string",
    "property2": "string"
  },
  "_display": null
}

```

行动参数

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|parameters|object|true|none|实例化了的参数。是个map|
|» **additionalProperties**|any|false|none|none|

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|»» *anonymous*|string|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|»» *anonymous*|number|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|»» *anonymous*|boolean|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|»» *anonymous*|integer|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|»» *anonymous*|object|false|none|none|

continued

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|dynamic_params|object|false|none|动态参数。是个map|
|» **additionalProperties**|any|false|none|none|
|_instance_id|string|false|none|对象实例id：对象类id-根据联合主键生成的对象id。默认返回，当 exclude_system_properties 排除时，不返回。|
|_instance_identity|[InstanceIdentity](#schemainstanceidentity)|false|none|对象实例标识。key为属性名称，value为属性值。id是主键的字符串表现形式。默认返回，当 exclude_system_properties 排除时，不返回。|
|_display|any|false|none|对象实例的显示属性值。显示属性只选一个，所以这里直接展示值即可。默认返回，当 exclude_system_properties 排除时，不返回。|

<h2 id="tocS_ObjectInfo">ObjectInfo</h2>
<!-- backwards compatibility -->
<a id="schemaobjectinfo"></a>
<a id="schema_ObjectInfo"></a>
<a id="tocSobjectinfo"></a>
<a id="tocsobjectinfo"></a>

```json
{
  "_instance_id": "string",
  "_instance_identity": {
    "property1": "string",
    "property2": "string"
  },
  "_display": null,
  "property1": "string",
  "property2": "string"
}

```

对象信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|**additionalProperties**|any|false|none|none|

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|string|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|number|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|boolean|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|integer|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[MetricProperty](#schemametricproperty)|false|none|指标属性|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[OperatorProperty](#schemaoperatorproperty)|false|none|算子属性|

continued

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|_instance_id|string|false|none|实例id。默认返回，当 exclude_system_properties 排除时，不返回。|
|_instance_identity|[InstanceIdentity](#schemainstanceidentity)|false|none|实例标识。默认返回，当 exclude_system_properties 排除时，不返回。|
|_display|any|false|none|对象的显示属性值。默认返回，当 exclude_system_properties 排除时，不返回。|

<h2 id="tocS_ObjectInfoInSubgraph">ObjectInfoInSubgraph</h2>
<!-- backwards compatibility -->
<a id="schemaobjectinfoinsubgraph"></a>
<a id="schema_ObjectInfoInSubgraph"></a>
<a id="tocSobjectinfoinsubgraph"></a>
<a id="tocsobjectinfoinsubgraph"></a>

```json
{
  "object_type_id": "string",
  "object_type_name": "string",
  "properties": {
    "property1": "string",
    "property2": "string"
  },
  "_instance_id": "string",
  "_display": null,
  "_instance_identity": {
    "property1": "string",
    "property2": "string"
  }
}

```

子图中的对象信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_type_id|string|true|none|对象类ID|
|object_type_name|string|true|none|对象类名称|
|properties|object|true|none|属性值列表。map中key是属性名，value是属性值|
|» **additionalProperties**|any|false|none|none|

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|»» *anonymous*|string|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|»» *anonymous*|number|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|»» *anonymous*|boolean|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|»» *anonymous*|integer|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|»» *anonymous*|[MetricProperty](#schemametricproperty)|false|none|指标属性|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|»» *anonymous*|[OperatorProperty](#schemaoperatorproperty)|false|none|算子属性|

continued

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|_instance_id|string|false|none|对象实例id：对象类id-根据联合主键生成的对象id。默认返回，当 exclude_system_properties 排除时，不返回。|
|_display|any|false|none|对象实例的显示属性值。显示属性只选一个，所以这里直接展示值即可。默认返回，当 exclude_system_properties 排除时，不返回。|
|_instance_identity|[InstanceIdentity](#schemainstanceidentity)|false|none|对象实例标识。key为属性名称，value为属性值。id是主键的字符串表现形式。默认返回，当 exclude_system_properties 排除时，不返回。|

<h2 id="tocS_ObjectPropertyValue">ObjectPropertyValue</h2>
<!-- backwards compatibility -->
<a id="schemaobjectpropertyvalue"></a>
<a id="schema_ObjectPropertyValue"></a>
<a id="tocSobjectpropertyvalue"></a>
<a id="tocsobjectpropertyvalue"></a>

```json
{
  "_instance_id": "string",
  "_instance_identity": {
    "property1": "string",
    "property2": "string"
  },
  "_display": null,
  "property1": "string",
  "property2": "string"
}

```

对象的属性值

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|**additionalProperties**|any|false|none|none|

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|string|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|number|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|boolean|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|integer|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[MetricPropertyValue](#schemametricpropertyvalue)|false|none|指标属性值|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|object|false|none|none|

continued

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|_instance_id|string|false|none|对象实例id：对象类id-根据联合主键生成的对象id。默认返回，当 exclude_system_properties 排除时，不返回。|
|_instance_identity|[InstanceIdentity](#schemainstanceidentity)|false|none|对象实例标识。key为属性名称，value为属性值。id是主键的字符串表现形式。默认返回，当 exclude_system_properties 排除时，不返回。|
|_display|any|false|none|对象实例的显示属性值。显示属性只选一个，所以这里直接展示值即可。默认返回，当 exclude_system_properties 排除时，不返回。|

<h2 id="tocS_OperatorProperty">OperatorProperty</h2>
<!-- backwards compatibility -->
<a id="schemaoperatorproperty"></a>
<a id="schema_OperatorProperty"></a>
<a id="tocSoperatorproperty"></a>
<a id="tocsoperatorproperty"></a>

```json
{
  "property_type": "metric",
  "mapping_source_id": "string",
  "has_any_unfilled_params": true,
  "parameters": {
    "property1": "string",
    "property2": "string"
  },
  "dynamic_params": {
    "property1": null,
    "property2": null
  }
}

```

算子属性

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|property_type|string|true|none|属性类型|
|mapping_source_id|string|true|none|映射的指标模型id|
|has_any_unfilled_params|boolean|true|none|是否有未填充的参数，默认是false|
|parameters|object|true|none|实例化了的参数。是个map|
|» **additionalProperties**|any|false|none|none|

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|»» *anonymous*|string|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|»» *anonymous*|number|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|»» *anonymous*|boolean|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|»» *anonymous*|integer|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|»» *anonymous*|object|false|none|none|

continued

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|dynamic_params|object|true|none|动态参数。是个map|
|» **additionalProperties**|any|false|none|none|

#### Enumerated Values

|Property|Value|
|---|---|
|property_type|metric|

<h2 id="tocS_PropertyQueryBody">PropertyQueryBody</h2>
<!-- backwards compatibility -->
<a id="schemapropertyquerybody"></a>
<a id="schema_PropertyQueryBody"></a>
<a id="tocSpropertyquerybody"></a>
<a id="tocspropertyquerybody"></a>

```json
{
  "_instance_identities": [
    {
      "property1": "string",
      "property2": "string"
    }
  ],
  "properties": [
    "string"
  ],
  "dynamic_params": {
    "property1": "string",
    "property2": "string"
  }
}

```

属性查询请求体

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|_instance_identities|[[InstanceIdentity](#schemainstanceidentity)]|true|none|对象主键的数组，代表多个对象。每项为 map，key 为主键属性名，value 为对应的属性值|
|properties|[string]|true|none|属性列表|
|dynamic_params|object|false|none|各逻辑属性所需的动态参数：外层 key 为**逻辑属性名**，内层为参数名到取值的 map。<br>当属性是指标属性时，内层结构可遵循 MetricPropertyDynamicParams（时间、分析维度等）；<br>算子属性则为参数名到标量或嵌套对象的映射。未传或某属性无动态需求时可省略该 key。|
|» **additionalProperties**|any|false|none|none|

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|»» *anonymous*|string|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|»» *anonymous*|number|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|»» *anonymous*|boolean|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|»» *anonymous*|integer|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|»» *anonymous*|[MetricPropertyDynamicParams](#schemametricpropertydynamicparams)|false|none|指标属性的 dynamic_params 结构。当属性是指标属性时，dynamic_params 应遵循此 schema。<br>包含时间参数、分析参数以及指标属性配置中定义的其他动态参数。|

<h2 id="tocS_SubGraphQueryBaseOnSource">SubGraphQueryBaseOnSource</h2>
<!-- backwards compatibility -->
<a id="schemasubgraphquerybaseonsource"></a>
<a id="schema_SubGraphQueryBaseOnSource"></a>
<a id="tocSsubgraphquerybaseonsource"></a>
<a id="tocssubgraphquerybaseonsource"></a>

```json
{
  "concept_groups": [
    "string"
  ],
  "source_object_type_id": "string",
  "condition": {
    "operation": "and",
    "sub_conditions": [
      {
        "operation": "and",
        "sub_conditions": []
      }
    ]
  },
  "direction": "forward",
  "path_length": 0,
  "include_incomplete_path": false,
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

基于起点、方向和路径长度获取对象子图

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|concept_groups|[string]|false|none|概念分组id数组|
|source_object_type_id|string|true|none|起点对象类id|
|condition|[Condition](#schemacondition)|false|none|起点对象类过滤条件|
|direction|string|true|none|探索子图的路径方向|
|path_length|integer|true|none|探索子图的路径的最大长度|
|include_incomplete_path|boolean|false|none|是否包含不完整的路径。默认是false，不包含，只返回完整的路径|
|sort|[[Sort](#schemasort)]|false|none|对起点类的排序字段|
|limit|integer|false|none|对起点类的对象数量的限制。默认是10|
|need_total|boolean|false|none|是否需要(起点类)总数，默认false。|
|search_after|[any]|false|none|上次查询返回的最后一个起点类对象的排序值。只对起点类生效。第一次查询不传此字段|

#### Enumerated Values

|Property|Value|
|---|---|
|direction|forward|
|direction|backward|
|direction|bidirectional|

<h2 id="tocS_SubGraphQueryBaseOnObjects">SubGraphQueryBaseOnObjects</h2>
<!-- backwards compatibility -->
<a id="schemasubgraphquerybaseonobjects"></a>
<a id="schema_SubGraphQueryBaseOnObjects"></a>
<a id="tocSsubgraphquerybaseonobjects"></a>
<a id="tocssubgraphquerybaseonobjects"></a>

```json
{
  "entries": [
    {
      "object_type_id": "string",
      "_instance_identity": {
        "property1": "string",
        "property2": "string"
      }
    }
  ]
}

```

基于一组对象实例探索关系子图的请求体

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[InputObjectInstance](#schemainputobjectinstance)]|true|none|对象实例数组|

<h2 id="tocS_InputObjectInstance">InputObjectInstance</h2>
<!-- backwards compatibility -->
<a id="schemainputobjectinstance"></a>
<a id="schema_InputObjectInstance"></a>
<a id="tocSinputobjectinstance"></a>
<a id="tocsinputobjectinstance"></a>

```json
{
  "object_type_id": "string",
  "_instance_identity": {
    "property1": "string",
    "property2": "string"
  }
}

```

输入的对象实例

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|object_type_id|string|true|none|对象类型ID|
|_instance_identity|[InstanceIdentity](#schemainstanceidentity)|true|none|对象唯一标识，对应主键字段。key为主键属性名，value为对应的属性值|

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

行动类信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|行动类ID|
|name|string|true|none|行动类名称|
|action_type|string|true|none|**[已废弃]** 等价于 `action_intent`。若同时传入须与同名字段取值一致。|
|action_intent|string|false|none|推荐：`add`/`modify`/`delete`（与历史上 `action_type` 对齐）。|
|object_type_id|string|true|none|行动类所绑定的对象类ID|
|condition|[ActionCondition](#schemaactioncondition)|true|none|行动条件|
|affect|[Affect](#schemaaffect)|true|none|**[已废弃]** 使用 `impact_contracts`。|
|impact_contracts|[[ImpactContractItem](#schemaimpactcontractitem)]|false|none|[行动影响契约单条（与 bkn-backend `impact_contracts` 一致）。]|
|action_source|any|true|none|绑定的行动的资源|

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[ToolSource](#schematoolsource)|false|none|行动资源|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[MCPSource](#schemamcpsource)|false|none|MCP资源|

continued

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|parameters|[[ActionTypeParams](#schemaactiontypeparams)]|true|none|行动资源参数|
|schedule|[Schedule](#schemaschedule)|true|none|行动监听参数配置|

#### Enumerated Values

|Property|Value|
|---|---|
|action_type|add|
|action_type|modify|
|action_type|delete|
|action_intent|add|
|action_intent|modify|
|action_intent|delete|

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

<h2 id="tocS_Actions">Actions</h2>
<!-- backwards compatibility -->
<a id="schemaactions"></a>
<a id="schema_Actions"></a>
<a id="tocSactions"></a>
<a id="tocsactions"></a>

```json
{
  "action_type": {
    "id": "string",
    "name": "string",
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
  },
  "action_source": {
    "type": "tool",
    "box_id": "string",
    "tool_id": "string"
  },
  "actions": [
    {
      "parameters": {
        "property1": "string",
        "property2": "string"
      },
      "dynamic_params": {
        "property1": null,
        "property2": null
      },
      "_instance_id": "string",
      "_instance_identity": {
        "property1": "string",
        "property2": "string"
      },
      "_display": null
    }
  ]
}

```

行动查询返回结果

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|action_type|[ActionType](#schemaactiontype)|false|none|内嵌的行动类负载（外层键名仍为 `action_type`，与载荷内枚举字段 **`action_intent`** 不相同）。载荷内推荐消费 `ActionType.action_intent` / `impact_contracts`。|
|action_source|any|true|none|行动资源|

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[ToolSource](#schematoolsource)|false|none|行动资源|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|[MCPSource](#schemamcpsource)|false|none|MCP资源|

continued

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|actions|[[ActionParam](#schemaactionparam)]|true|none|各对象的实例化后的行动参数，顺序与请求的对象唯一标识数组一致|

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

<h2 id="tocS_condition_eq">condition_eq</h2>
<!-- backwards compatibility -->
<a id="schemacondition_eq"></a>
<a id="schema_condition_eq"></a>
<a id="tocScondition_eq"></a>
<a id="tocscondition_eq"></a>

```json
{
  "field": "string",
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
|operation|==|

<h2 id="tocS_condition_in">condition_in</h2>
<!-- backwards compatibility -->
<a id="schemacondition_in"></a>
<a id="schema_condition_in"></a>
<a id="tocScondition_in"></a>
<a id="tocscondition_in"></a>

```json
{
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
|field|string|true|none|过滤字段，包含支持所有类型|
|operation|string|true|none|操作符|
|value|[any]|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|in|

<h2 id="tocS_condition_knn">condition_knn</h2>
<!-- backwards compatibility -->
<a id="schemacondition_knn"></a>
<a id="schema_condition_knn"></a>
<a id="tocScondition_knn"></a>
<a id="tocscondition_knn"></a>

```json
{
  "field": "string",
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
|field|string|true|none|过滤字段, vector字段或者是构建了向量索引的字段|
|operation|string|true|none|操作符|
|value|number|true|none|过滤值。当limit_key为k时，limit_value为整型；当limit_key为max_distance和min_score时，limit_value为浮点型|
|limit_key|string|false|none|执行径向搜索时使用的过滤和评分行为, k:返回最相似的limit_value个结果；max_distance:返回距离小于等于limit_value的结果；min_score：返回相似度分数大于等于limit_value的结果。默认值为k|
|limit_value|number|false|none|执行径向搜索使用的值。默认值为100|
|sub_conditions|[[Condition](#schemacondition)]|false|none|knn下的子查询|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|knn|
|limit_key|k|
|limit_key|max_distance|
|limit_key|min_score|

<h2 id="tocS_condition_like">condition_like</h2>
<!-- backwards compatibility -->
<a id="schemacondition_like"></a>
<a id="schema_condition_like"></a>
<a id="tocScondition_like"></a>
<a id="tocscondition_like"></a>

```json
{
  "field": "string",
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
|operation|like|

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
|fields|[string]|false|none|过滤字段数组，多字段全文匹配支持的字段类型：text字符串或者构建了全文索引的字段。为空时，用opensearch中 index.default_field 配置的字段进行查询。当需要对所有字段进行匹配时，此参数传 ["*"].|
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

<h2 id="tocS_condition_match_phrase">condition_match_phrase</h2>
<!-- backwards compatibility -->
<a id="schemacondition_match_phrase"></a>
<a id="schema_condition_match_phrase"></a>
<a id="tocScondition_match_phrase"></a>
<a id="tocscondition_match_phrase"></a>

```json
{
  "field": "string",
  "operation": "match_phrase",
  "value": "string"
}

```

match_phrase 过滤，支持单个字段和*, * 表示全部字段

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，短语匹配支持的字段类型：text字符串或者构建了全文索引的字段|
|operation|string|true|none|操作符|
|value|string|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|match_phrase|

<h2 id="tocS_condition_match">condition_match</h2>
<!-- backwards compatibility -->
<a id="schemacondition_match"></a>
<a id="schema_condition_match"></a>
<a id="tocScondition_match"></a>
<a id="tocscondition_match"></a>

```json
{
  "field": "string",
  "operation": "match",
  "value": "string"
}

```

match 过滤，支持单个字段和*, * 表示全部字段

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，全文匹配支持的字段类型：text字符串或者构建了全文索引的字段|
|operation|string|true|none|操作符|
|value|string|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|match|

<h2 id="tocS_condition_not_eq">condition_not_eq</h2>
<!-- backwards compatibility -->
<a id="schemacondition_not_eq"></a>
<a id="schema_condition_not_eq"></a>
<a id="tocScondition_not_eq"></a>
<a id="tocscondition_not_eq"></a>

```json
{
  "field": "string",
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
|operation|!=|

<h2 id="tocS_condition_not_in">condition_not_in</h2>
<!-- backwards compatibility -->
<a id="schemacondition_not_in"></a>
<a id="schema_condition_not_in"></a>
<a id="tocScondition_not_in"></a>
<a id="tocscondition_not_in"></a>

```json
{
  "field": "string",
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
|field|string|true|none|过滤字段，支持所有类型|
|operation|string|true|none|操作符|
|value|[any]|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|not_in|

<h2 id="tocS_condition_not_like">condition_not_like</h2>
<!-- backwards compatibility -->
<a id="schemacondition_not_like"></a>
<a id="schema_condition_not_like"></a>
<a id="tocScondition_not_like"></a>
<a id="tocscondition_not_like"></a>

```json
{
  "field": "string",
  "operation": "not_like",
  "value": "string"
}

```

not_like过滤

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，不相似支持的字段类型：string字符串|
|operation|string|true|none|操作符|
|value|string|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|not_like|

<h2 id="tocS_condition_regex">condition_regex</h2>
<!-- backwards compatibility -->
<a id="schemacondition_regex"></a>
<a id="schema_condition_regex"></a>
<a id="tocScondition_regex"></a>
<a id="tocscondition_regex"></a>

```json
{
  "field": "string",
  "operation": "regex",
  "value": "string"
}

```

regex过滤

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，正则支持的字段类型：string字符串或构建了关键字索引的字符串字段|
|operation|string|true|none|操作符|
|value|string|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|regex|

<h2 id="tocS_condition_lt">condition_lt</h2>
<!-- backwards compatibility -->
<a id="schemacondition_lt"></a>
<a id="schema_condition_lt"></a>
<a id="tocScondition_lt"></a>
<a id="tocscondition_lt"></a>

```json
{
  "field": "string",
  "operation": "<",
  "value": null
}

```

小于过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，小于支持的字段类型：数值、字符串|
|operation|string|true|none|操作符|
|value|any|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|<|

<h2 id="tocS_condition_gte">condition_gte</h2>
<!-- backwards compatibility -->
<a id="schemacondition_gte"></a>
<a id="schema_condition_gte"></a>
<a id="tocScondition_gte"></a>
<a id="tocscondition_gte"></a>

```json
{
  "field": "string",
  "operation": ">=",
  "value": null
}

```

大于等于过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，大于等于支持的字段类型：数值、字符串|
|operation|string|true|none|操作符|
|value|any|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|>=|

<h2 id="tocS_condition_gt">condition_gt</h2>
<!-- backwards compatibility -->
<a id="schemacondition_gt"></a>
<a id="schema_condition_gt"></a>
<a id="tocScondition_gt"></a>
<a id="tocscondition_gt"></a>

```json
{
  "field": "string",
  "operation": ">",
  "value": null
}

```

大于过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，大于支持的字段类型：数值、字符串|
|operation|string|true|none|操作符|
|value|any|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|>|

<h2 id="tocS_condition_lte">condition_lte</h2>
<!-- backwards compatibility -->
<a id="schemacondition_lte"></a>
<a id="schema_condition_lte"></a>
<a id="tocScondition_lte"></a>
<a id="tocscondition_lte"></a>

```json
{
  "field": "string",
  "operation": "<=",
  "value": null
}

```

小于等于过滤条件

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，小于等于支持的字段类型：数值、字符串|
|operation|string|true|none|操作符|
|value|any|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|<=|

<h2 id="tocS_condition_range">condition_range</h2>
<!-- backwards compatibility -->
<a id="schemacondition_range"></a>
<a id="schema_condition_range"></a>
<a id="tocScondition_range"></a>
<a id="tocscondition_range"></a>

```json
{
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
|field|string|true|none|过滤字段，即对象类的属性名称。支持的属性类型为时间类型、数值|
|operation|string|true|none|操作符|
|value|[any]|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|range|

<h2 id="tocS_condition_out_range">condition_out_range</h2>
<!-- backwards compatibility -->
<a id="schemacondition_out_range"></a>
<a id="schema_condition_out_range"></a>
<a id="tocScondition_out_range"></a>
<a id="tocscondition_out_range"></a>

```json
{
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
|field|string|true|none|过滤字段，即对象类的属性名称。支持的属性类型为时间类型、数值|
|operation|string|true|none|操作符|
|value|[any]|true|none|过滤值|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|range|

<h2 id="tocS_condition_exist">condition_exist</h2>
<!-- backwards compatibility -->
<a id="schemacondition_exist"></a>
<a id="schema_condition_exist"></a>
<a id="tocScondition_exist"></a>
<a id="tocscondition_exist"></a>

```json
{
  "field": "string",
  "operation": "exist"
}

```

存在过滤

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，即对象类的属性名称。支持的属性类型为所有类型|
|operation|string|true|none|操作符|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|exist|

<h2 id="tocS_condition_not_exist">condition_not_exist</h2>
<!-- backwards compatibility -->
<a id="schemacondition_not_exist"></a>
<a id="schema_condition_not_exist"></a>
<a id="tocScondition_not_exist"></a>
<a id="tocscondition_not_exist"></a>

```json
{
  "field": "string",
  "operation": "not_exist"
}

```

不存在过滤

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|field|string|true|none|过滤字段，即对象类的属性名称。支持的属性类型为所有类型|
|operation|string|true|none|操作符|

#### Enumerated Values

|Property|Value|
|---|---|
|operation|not_exist|

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
|*anonymous*|[condition_gt](#schemacondition_gt)|false|none|大于过滤条件|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_gte](#schemacondition_gte)|false|none|大于等于过滤条件|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_lt](#schemacondition_lt)|false|none|小于过滤条件|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_lte](#schemacondition_lte)|false|none|小于等于过滤条件|

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
|*anonymous*|[condition_range](#schemacondition_range)|false|none|范围内过滤。右侧值为长度为 2 的数组，边界为左闭右开|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_out_range](#schemacondition_out_range)|false|none|范围外过滤。右侧值为长度为2的数组，边界为左闭右开, 即 [ value[0],  value[1] )。此种情况下，符合过滤条件的值的区间为 (-inf, value[0] ) & [ value[1], +inf )，即左侧指定字段＜value[0] 或 ≥value[1] 的值。|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_exist](#schemacondition_exist)|false|none|存在过滤|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[condition_not_exist](#schemacondition_not_exist)|false|none|不存在过滤|

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

<h2 id="tocS_InstanceIdentity">InstanceIdentity</h2>
<!-- backwards compatibility -->
<a id="schemainstanceidentity"></a>
<a id="schema_InstanceIdentity"></a>
<a id="tocSinstanceidentity"></a>
<a id="tocsinstanceidentity"></a>

```json
{
  "property1": "string",
  "property2": "string"
}

```

唯一标识。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|**additionalProperties**|any|false|none|none|

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|string|false|none|none|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|integer|false|none|none|

<h2 id="tocS_ActionQuery">ActionQuery</h2>
<!-- backwards compatibility -->
<a id="schemaactionquery"></a>
<a id="schema_ActionQuery"></a>
<a id="tocSactionquery"></a>
<a id="tocsactionquery"></a>

```json
{
  "_instance_identities": [
    {
      "property1": "string",
      "property2": "string"
    }
  ],
  "dynamic_params": {}
}

```

行动查询。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|_instance_identities|[[InstanceIdentity](#schemainstanceidentity)]|false|none|目标对象主键列表。每项为主键字段名到值的映射。可为空数组或省略，表示由服务端依据行动类绑定与条件拉取候选实例。|
|dynamic_params|object|false|none|行动类「动态输入」参数取值（与行动类 parameters 中 value_from=input 的 name 对应，支持点分路径表示嵌套键）。<br>当行动类定义了此类参数且要求客户端提供时须传入对应非 null 取值，否则可能在业务层返回 400（例如 OntologyQuery.ActionType.InvalidParameter.DynamicParams）。|

<h2 id="tocS_ActionExecutionRequest">ActionExecutionRequest</h2>
<!-- backwards compatibility -->
<a id="schemaactionexecutionrequest"></a>
<a id="schema_ActionExecutionRequest"></a>
<a id="tocSactionexecutionrequest"></a>
<a id="tocsactionexecutionrequest"></a>

```json
{
  "_instance_identities": [
    {
      "property1": "string",
      "property2": "string"
    }
  ],
  "dynamic_params": {}
}

```

行动执行请求体。当行动类 parameters 中存在 value_from 为 input 的项时，须在 dynamic_params 中传入全部对应非空取值，否则返回 400（错误码 OntologyQuery.ActionExecution.InvalidParameter）。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|_instance_identities|[[InstanceIdentity](#schemainstanceidentity)]|false|none|目标对象唯一标识列表；扫描模式下可传空数组，由服务端按行动条件拉取实例|
|dynamic_params|object|false|none|与行动类 input 参数名对应的取值（支持点分路径）。有 input 参数时必填且不能为 null。|

<h2 id="tocS_ActionExecutionResponse">ActionExecutionResponse</h2>
<!-- backwards compatibility -->
<a id="schemaactionexecutionresponse"></a>
<a id="schema_ActionExecutionResponse"></a>
<a id="tocSactionexecutionresponse"></a>
<a id="tocsactionexecutionresponse"></a>

```json
{
  "execution_id": "string",
  "status": "pending",
  "message": "string",
  "created_at": 0
}

```

行动执行响应（异步）

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|execution_id|string|false|none|执行ID|
|status|string|false|none|执行状态|
|message|string|false|none|提示消息|
|created_at|integer(int64)|false|none|创建时间（毫秒时间戳）|

#### Enumerated Values

|Property|Value|
|---|---|
|status|pending|
|status|running|
|status|completed|
|status|failed|

<h2 id="tocS_ActionExecution">ActionExecution</h2>
<!-- backwards compatibility -->
<a id="schemaactionexecution"></a>
<a id="schema_ActionExecution"></a>
<a id="tocSactionexecution"></a>
<a id="tocsactionexecution"></a>

```json
{
  "id": "string",
  "kn_id": "string",
  "action_type_id": "string",
  "action_type_name": "string",
  "action_source_type": "tool",
  "object_type_id": "string",
  "trigger_type": "manual",
  "status": "pending",
  "total_count": 0,
  "success_count": 0,
  "failed_count": 0,
  "results": [
    {
      "instance_identity": {},
      "status": "pending",
      "parameters": {},
      "result": {},
      "error_message": "string",
      "duration_ms": 0
    }
  ],
  "results_total": 0,
  "results_offset": 0,
  "results_limit": 0,
  "dynamic_params": {},
  "executor_id": "string",
  "executor": {
    "id": "string",
    "type": "string",
    "name": "string"
  },
  "start_time": 0,
  "end_time": 0,
  "duration_ms": 0,
  "action_type_snapshot": {}
}

```

行动执行详情

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|false|none|执行ID|
|kn_id|string|false|none|业务知识网络ID|
|action_type_id|string|false|none|行动类ID|
|action_type_name|string|false|none|行动类名称|
|action_source_type|string|false|none|行动源类型|
|object_type_id|string|false|none|对象类ID|
|trigger_type|string|false|none|触发类型|
|status|string|false|none|执行状态|
|total_count|integer|false|none|对象总数|
|success_count|integer|false|none|成功数量|
|failed_count|integer|false|none|失败数量|
|results|[[ObjectExecutionResult](#schemaobjectexecutionresult)]|false|none|每个对象的执行结果（分页返回）|
|results_total|integer|false|none|results 总数（用于分页）|
|results_offset|integer|false|none|当前 results 偏移量|
|results_limit|integer|false|none|当前 results 分页大小|
|dynamic_params|object|false|none|动态参数|
|executor_id|string|false|none|执行者ID（已废弃，请使用 executor）|
|executor|object|false|none|执行者信息|
|» id|string|false|none|执行者ID|
|» type|string|false|none|执行者类型|
|» name|string|false|none|执行者名称|
|start_time|integer(int64)|false|none|开始时间（毫秒时间戳）|
|end_time|integer(int64)|false|none|结束时间（毫秒时间戳）|
|duration_ms|integer(int64)|false|none|执行耗时（毫秒）|
|action_type_snapshot|object|false|none|执行时的行动类配置快照（与 ontology-manager 返回一致）|

#### Enumerated Values

|Property|Value|
|---|---|
|action_source_type|tool|
|action_source_type|mcp|
|trigger_type|manual|
|trigger_type|scheduled|
|status|pending|
|status|running|
|status|completed|
|status|failed|
|status|cancelled|

<h2 id="tocS_ObjectExecutionResult">ObjectExecutionResult</h2>
<!-- backwards compatibility -->
<a id="schemaobjectexecutionresult"></a>
<a id="schema_ObjectExecutionResult"></a>
<a id="tocSobjectexecutionresult"></a>
<a id="tocsobjectexecutionresult"></a>

```json
{
  "instance_identity": {},
  "status": "pending",
  "parameters": {},
  "result": {},
  "error_message": "string",
  "duration_ms": 0
}

```

单个对象的执行结果

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|instance_identity|object|false|none|对象实例标识|
|status|string|false|none|执行状态|
|parameters|object|false|none|解析后的执行参数|
|result|object|false|none|执行结果|
|error_message|string|false|none|错误消息|
|duration_ms|integer(int64)|false|none|执行耗时（毫秒）|

#### Enumerated Values

|Property|Value|
|---|---|
|status|pending|
|status|success|
|status|failed|
|status|cancelled|

<h2 id="tocS_ActionLogQuery">ActionLogQuery</h2>
<!-- backwards compatibility -->
<a id="schemaactionlogquery"></a>
<a id="schema_ActionLogQuery"></a>
<a id="tocSactionlogquery"></a>
<a id="tocsactionlogquery"></a>

```json
{
  "action_type_id": "string",
  "status": "pending",
  "trigger_type": "manual",
  "start_time_range": [
    0,
    0
  ],
  "limit": 20,
  "need_total": false,
  "search_after": [
    null
  ]
}

```

行动执行日志查询

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|action_type_id|string|false|none|行动类ID（可选）|
|status|string|false|none|执行状态（可选）|
|trigger_type|string|false|none|触发类型（可选）|
|start_time_range|[integer]|false|none|开始时间范围 [起始, 结束]（毫秒时间戳）|
|limit|integer|false|none|返回数量限制，默认20，最大1000|
|need_total|boolean|false|none|是否需要返回总数|
|search_after|[any]|false|none|分页游标|

#### Enumerated Values

|Property|Value|
|---|---|
|status|pending|
|status|running|
|status|completed|
|status|failed|
|status|cancelled|
|trigger_type|manual|
|trigger_type|scheduled|

<h2 id="tocS_ActionExecutionList">ActionExecutionList</h2>
<!-- backwards compatibility -->
<a id="schemaactionexecutionlist"></a>
<a id="schema_ActionExecutionList"></a>
<a id="tocSactionexecutionlist"></a>
<a id="tocsactionexecutionlist"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "kn_id": "string",
      "action_type_id": "string",
      "action_type_name": "string",
      "action_source_type": "tool",
      "object_type_id": "string",
      "trigger_type": "manual",
      "status": "pending",
      "total_count": 0,
      "success_count": 0,
      "failed_count": 0,
      "results": [
        {
          "instance_identity": {},
          "status": "pending",
          "parameters": {},
          "result": {},
          "error_message": "string",
          "duration_ms": 0
        }
      ],
      "results_total": 0,
      "results_offset": 0,
      "results_limit": 0,
      "dynamic_params": {},
      "executor_id": "string",
      "executor": {
        "id": "string",
        "type": "string",
        "name": "string"
      },
      "start_time": 0,
      "end_time": 0,
      "duration_ms": 0,
      "action_type_snapshot": {}
    }
  ],
  "total_count": 0,
  "search_after": [
    null
  ]
}

```

行动执行日志列表

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[ActionExecution](#schemaactionexecution)]|false|none|执行记录列表|
|total_count|integer|false|none|总数（当 need_total 为 true 时返回）|
|search_after|[any]|false|none|下一页游标|

<h2 id="tocS_CancelExecutionRequest">CancelExecutionRequest</h2>
<!-- backwards compatibility -->
<a id="schemacancelexecutionrequest"></a>
<a id="schema_CancelExecutionRequest"></a>
<a id="tocScancelexecutionrequest"></a>
<a id="tocscancelexecutionrequest"></a>

```json
{
  "reason": "string"
}

```

取消执行请求

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|reason|string|false|none|取消原因（可选）|

<h2 id="tocS_CancelExecutionResponse">CancelExecutionResponse</h2>
<!-- backwards compatibility -->
<a id="schemacancelexecutionresponse"></a>
<a id="schema_CancelExecutionResponse"></a>
<a id="tocScancelexecutionresponse"></a>
<a id="tocscancelexecutionresponse"></a>

```json
{
  "execution_id": "string",
  "status": "cancelled",
  "message": "string",
  "cancelled_count": 0,
  "completed_count": 0
}

```

取消执行响应

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|execution_id|string|false|none|执行ID|
|status|string|false|none|执行状态|
|message|string|false|none|提示消息|
|cancelled_count|integer|false|none|被取消的对象数量（原本 pending 状态的对象）|
|completed_count|integer|false|none|已完成的对象数量（取消前已执行完成的对象）|

#### Enumerated Values

|Property|Value|
|---|---|
|status|cancelled|

<h2 id="tocS_MetricQueryOrderBy">MetricQueryOrderBy</h2>
<!-- backwards compatibility -->
<a id="schemametricqueryorderby"></a>
<a id="schema_MetricQueryOrderBy"></a>
<a id="tocSmetricqueryorderby"></a>
<a id="tocsmetricqueryorderby"></a>

```json
{
  "property": "string",
  "direction": "asc"
}

```

指标请求体 `order_by` 单项；与 MetricDefinition.calculation_formula.order_by 同构（DESIGN 附录 B.1），字段名为 `property`（与旧 uniquery `order_by_fields[].name` 区分）。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|property|string|true|none|排序属性（数据属性名等）|
|direction|string|false|none|排序方向；未传时由引擎默认|

#### Enumerated Values

|Property|Value|
|---|---|
|direction|asc|
|direction|desc|

<h2 id="tocS_MetricQueryRequestBody">MetricQueryRequestBody</h2>
<!-- backwards compatibility -->
<a id="schemametricqueryrequestbody"></a>
<a id="schema_MetricQueryRequestBody"></a>
<a id="tocSmetricqueryrequestbody"></a>
<a id="tocsmetricqueryrequestbody"></a>

```json
{
  "time": {
    "start": 0,
    "end": 0,
    "instant": true,
    "step": "string"
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
  "analysis_dimensions": [
    "string"
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
  },
  "metrics": {
    "type": "sameperiod",
    "sameperiod_config": {
      "method": [
        "growth_value",
        "growth_rate"
      ],
      "offset": 0,
      "time_granularity": "day"
    }
  },
  "limit": 1
}

```

指标查询请求体（metric_id 在路径）。与 uniquery MetricQuery 对齐的时间/分析等 + BKN Condition；含 limit 截断序列条数；不含 scope_context。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|time|object|false|none|none|
|» start|integer(int64)|false|none|none|
|» end|integer(int64)|false|none|none|
|» instant|boolean|false|none|none|
|» step|string|false|none|none|
|condition|[Condition](#schemacondition)|false|none|none|
|analysis_dimensions|[string]|false|none|none|
|order_by|[[MetricQueryOrderBy](#schemametricqueryorderby)]|false|none|[指标请求体 `order_by` 单项；与 MetricDefinition.calculation_formula.order_by 同构（DESIGN 附录 B.1），字段名为 `property`（与旧 uniquery `order_by_fields[].name` 区分）。<br>]|
|having|[HavingCondition](#schemahavingcondition)|false|none|与 calculation_formula.having 同构（附录 B.1）；对聚合结果过滤|
|metrics|[Metrics](#schemametrics)|false|none|同环比/占比（§3.3.1.1），与 uniquery Metrics 一致|
|limit|integer|false|none|仅返回前 limit 条 Data 序列（MetricData.datas）|

<h2 id="tocS_MetricDryRunConfig">MetricDryRunConfig</h2>
<!-- backwards compatibility -->
<a id="schemametricdryrunconfig"></a>
<a id="schema_MetricDryRunConfig"></a>
<a id="tocSmetricdryrunconfig"></a>
<a id="tocsmetricdryrunconfig"></a>

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
  "unit_type": "numUnit",
  "unit": "none",
  "metric_type": "string",
  "scope_type": "string",
  "scope_ref": "string",
  "time_dimension": {},
  "calculation_formula": {},
  "analysis_dimensions": [
    {}
  ]
}

```

试算请求体中的 `metric_config`：与 MetricDefinition 中参与执行的字段 JSON 同构；id、name、kn_id、branch、tags 等可省略。
不包含服务端管理字段（creator、create_time、updater、update_time、module_type）。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|false|none|none|
|kn_id|string|false|none|none|
|branch|string|false|none|none|
|name|string|false|none|none|
|comment|string|false|none|none|
|tags|[string]|false|none|none|
|unit_type|[MetricUnitType](#schemametricunittype)|false|none|指标单位类型，与 bkn-backend `interfaces.ValidMetricUnitTypes`（及 bkn-metrics OpenAPI）一致。|
|unit|[MetricUnit](#schemametricunit)|false|none|指标度量单位，与 bkn-backend `interfaces.ValidMetricUnits`（及 bkn-metrics OpenAPI）一致。|
|metric_type|string|true|none|none|
|scope_type|string|true|none|none|
|scope_ref|string|true|none|none|
|time_dimension|object|false|none|与 MetricDefinition.time_dimension 同构|
|calculation_formula|object|true|none|与 MetricDefinition.calculation_formula 同构（DESIGN 附录 B.1）|
|analysis_dimensions|[object]|false|none|none|

<h2 id="tocS_MetricDryRun">MetricDryRun</h2>
<!-- backwards compatibility -->
<a id="schemametricdryrun"></a>
<a id="schema_MetricDryRun"></a>
<a id="tocSmetricdryrun"></a>
<a id="tocsmetricdryrun"></a>

```json
{
  "metric_config": {
    "id": "string",
    "kn_id": "string",
    "branch": "string",
    "name": "string",
    "comment": "string",
    "tags": [
      "string"
    ],
    "unit_type": "numUnit",
    "unit": "none",
    "metric_type": "string",
    "scope_type": "string",
    "scope_ref": "string",
    "time_dimension": {},
    "calculation_formula": {},
    "analysis_dimensions": [
      {}
    ]
  },
  "time": {},
  "condition": {
    "operation": "and",
    "sub_conditions": [
      {
        "operation": "and",
        "sub_conditions": []
      }
    ]
  },
  "analysis_dimensions": [
    "string"
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
  },
  "metrics": {
    "type": "sameperiod",
    "sameperiod_config": {
      "method": [
        "growth_value",
        "growth_rate"
      ],
      "offset": 0,
      "time_granularity": "day"
    }
  },
  "limit": 1
}

```

指标试算：metric_config 与 MetricDefinition 同构；运行时字段与 MetricQueryRequestBody 对齐

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|metric_config|[MetricDryRunConfig](#schemametricdryrunconfig)|true|none|试算请求体中的 `metric_config`：与 MetricDefinition 中参与执行的字段 JSON 同构；id、name、kn_id、branch、tags 等可省略。<br>不包含服务端管理字段（creator、create_time、updater、update_time、module_type）。|
|time|object|false|none|none|
|condition|[Condition](#schemacondition)|false|none|none|
|analysis_dimensions|[string]|false|none|none|
|order_by|[[MetricQueryOrderBy](#schemametricqueryorderby)]|false|none|[指标请求体 `order_by` 单项；与 MetricDefinition.calculation_formula.order_by 同构（DESIGN 附录 B.1），字段名为 `property`（与旧 uniquery `order_by_fields[].name` 区分）。<br>]|
|having|[HavingCondition](#schemahavingcondition)|false|none|having值数据过滤|
|metrics|[Metrics](#schemametrics)|false|none|同环比、占比分析|
|limit|integer|false|none|none|

<h2 id="tocS_MetricData">MetricData</h2>
<!-- backwards compatibility -->
<a id="schemametricdata"></a>
<a id="schema_MetricData"></a>
<a id="tocSmetricdata"></a>
<a id="tocsmetricdata"></a>

```json
{
  "model": {
    "unit_type": "numUnit",
    "unit": "none"
  },
  "datas": [
    {
      "labels": {
        "property1": "string",
        "property2": "string"
      },
      "times": [
        null
      ],
      "values": [
        null
      ],
      "growth_values": [
        null
      ],
      "growth_rates": [
        null
      ],
      "proportions": [
        null
      ]
    }
  ],
  "step": "string",
  "is_variable": true,
  "is_calendar": true
}

```

与 uniquery GetMetricDataByID 返回一致

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|model|object|false|none|none|
|» unit_type|[MetricUnitType](#schemametricunittype)|false|none|指标单位类型，与 bkn-backend `interfaces.ValidMetricUnitTypes`（及 bkn-metrics OpenAPI）一致。|
|» unit|[MetricUnit](#schemametricunit)|false|none|指标度量单位，与 bkn-backend `interfaces.ValidMetricUnits`（及 bkn-metrics OpenAPI）一致。|
|datas|[[MetricDataRow](#schemametricdatarow)]|true|none|none|
|step|string|true|none|none|
|is_variable|boolean|true|none|none|
|is_calendar|boolean|true|none|none|

<h2 id="tocS_MetricDataRow">MetricDataRow</h2>
<!-- backwards compatibility -->
<a id="schemametricdatarow"></a>
<a id="schema_MetricDataRow"></a>
<a id="tocSmetricdatarow"></a>
<a id="tocsmetricdatarow"></a>

```json
{
  "labels": {
    "property1": "string",
    "property2": "string"
  },
  "times": [
    null
  ],
  "values": [
    null
  ],
  "growth_values": [
    null
  ],
  "growth_rates": [
    null
  ],
  "proportions": [
    null
  ]
}

```

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|labels|object|true|none|none|
|» **additionalProperties**|string|false|none|none|
|times|[any]|true|none|none|
|values|[any]|true|none|none|
|growth_values|[any]|false|none|none|
|growth_rates|[any]|false|none|none|
|proportions|[any]|false|none|none|



