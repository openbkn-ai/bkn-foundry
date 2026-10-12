# Sandbox Executor API 参考

以 [`interfaces/http/rest.py`](../interfaces/http/rest.py) 为准；机器可读合同见
[executor OpenAPI](../../../../../docs/api/sandbox-executor/sandbox-executor.yaml)。
基础地址示例：`http://sandbox-executor:8080`。该服务供集群内部调用，当前 REST
适配器没有实现 `INTERNAL_API_TOKEN` 或 Bearer 令牌校验，访问边界由部署网络控制。

## 提交代码

`POST /execute`，请求头 `Content-Type: application/json`。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| execution_id | string | 是 | 执行标识 |
| session_id | string | 是 | 会话标识 |
| code | string | 是 | 要执行的代码 |
| language | string | 是 | python、javascript 或 shell |
| timeout | integer | 否 | 秒，1–3600，默认 300 |
| event | object | 否 | handler 输入，默认空对象 |
| env_vars | object | 否 | 环境变量，默认空对象 |
| working_directory | string 或 null | 否 | 相对 workspace 根目录的执行目录，默认 null；拒绝越界路径 |

HTTP 200 表示已安排后台执行，响应如下：

```json
{"execution_id":"exec_20240115_abc12345","status":"PENDING","message":"Execution submitted"}
```

最终执行结果通过回调上报 control plane；调用方可从 control plane 的
`GET /api/v1/executions/{execution_id}/status` 或 `/result` 查询。
提交成功不代表执行完成。

```bash
curl -X POST http://sandbox-executor:8080/execute \
  -H 'Content-Type: application/json' \
  -d '{"execution_id":"exec_20240115_abc12345","session_id":"sess_test_001","code":"def handler(event): return event","language":"python","event":{"name":"Alice"},"timeout":10}'
```

## 健康检查

`GET /health` 的 HTTP 200 响应：

```json
{"status":"healthy","version":"1.0.0","uptime_seconds":42.5,"active_executions":0}
```

隔离机制或 workspace 不可用时返回 HTTP 503，响应使用
`{"detail":{"status":"unhealthy","reason":"..."}}`。

## 服务信息

`GET /` 返回：

```json
{"service":"sandbox-executor","version":"1.0.0","docs":"/docs","health":"/health","execute":"/execute"}
```

## 同步会话依赖

`POST /internal/session-config/sync`，仅供 control plane 调用。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| session_id | string | 是 | 会话标识 |
| language_runtime | string | 是 | 运行时，例如 python3.11 |
| python_package_index_url | string | 是 | Python 包索引地址 |
| sync_mode | string | 是 | replace 或 merge |
| dependencies | string[] | 否 | pip 依赖列表，默认空数组 |

HTTP 200 返回 `status`、`installed_dependencies`、`error`、`started_at`、
`completed_at`。每项依赖包含 `name`、`version`、`install_location`、`install_time`、
`is_from_template`。

## 错误响应

| 状态码 | 情况及响应 |
| --- | --- |
| 400 | ValueError/Pydantic 异常处理器返回 `error_code/description/error_detail/solution`；依赖同步的业务校验失败返回 `detail` 对象 |
| 422 | FastAPI 请求校验失败返回 `detail` 数组；依赖安装失败返回含 `error_code/description/error_detail` 的 `detail` 对象 |
| 500 | 通用异常返回 `error_code/description/error_detail/solution`；依赖同步失败返回 `detail` 对象 |
| 503 | 服务未初始化返回 `detail` 字符串；健康检查失败返回 `detail` 对象 |

后台执行失败由 control plane 中的执行状态和结果体现，不会改写已返回的提交确认。
