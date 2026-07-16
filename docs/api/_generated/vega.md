<!-- Generator: Widdershins v4.0.1 -->

<h1 id="auth-resource">Auth Resource v0.1.0</h1>

> Scroll down for code samples, example requests and responses. Select a language for code samples from the tabs above or the mobile navigation menu.

Vega Backend 授权资源列表 API。

该端点按 `resource_type` 查询可授权资源的简要列表，供 ISF / 权限配置场景选择资源使用。

支持的 `resource_type`：

- `catalog`：Catalog 授权资源。
- `resource`：Resource 授权资源。
- `connector-type`：ConnectorType 授权资源。

Base URLs:

* <a href="/api/vega-backend/v1">/api/vega-backend/v1</a>

<h1 id="auth-resource-default">Default</h1>

## 获取授权资源列表

<a id="opIdlistAuthResources"></a>

> Code samples

```shell
# You can also use wget
curl -X GET /api/vega-backend/v1/api/vega-backend/v1/auth-resources?resource_type=catalog \
  -H 'Accept: application/json'

```

```http
GET /api/vega-backend/v1/api/vega-backend/v1/auth-resources?resource_type=catalog HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/auth-resources?resource_type=catalog',
{
  method: 'GET',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.get '/api/vega-backend/v1/api/vega-backend/v1/auth-resources',
  params: {
  'resource_type' => 'string'
}, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.get('/api/vega-backend/v1/api/vega-backend/v1/auth-resources', params={
  'resource_type': 'catalog'
}, headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('GET','/api/vega-backend/v1/api/vega-backend/v1/auth-resources', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/auth-resources?resource_type=catalog");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("GET");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("GET", "/api/vega-backend/v1/api/vega-backend/v1/auth-resources", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`GET /api/vega-backend/v1/auth-resources`

<h3 id="获取授权资源列表-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|resource_type|query|string|true|授权资源类型|
|id|query|string|false|按资源 ID 精确过滤|
|keyword|query|string|false|按名称关键字过滤|
|offset|query|integer(int64)|false|分页偏移量，>=0，默认 0|
|limit|query|integer(int64)|false|每页数量，默认 50|
|sort|query|string|false|排序字段；仅支持 `name`|
|direction|query|string|false|排序方向|

#### Enumerated Values

|Parameter|Value|
|---|---|
|resource_type|catalog|
|resource_type|resource|
|resource_type|connector-type|
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
      "type": "catalog",
      "name": "string"
    }
  ],
  "total_count": 0
}
```

<h3 id="获取授权资源列表-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[ListAuthResources](#schemalistauthresources)|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数错误|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未认证|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务端错误|None|

<h3 id="获取授权资源列表-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

# Schemas

<h2 id="tocS_AuthResource">AuthResource</h2>
<!-- backwards compatibility -->
<a id="schemaauthresource"></a>
<a id="schema_AuthResource"></a>
<a id="tocSauthresource"></a>
<a id="tocsauthresource"></a>

```json
{
  "id": "string",
  "type": "catalog",
  "name": "string"
}

```

可授权资源简要信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|none|
|type|string|true|none|none|
|name|string|true|none|none|

#### Enumerated Values

|Property|Value|
|---|---|
|type|catalog|
|type|resource|
|type|connector-type|

<h2 id="tocS_ListAuthResources">ListAuthResources</h2>
<!-- backwards compatibility -->
<a id="schemalistauthresources"></a>
<a id="schema_ListAuthResources"></a>
<a id="tocSlistauthresources"></a>
<a id="tocslistauthresources"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "type": "catalog",
      "name": "string"
    }
  ],
  "total_count": 0
}

```

授权资源列表响应

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[AuthResource](#schemaauthresource)]|true|none|[可授权资源简要信息]|
|total_count|integer(int64)|true|none|none|



<!-- Generator: Widdershins v4.0.1 -->

<h1 id="buildtask">BuildTask v0.1.0</h1>

> Scroll down for code samples, example requests and responses. Select a language for code samples from the tabs above or the mobile navigation menu.

Vega Backend 构建任务（BuildTask）相关 API。

BuildTask 是顶级独立资源，每条 task 关联 1 个 Resource，对其执行 streaming /
batch / embedding 三种模式之一的构建。状态机：

  init → running → stopping → stopped
                ↘ completed
                ↘ failed

外部仅可触发 `start` / `stop` 两个动作；其它状态由 worker 内部转移，外部只读。
`start` / `stop` 返回 HTTP 202 且 body 为空，仅表示"指令被接受"，**不**表示
status 已切换；持久化 status 由 worker 实际执行时写入，客户端如需感知应轮询 GET。

端点设计遵循 [vega-backend/CLAUDE.md] 的"端点设计规则"：批量删除走 path
（`DELETE /build-tasks/{ids}`，逗号分隔），不提供 `/resources/{id}/build-tasks`
嵌套列表视图——按父资源过滤一律走 `GET /build-tasks?resource_id={id}`。

Base URLs:

* <a href="/api/vega-backend/v1">/api/vega-backend/v1</a>

<h1 id="buildtask-default">Default</h1>

## 获取构建任务列表

> Code samples

```shell
# You can also use wget
curl -X GET /api/vega-backend/v1/api/vega-backend/v1/build-tasks \
  -H 'Accept: application/json'

```

```http
GET /api/vega-backend/v1/api/vega-backend/v1/build-tasks HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/build-tasks',
{
  method: 'GET',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.get '/api/vega-backend/v1/api/vega-backend/v1/build-tasks',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.get('/api/vega-backend/v1/api/vega-backend/v1/build-tasks', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('GET','/api/vega-backend/v1/api/vega-backend/v1/build-tasks', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/build-tasks");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("GET");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("GET", "/api/vega-backend/v1/api/vega-backend/v1/build-tasks", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`GET /api/vega-backend/v1/build-tasks`

分页获取构建任务；支持按 resource_id / catalog_id / status / mode 过滤。

<h3 id="获取构建任务列表-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|resource_id|query|string|false|按归属 resource 过滤|
|catalog_id|query|string|false|按归属 catalog 过滤|
|status|query|string|false|按状态过滤|
|mode|query|string|false|按任务模式过滤|
|offset|query|integer(int64)|false|分页偏移量，>=0，默认 0|
|limit|query|integer(int64)|false|每页数量，默认 20|
|sort|query|string|false|排序字段，默认 update_time|
|direction|query|string|false|排序方向，默认 desc|

#### Enumerated Values

|Parameter|Value|
|---|---|
|status|init|
|status|running|
|status|stopping|
|status|stopped|
|status|completed|
|status|failed|
|mode|streaming|
|mode|batch|
|mode|embedding|
|sort|create_time|
|sort|update_time|
|sort|status|
|sort|mode|
|direction|asc|
|direction|desc|

> Example responses

> 200 Response

```json
{
  "entries": [
    {
      "id": "string",
      "resource_id": "string",
      "catalog_id": "string",
      "status": "init",
      "mode": "streaming",
      "total_count": 0,
      "synced_count": 0,
      "vectorized_count": 0,
      "synced_mark": "string",
      "error_msg": "string",
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
      "update_time": 0,
      "embedding_fields": "string",
      "build_key_fields": "string",
      "embedding_model": "string",
      "model_dimensions": 0,
      "fulltext_fields": "string",
      "fulltext_analyzer": "string"
    }
  ],
  "total_count": 0
}
```

<h3 id="获取构建任务列表-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[ListBuildTasks](#schemalistbuildtasks)|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数 / 请求体非法|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="获取构建任务列表-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 创建构建任务

> Code samples

```shell
# You can also use wget
curl -X POST /api/vega-backend/v1/api/vega-backend/v1/build-tasks \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json'

```

```http
POST /api/vega-backend/v1/api/vega-backend/v1/build-tasks HTTP/1.1

Content-Type: application/json
Accept: application/json

```

```javascript
const inputBody = '{
  "resource_id": "string",
  "mode": "streaming",
  "embedding_fields": "string",
  "build_key_fields": "string",
  "embedding_model": "string",
  "model_dimensions": 0,
  "fulltext_fields": "string",
  "fulltext_analyzer": "string"
}';
const headers = {
  'Content-Type':'application/json',
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/build-tasks',
{
  method: 'POST',
  body: inputBody,
  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Content-Type' => 'application/json',
  'Accept' => 'application/json'
}

result = RestClient.post '/api/vega-backend/v1/api/vega-backend/v1/build-tasks',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Content-Type': 'application/json',
  'Accept': 'application/json'
}

r = requests.post('/api/vega-backend/v1/api/vega-backend/v1/build-tasks', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Content-Type' => 'application/json',
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('POST','/api/vega-backend/v1/api/vega-backend/v1/build-tasks', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/build-tasks");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("POST");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Content-Type": []string{"application/json"},
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("POST", "/api/vega-backend/v1/api/vega-backend/v1/build-tasks", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`POST /api/vega-backend/v1/build-tasks`

为指定 Resource 创建构建任务。同一 Resource 同时只能有一个 BuildTask；
已存在时返回 400 `VegaBackend.BuildTask.Exist`。

- `streaming` 模式要求 Resource 在 `source_metadata.primary_keys` 中声明主键。
- `batch` 模式要求 `build_key_fields` 非空。
- 所有字段都需要在 Resource 的 schema 中存在；否则 400。
- 创建后 status = `init`。

> Body parameter

```json
{
  "resource_id": "string",
  "mode": "streaming",
  "embedding_fields": "string",
  "build_key_fields": "string",
  "embedding_model": "string",
  "model_dimensions": 0,
  "fulltext_fields": "string",
  "fulltext_analyzer": "string"
}
```

<h3 id="创建构建任务-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|body|body|[CreateBuildTaskRequest](#schemacreatebuildtaskrequest)|true|none|

> Example responses

> 201 Response

```json
{
  "id": "string",
  "resource_id": "string",
  "status": "init"
}
```

<h3 id="创建构建任务-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|201|[Created](https://tools.ietf.org/html/rfc7231#section-6.3.2)|创建成功|Inline|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数 / 请求体非法|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|资源不存在|None|
|406|[Not Acceptable](https://tools.ietf.org/html/rfc7231#section-6.5.6)|Content-Type 不是 application/json|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="创建构建任务-responseschema">Response Schema</h3>

Status Code **201**

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» id|string|true|none|新建 BuildTask ID|
|» resource_id|string|true|none|关联 resource ID|
|» status|string|true|none|初始 status，恒为 `init`|

#### Enumerated Values

|Property|Value|
|---|---|
|status|init|

<aside class="success">
This operation does not require authentication
</aside>

## 获取构建任务详情

> Code samples

```shell
# You can also use wget
curl -X GET /api/vega-backend/v1/api/vega-backend/v1/build-tasks/{id} \
  -H 'Accept: application/json'

```

```http
GET /api/vega-backend/v1/api/vega-backend/v1/build-tasks/{id} HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/build-tasks/{id}',
{
  method: 'GET',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.get '/api/vega-backend/v1/api/vega-backend/v1/build-tasks/{id}',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.get('/api/vega-backend/v1/api/vega-backend/v1/build-tasks/{id}', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('GET','/api/vega-backend/v1/api/vega-backend/v1/build-tasks/{id}', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/build-tasks/{id}");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("GET");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("GET", "/api/vega-backend/v1/api/vega-backend/v1/build-tasks/{id}", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`GET /api/vega-backend/v1/build-tasks/{id}`

<h3 id="获取构建任务详情-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|id|path|string|true|BuildTask ID|

> Example responses

> 200 Response

```json
{
  "id": "string",
  "resource_id": "string",
  "catalog_id": "string",
  "status": "init",
  "mode": "streaming",
  "total_count": 0,
  "synced_count": 0,
  "vectorized_count": 0,
  "synced_mark": "string",
  "error_msg": "string",
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
  "update_time": 0,
  "embedding_fields": "string",
  "build_key_fields": "string",
  "embedding_model": "string",
  "model_dimensions": 0,
  "fulltext_fields": "string",
  "fulltext_analyzer": "string"
}
```

<h3 id="获取构建任务详情-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[BuildTask](#schemabuildtask)|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|资源不存在|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="获取构建任务详情-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 删除构建任务（整体事务）

> Code samples

```shell
# You can also use wget
curl -X DELETE /api/vega-backend/v1/api/vega-backend/v1/build-tasks/{ids} \
  -H 'Accept: application/json'

```

```http
DELETE /api/vega-backend/v1/api/vega-backend/v1/build-tasks/{ids} HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/build-tasks/{ids}',
{
  method: 'DELETE',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.delete '/api/vega-backend/v1/api/vega-backend/v1/build-tasks/{ids}',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.delete('/api/vega-backend/v1/api/vega-backend/v1/build-tasks/{ids}', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('DELETE','/api/vega-backend/v1/api/vega-backend/v1/build-tasks/{ids}', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/build-tasks/{ids}");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("DELETE");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("DELETE", "/api/vega-backend/v1/api/vega-backend/v1/build-tasks/{ids}", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`DELETE /api/vega-backend/v1/build-tasks/{ids}`

整体事务语义：所有 id 通过预校验后才进入删除阶段，任一预校验失败整批不删。

预校验顺序：

1. 任一 id 处于 `running` / `stopping` → 409 `VegaBackend.BuildTask.HasRunningExecution`，
   `error_details` 携带 `{ running_ids: [...] }`。状态拦截**不可绕过**，必须先 stop 再删。
2. 任一 id 不存在（且未启用 `ignore_missing`）→ 404 `VegaBackend.BuildTask.NotFound`，
   `error_details` 携带 `{ missing_ids: [...] }`。
3. 全部通过 → 逐条删除，返回 204。

<h3 id="删除构建任务（整体事务）-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|ignore_missing|query|boolean|false|放宽不存在性检查：缺失 id 视为已删除（静默跳过），其它 id 正常删。|
|ids|path|string|true|BuildTask ID 列表，逗号分隔（单条即长度为 1 的退化情形）。|

#### Detailed descriptions

**ignore_missing**: 放宽不存在性检查：缺失 id 视为已删除（静默跳过），其它 id 正常删。
**不影响** running/stopping 拦截。

**ids**: BuildTask ID 列表，逗号分隔（单条即长度为 1 的退化情形）。

> Example responses

<h3 id="删除构建任务（整体事务）-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|删除成功|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|资源不存在|None|
|409|[Conflict](https://tools.ietf.org/html/rfc7231#section-6.5.8)|状态冲突：状态机不允许该转移，或 task 处于 running/stopping 无法删除|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="删除构建任务（整体事务）-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 启动构建任务

> Code samples

```shell
# You can also use wget
curl -X POST /api/vega-backend/v1/api/vega-backend/v1/build-tasks/{id}/start \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json'

```

```http
POST /api/vega-backend/v1/api/vega-backend/v1/build-tasks/{id}/start HTTP/1.1

Content-Type: application/json
Accept: application/json

```

```javascript
const inputBody = '{
  "execute_type": "incremental"
}';
const headers = {
  'Content-Type':'application/json',
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/build-tasks/{id}/start',
{
  method: 'POST',
  body: inputBody,
  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Content-Type' => 'application/json',
  'Accept' => 'application/json'
}

result = RestClient.post '/api/vega-backend/v1/api/vega-backend/v1/build-tasks/{id}/start',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Content-Type': 'application/json',
  'Accept': 'application/json'
}

r = requests.post('/api/vega-backend/v1/api/vega-backend/v1/build-tasks/{id}/start', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Content-Type' => 'application/json',
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('POST','/api/vega-backend/v1/api/vega-backend/v1/build-tasks/{id}/start', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/build-tasks/{id}/start");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("POST");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Content-Type": []string{"application/json"},
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("POST", "/api/vega-backend/v1/api/vega-backend/v1/build-tasks/{id}/start", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`POST /api/vega-backend/v1/build-tasks/{id}/start`

合法状态转移：`status ∈ {init, stopped, completed, failed}` → `running`。
`failed` 允许重启：从 `synced_mark` 水位继续（batch incremental），
无需删除重建；worker 实际开始执行时会清空 `error_msg`。

**响应 status 滞后**：HTTP 202 表示"启动指令已被接受并入队"，**不**表示
status 已切换为 `running`。worker 实际执行时才会写为 `running`，
客户端如需确认应轮询 GET。响应 body 为空。

**非幂等**：对已 running / stopping 的 task 再次 start 返回 409
`VegaBackend.BuildTask.InvalidStateTransition`。

> Body parameter

```json
{
  "execute_type": "incremental"
}
```

<h3 id="启动构建任务-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|body|body|[StartBuildTaskRequest](#schemastartbuildtaskrequest)|false|none|
|id|path|string|true|BuildTask ID|

> Example responses

<h3 id="启动构建任务-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|202|[Accepted](https://tools.ietf.org/html/rfc7231#section-6.3.3)|启动指令已接受（body 为空）|None|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数 / 请求体非法|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|资源不存在|None|
|409|[Conflict](https://tools.ietf.org/html/rfc7231#section-6.5.8)|状态冲突：状态机不允许该转移，或 task 处于 running/stopping 无法删除|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="启动构建任务-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 停止构建任务

> Code samples

```shell
# You can also use wget
curl -X POST /api/vega-backend/v1/api/vega-backend/v1/build-tasks/{id}/stop \
  -H 'Accept: application/json'

```

```http
POST /api/vega-backend/v1/api/vega-backend/v1/build-tasks/{id}/stop HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/build-tasks/{id}/stop',
{
  method: 'POST',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.post '/api/vega-backend/v1/api/vega-backend/v1/build-tasks/{id}/stop',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.post('/api/vega-backend/v1/api/vega-backend/v1/build-tasks/{id}/stop', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('POST','/api/vega-backend/v1/api/vega-backend/v1/build-tasks/{id}/stop', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/build-tasks/{id}/stop");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("POST");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("POST", "/api/vega-backend/v1/api/vega-backend/v1/build-tasks/{id}/stop", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`POST /api/vega-backend/v1/build-tasks/{id}/stop`

合法状态转移：
- `running` → `stopping`：正常停止，worker 在批间检查点退出后写 `stopped`。
- `stopping` → `stopped`：强制完结。worker 已不在（重试耗尽 / 服务重启）时
  `stopping` 不会被自动推进，再次调 stop 直接落停，使任务可删除。

**响应 status 滞后**：HTTP 202 表示"停止指令已被记录"。`stopping → stopped`
由 worker 异步推进，客户端可轮询 GET 拿最新 status。响应 body 为空。

**非幂等**：对非 running / stopping 状态的 task 调 stop 返回 409
`VegaBackend.BuildTask.InvalidStateTransition`。

<h3 id="停止构建任务-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|id|path|string|true|BuildTask ID|

> Example responses

<h3 id="停止构建任务-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|202|[Accepted](https://tools.ietf.org/html/rfc7231#section-6.3.3)|停止指令已接受（body 为空）|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|资源不存在|None|
|409|[Conflict](https://tools.ietf.org/html/rfc7231#section-6.5.8)|状态冲突：状态机不允许该转移，或 task 处于 running/stopping 无法删除|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="停止构建任务-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
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

操作者信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|账户 ID|
|type|string|true|none|账户类型|
|name|string|false|none|显示名|

<h2 id="tocS_BuildTask">BuildTask</h2>
<!-- backwards compatibility -->
<a id="schemabuildtask"></a>
<a id="schema_BuildTask"></a>
<a id="tocSbuildtask"></a>
<a id="tocsbuildtask"></a>

```json
{
  "id": "string",
  "resource_id": "string",
  "catalog_id": "string",
  "status": "init",
  "mode": "streaming",
  "total_count": 0,
  "synced_count": 0,
  "vectorized_count": 0,
  "synced_mark": "string",
  "error_msg": "string",
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
  "update_time": 0,
  "embedding_fields": "string",
  "build_key_fields": "string",
  "embedding_model": "string",
  "model_dimensions": 0,
  "fulltext_fields": "string",
  "fulltext_analyzer": "string"
}

```

构建任务实体

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|全局唯一 ID|
|resource_id|string|true|none|关联 Resource ID|
|catalog_id|string|true|none|关联 Catalog ID（冗余存储以加速过滤）|
|status|string|true|none|状态|
|mode|string|true|none|任务模式|
|total_count|integer(int64)|true|none|总数|
|synced_count|integer(int64)|true|none|已同步数|
|vectorized_count|integer(int64)|true|none|已向量化数|
|synced_mark|string|false|none|同步标记（增量构建用）|
|error_msg|string|false|none|错误信息（仅在 failed 状态下有意义）|
|creator|[AccountInfo](#schemaaccountinfo)|true|none|操作者信息|
|create_time|integer(int64)|true|none|创建时间，毫秒级时间戳|
|updater|[AccountInfo](#schemaaccountinfo)|true|none|操作者信息|
|update_time|integer(int64)|true|none|更新时间，毫秒级时间戳|
|embedding_fields|string|false|none|需向量化的字段，逗号分隔|
|build_key_fields|string|true|none|构建中依赖的特殊键字段，逗号分隔。<br>- batch 模式：依赖具有时序性的字段（如 update_time）<br>- streaming 模式：依赖唯一标识某行的字段（如主键）|
|embedding_model|string|false|none|嵌入模型名|
|model_dimensions|integer|false|none|模型向量维度|
|fulltext_fields|string|false|none|已建全文索引的字段，逗号分隔|
|fulltext_analyzer|string|false|none|全文分词器；为空用默认 standard|

#### Enumerated Values

|Property|Value|
|---|---|
|status|init|
|status|running|
|status|stopping|
|status|stopped|
|status|completed|
|status|failed|
|mode|streaming|
|mode|batch|
|mode|embedding|

<h2 id="tocS_CreateBuildTaskRequest">CreateBuildTaskRequest</h2>
<!-- backwards compatibility -->
<a id="schemacreatebuildtaskrequest"></a>
<a id="schema_CreateBuildTaskRequest"></a>
<a id="tocScreatebuildtaskrequest"></a>
<a id="tocscreatebuildtaskrequest"></a>

```json
{
  "resource_id": "string",
  "mode": "streaming",
  "embedding_fields": "string",
  "build_key_fields": "string",
  "embedding_model": "string",
  "model_dimensions": 0,
  "fulltext_fields": "string",
  "fulltext_analyzer": "string"
}

```

创建构建任务请求体

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|resource_id|string|true|none|关联 Resource ID（必填）|
|mode|string|true|none|任务模式|
|embedding_fields|string|false|none|需向量化的字段，逗号分隔|
|build_key_fields|string|false|none|构建键字段，逗号分隔；batch 模式必填|
|embedding_model|string|false|none|嵌入模型名；为空且 embedding_fields 非空时使用默认模型|
|model_dimensions|integer|false|none|模型向量维度；为 0 且 embedding_model 非空时按模型查询填入|
|fulltext_fields|string|false|none|需建全文索引的字段，逗号分隔；仅 string/text 字段可选。 string 字段建索引时主字段保持 keyword（精确匹配/排序不变），额外加一个 `<字段>.fulltext` 分词子字段；text 字段主字段本身分词。全文检索随结构化 数据同步落地（不像 embedding 需异步补）。|
|fulltext_analyzer|string|false|none|全文分词器（standard/ik_max_word/hanlp_index 等）；为空用 OpenSearch 默认 standard|

#### Enumerated Values

|Property|Value|
|---|---|
|mode|streaming|
|mode|batch|
|mode|embedding|

<h2 id="tocS_StartBuildTaskRequest">StartBuildTaskRequest</h2>
<!-- backwards compatibility -->
<a id="schemastartbuildtaskrequest"></a>
<a id="schema_StartBuildTaskRequest"></a>
<a id="tocSstartbuildtaskrequest"></a>
<a id="tocsstartbuildtaskrequest"></a>

```json
{
  "execute_type": "incremental"
}

```

启动构建任务请求体（可选）

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|execute_type|string|false|none|执行类型；默认 incremental|

#### Enumerated Values

|Property|Value|
|---|---|
|execute_type|incremental|
|execute_type|full|

<h2 id="tocS_ListBuildTasks">ListBuildTasks</h2>
<!-- backwards compatibility -->
<a id="schemalistbuildtasks"></a>
<a id="schema_ListBuildTasks"></a>
<a id="tocSlistbuildtasks"></a>
<a id="tocslistbuildtasks"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "resource_id": "string",
      "catalog_id": "string",
      "status": "init",
      "mode": "streaming",
      "total_count": 0,
      "synced_count": 0,
      "vectorized_count": 0,
      "synced_mark": "string",
      "error_msg": "string",
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
      "update_time": 0,
      "embedding_fields": "string",
      "build_key_fields": "string",
      "embedding_model": "string",
      "model_dimensions": 0,
      "fulltext_fields": "string",
      "fulltext_analyzer": "string"
    }
  ],
  "total_count": 0
}

```

构建任务列表

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[BuildTask](#schemabuildtask)]|true|none|条目列表|
|total_count|integer(int64)|true|none|总条数|



<!-- Generator: Widdershins v4.0.1 -->

<h1 id="catalog">Catalog v0.2.1</h1>

> Scroll down for code samples, example requests and responses. Select a language for code samples from the tabs above or the mobile navigation menu.

Vega Backend Catalog（数据目录）API。

Catalog 是一个数据源连接的抽象，对应一个物理数据源（MySQL / OpenSearch / S3 等）
或逻辑目录（虚拟视图）。资源（`Resource`）按 catalog 归属注册。

**可检索业务 KV（`extensions`，Issue #382，方案 B）**

- 与 `tags`（最多 5 个、用于展示的短字符串列表）不同，`extensions` 为 **扁平 string→string**，
  用于 DIP 等域外元数据及 **列表按 key/value 筛选**；持久化见 **`t_entity_extension`**（仅存一套行）。
- **创建 / 更新**：请求体可选 **`extensions`**。**根对象出现 `extensions` 键**（含 `{}`）
  即对该 catalog 做 KV **整包替换**；**键不出现**则不修改已有 KV。
- **读取**：详情与列表（`include_extensions=true` 时）返回 **`extensions`**。
- **列表**：默认不返回各条目的 KV object；`include_extensions=true` 时返回。
  可选 `include_extension_keys` 仅投影部分 key。
- **列表筛选**：**`extension_key`/`extension_value`**（数组 query，`style=form` + `explode=true`），等长成对 AND。
- 设计依据：
  [catalog-resource-labels-scheme-b-design.md](../../../design/vega/features/vega-backend/dip-for-extension/catalog-resource-labels-scheme-b-design.md)

**持久化（与 `migrations/mariadb`、`migrations/dm8` 惯例对齐）**

- 表名：`t_entity_extension`（**不**改 `t_catalog` / `t_resource` 主表结构）。
- 列：`f_entity_id` VARCHAR(40) NOT NULL（与 `t_catalog.f_id` **同一取值空间、全局唯一**）、
  `f_key` VARCHAR(128) NOT NULL、`f_value` VARCHAR(512) NOT NULL、
  `f_create_time` / `f_update_time` BIGINT（与现有表时间字段风格一致）。
- 主键：`PRIMARY KEY (f_entity_id, f_key)`（单值语义；**无** `f_scope` 列，依赖 catalog/resource
  id 全局不冲突之前提，见设计文档）。
- 索引（MariaDB 示例，DM8 同源语义）：`KEY idx_entity (f_entity_id)`；
  `KEY idx_entity_key_value (f_entity_id, f_key, f_value(191))` 等（前缀长度以方言上限为准）。
- 删除 catalog 时应用层同事务删除 `t_entity_extension` 中 `f_entity_id` 等于该 catalog `f_id` 的行；
  若级联删除其下 resource，须一并删除对应 resource 的 `f_entity_id` 行。

本文件仅包含 catalog 自身的 CRUD 与状态相关端点。跨资源的便利端点：
- `POST /catalogs/{id}/discover`（手动发现一次） → 见 `discover-task.yaml`
- `GET /catalogs/{ids}/resources`（按 catalog 列资源） → 见 `resource.yaml`
- `GET /catalogs/{cid}/discover-schedules`（按 catalog 列调度） → 见 `discover-schedule.yaml`

每个外部接口都有一一对应的内部版本，路径前缀为 `/api/vega-backend/in/v1`，请求体与
响应结构与外部完全一致；区别仅在于鉴权方式：外部走 OAuth Token 校验，内部从请求头
（`X-Account-ID` / `X-Account-Type`）解析访问者。

本文档仅描述外部接口。

Base URLs:

* <a href="/api/vega-backend/v1">/api/vega-backend/v1</a>

<h1 id="catalog-default">Default</h1>

## 获取 catalog 列表

> Code samples

```shell
# You can also use wget
curl -X GET /api/vega-backend/v1/api/vega-backend/v1/catalogs \
  -H 'Accept: application/json'

```

```http
GET /api/vega-backend/v1/api/vega-backend/v1/catalogs HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/catalogs',
{
  method: 'GET',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.get '/api/vega-backend/v1/api/vega-backend/v1/catalogs',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.get('/api/vega-backend/v1/api/vega-backend/v1/catalogs', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('GET','/api/vega-backend/v1/api/vega-backend/v1/catalogs', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/catalogs");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("GET");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("GET", "/api/vega-backend/v1/api/vega-backend/v1/catalogs", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`GET /api/vega-backend/v1/catalogs`

分页获取 catalog；支持按 name、tag、type、health_check_status 过滤。

**KV 筛选**：`extension_key` / `extension_value` 为数组 query。
序列化形如 `extension_key=a&extension_key=b&extension_value=1&extension_value=2`。参与筛选的一组参数 **必须等长**，
按下标配对为 AND 等值条件；长度不一致或未成对 → 400。

<h3 id="获取-catalog-列表-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|name|query|string|false|按名称模糊过滤，匹配名称中包含该值的 catalog|
|tag|query|string|false|按标签精确过滤|
|type|query|string|false|按 catalog 类型过滤|
|health_check_status|query|string|false|按健康检查状态过滤|
|enabled|query|boolean|false|按 catalog 启用状态过滤|
|offset|query|integer(int64)|false|分页偏移量，>=0，默认 0|
|limit|query|integer(int64)|false|每页数量，1-1000，-1 表示不分页，默认 20|
|sort|query|string|false|排序字段|
|direction|query|string|false|排序方向|
|extension_key|query|array[string]|false|与 `extension_value` 成对；多条件 AND。等长数组，按下标配对；等值匹配 `t_entity_extension.f_key`。|
|extension_value|query|array[string]|false|与 `extension_key` 成对；语义见 `extension_key`。|
|include_extensions|query|boolean|false|为 true 时列表 `entries` 中每条 Catalog 带 `extensions`；默认 false。|
|include_extension_keys|query|string|false|逗号分隔的 key 列表；在 `include_extensions` 为 true 时仅返回列出的 key（仍一次加载后过滤）。|

#### Detailed descriptions

**include_extensions**: 为 true 时列表 `entries` 中每条 Catalog 带 `extensions`；默认 false。

**include_extension_keys**: 逗号分隔的 key 列表；在 `include_extensions` 为 true 时仅返回列出的 key（仍一次加载后过滤）。
未携带或为空表示返回全部 key。

#### Enumerated Values

|Parameter|Value|
|---|---|
|type|physical|
|type|logical|
|health_check_status|healthy|
|health_check_status|degraded|
|health_check_status|unhealthy|
|health_check_status|offline|
|health_check_status|unchecked|
|sort|name|
|sort|create_time|
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
      "description": "string",
      "type": "physical",
      "enabled": true,
      "connector_type": "string",
      "connector_config": {},
      "metadata": {},
      "extensions": {
        "property1": "string",
        "property2": "string"
      },
      "health_check_enabled": true,
      "health_check_status": "healthy",
      "last_check_time": 0,
      "health_check_result": "string",
      "creator": {
        "id": "string",
        "type": "string"
      },
      "create_time": 0,
      "updater": {
        "id": "string",
        "type": "string"
      },
      "update_time": 0,
      "operations": [
        "string"
      ]
    }
  ],
  "total_count": 0
}
```

<h3 id="获取-catalog-列表-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[ListCatalogs](#schemalistcatalogs)|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数 / 请求体非法。常见 errcode（命名以合入代码为准）：
- `VegaBackend.InvalidParameter.RequestBody`：body schema 不合法
- `VegaBackend.InvalidParameter.ID`：`id` 缺失或格式非法（小写字母/数字/`_`/`-`，首字符须为字母或数字，长度 ≤40）
- `VegaBackend.Extensions.InvalidFormat`：`extensions` 非 object 或 key/value 非 string
- `VegaBackend.Extensions.QuotaExceeded`：条数或长度超限
- `VegaBackend.Extensions.ReservedKey`：key 以 `vega_` 开头
- `VegaBackend.Extensions.MismatchedQueryPairs`：`extension_key` / `extension_value` 数组长度不一致或未成对|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="获取-catalog-列表-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 创建 catalog

> Code samples

```shell
# You can also use wget
curl -X POST /api/vega-backend/v1/api/vega-backend/v1/catalogs \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json'

```

```http
POST /api/vega-backend/v1/api/vega-backend/v1/catalogs HTTP/1.1

Content-Type: application/json
Accept: application/json

```

```javascript
const inputBody = '{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "description": "string",
  "enabled": true,
  "connector_type": "string",
  "connector_config": {},
  "extensions": {
    "property1": "string",
    "property2": "string"
  }
}';
const headers = {
  'Content-Type':'application/json',
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/catalogs',
{
  method: 'POST',
  body: inputBody,
  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Content-Type' => 'application/json',
  'Accept' => 'application/json'
}

result = RestClient.post '/api/vega-backend/v1/api/vega-backend/v1/catalogs',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Content-Type': 'application/json',
  'Accept': 'application/json'
}

r = requests.post('/api/vega-backend/v1/api/vega-backend/v1/catalogs', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Content-Type' => 'application/json',
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('POST','/api/vega-backend/v1/api/vega-backend/v1/catalogs', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/catalogs");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("POST");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Content-Type": []string{"application/json"},
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("POST", "/api/vega-backend/v1/api/vega-backend/v1/catalogs", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`POST /api/vega-backend/v1/catalogs`

创建一个新的 catalog。`name` 全局唯一，已存在时返回 409。
`connector_type` 必须是已注册且 enabled 的连接器类型。

可选 **`extensions`**：与 `t_catalog` 插入 **同一事务**内写入 `t_entity_extension`。
响应体 `CatalogRef` 返回 `extensions`，与持久化一致。

> Body parameter

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "description": "string",
  "enabled": true,
  "connector_type": "string",
  "connector_config": {},
  "extensions": {
    "property1": "string",
    "property2": "string"
  }
}
```

<h3 id="创建-catalog-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|body|body|[CatalogRequest](#schemacatalogrequest)|true|none|

> Example responses

> 201 Response

```json
{
  "id": "string",
  "extensions": {
    "property1": "string",
    "property2": "string"
  }
}
```

<h3 id="创建-catalog-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|201|[Created](https://tools.ietf.org/html/rfc7231#section-6.3.2)|创建成功|[CatalogRef](#schemacatalogref)|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数 / 请求体非法。常见 errcode（命名以合入代码为准）：
- `VegaBackend.InvalidParameter.RequestBody`：body schema 不合法
- `VegaBackend.InvalidParameter.ID`：`id` 缺失或格式非法（小写字母/数字/`_`/`-`，首字符须为字母或数字，长度 ≤40）
- `VegaBackend.Extensions.InvalidFormat`：`extensions` 非 object 或 key/value 非 string
- `VegaBackend.Extensions.QuotaExceeded`：条数或长度超限
- `VegaBackend.Extensions.ReservedKey`：key 以 `vega_` 开头
- `VegaBackend.Extensions.MismatchedQueryPairs`：`extension_key` / `extension_value` 数组长度不一致或未成对|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|406|[Not Acceptable](https://tools.ietf.org/html/rfc7231#section-6.5.6)|Content-Type 不是 application/json|None|
|409|[Conflict](https://tools.ietf.org/html/rfc7231#section-6.5.8)|同名 catalog 已存在 / connector_type 未启用|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="创建-catalog-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 获取 catalog 详情

> Code samples

```shell
# You can also use wget
curl -X GET /api/vega-backend/v1/api/vega-backend/v1/catalogs/{ids} \
  -H 'Accept: application/json'

```

```http
GET /api/vega-backend/v1/api/vega-backend/v1/catalogs/{ids} HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/catalogs/{ids}',
{
  method: 'GET',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.get '/api/vega-backend/v1/api/vega-backend/v1/catalogs/{ids}',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.get('/api/vega-backend/v1/api/vega-backend/v1/catalogs/{ids}', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('GET','/api/vega-backend/v1/api/vega-backend/v1/catalogs/{ids}', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/catalogs/{ids}");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("GET");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("GET", "/api/vega-backend/v1/api/vega-backend/v1/catalogs/{ids}", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`GET /api/vega-backend/v1/catalogs/{ids}`

路径参数支持单条或批量（逗号分隔）；批量时不存在的 ID 不会报错，结果中按存在的返回。
每条 `Catalog` 含 **`extensions`**（无副表行时为 `{}`）。

<h3 id="获取-catalog-详情-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|ids|path|string|true|catalog ID，多个用英文逗号分隔（如 `id1,id2,id3`）|

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
      "description": "string",
      "type": "physical",
      "enabled": true,
      "connector_type": "string",
      "connector_config": {},
      "metadata": {},
      "extensions": {
        "property1": "string",
        "property2": "string"
      },
      "health_check_enabled": true,
      "health_check_status": "healthy",
      "last_check_time": 0,
      "health_check_result": "string",
      "creator": {
        "id": "string",
        "type": "string"
      },
      "create_time": 0,
      "updater": {
        "id": "string",
        "type": "string"
      },
      "update_time": 0,
      "operations": [
        "string"
      ]
    }
  ]
}
```

<h3 id="获取-catalog-详情-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|Inline|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="获取-catalog-详情-responseschema">Response Schema</h3>

Status Code **200**

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» entries|[[Catalog](#schemacatalog)]|false|none|[Catalog（数据目录）实体]|
|»» id|string|true|none|catalog ID；小写字母、数字、下划线、连字符，不能以下划线开头，最大长度 40|
|»» name|string|true|none|catalog 名称，必填，全局唯一，最大长度 255|
|»» tags|[string]|false|none|标签列表（可选），最多 5 个，每个标签非空且最大长度 40，不能包含特殊字符|
|»» description|string|false|none|描述，最大长度 1000|
|»» type|string|true|none|catalog 类型|
|»» enabled|boolean|true|none|是否启用|
|»» connector_type|string|true|none|连接器类型标识（参见 connector-type.yaml）|
|»» connector_config|object|false|none|连接器配置（结构由 ConnectorType.field_config 决定）|
|»» metadata|object|false|none|扩展元信息|
|»» extensions|[EntityExtensions](#schemaentityextensions)|false|none|扁平 KV 的 JSON object 形态（`string`→`string`）。用于 **`Catalog` / `CatalogRequest` / `CatalogRef`**<br>上的 **`extensions`** 属性。<br><br>存于 `t_entity_extension`；与 `tags` 语义独立。无数据时 JSON 为 `{}`。单实体条数、键值长度以服务端配置为准<br>（建议 ≤64 条；key ≤128；value ≤512 UTF-8）；禁止 key 以 `vega_` 开头（平台保留）。<br><br>**列表**：`include_extensions` 为 true 时，条目返回 `extensions`；默认 false 时可省略以减小负载。<br>`include_extension_keys` 非空时仅投影所列 key。<br><br>**请求体（POST/PUT）**：根对象出现 **`extensions` 键**（含 `{}`）即触发该实体 KV **整包替换**；<br>**键未出现**则不修改副表（与 `info.description` 一致）。|
|»»» **additionalProperties**|string|false|none|none|
|»» health_check_enabled|boolean|false|none|是否开启周期健康检查|
|»» health_check_status|string|false|none|最近一次健康检查状态|
|»» last_check_time|integer(int64)|false|none|最近一次健康检查时间（毫秒时间戳）|
|»» health_check_result|string|false|none|最近一次健康检查详情|
|»» creator|[AccountInfo](#schemaaccountinfo)|false|none|账号信息|
|»»» id|string|false|none|none|
|»»» type|string|false|none|账号类型（user / app / anonymous 等）|
|»» create_time|integer(int64)|false|none|none|
|»» updater|[AccountInfo](#schemaaccountinfo)|false|none|账号信息|
|»» update_time|integer(int64)|false|none|none|
|»» operations|[string]|false|none|当前用户对该 catalog 的操作权限集合|

#### Enumerated Values

|Property|Value|
|---|---|
|type|physical|
|type|logical|
|health_check_status|healthy|
|health_check_status|degraded|
|health_check_status|unhealthy|
|health_check_status|offline|
|health_check_status|unchecked|

<aside class="success">
This operation does not require authentication
</aside>

## 删除 catalog

> Code samples

```shell
# You can also use wget
curl -X DELETE /api/vega-backend/v1/api/vega-backend/v1/catalogs/{ids} \
  -H 'Accept: application/json'

```

```http
DELETE /api/vega-backend/v1/api/vega-backend/v1/catalogs/{ids} HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/catalogs/{ids}',
{
  method: 'DELETE',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.delete '/api/vega-backend/v1/api/vega-backend/v1/catalogs/{ids}',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.delete('/api/vega-backend/v1/api/vega-backend/v1/catalogs/{ids}', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('DELETE','/api/vega-backend/v1/api/vega-backend/v1/catalogs/{ids}', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/catalogs/{ids}");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("DELETE");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("DELETE", "/api/vega-backend/v1/api/vega-backend/v1/catalogs/{ids}", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`DELETE /api/vega-backend/v1/catalogs/{ids}`

路径参数支持单条或批量（逗号分隔）。删除会同步移除该 catalog 下的资源、调度等关联记录，
并删除 `t_entity_extension` 中 `f_entity_id` 等于被删 catalog / resource `f_id` 的行（应用层同事务）。

<h3 id="删除-catalog-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|ids|path|string|true|catalog ID，多个用英文逗号分隔（如 `id1,id2,id3`）|

> Example responses

<h3 id="删除-catalog-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|删除成功|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|资源不存在|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="删除-catalog-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 修改 catalog

> Code samples

```shell
# You can also use wget
curl -X PUT /api/vega-backend/v1/api/vega-backend/v1/catalogs/{id} \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json'

```

```http
PUT /api/vega-backend/v1/api/vega-backend/v1/catalogs/{id} HTTP/1.1

Content-Type: application/json
Accept: application/json

```

```javascript
const inputBody = '{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "description": "string",
  "enabled": true,
  "connector_type": "string",
  "connector_config": {},
  "extensions": {
    "property1": "string",
    "property2": "string"
  }
}';
const headers = {
  'Content-Type':'application/json',
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}',
{
  method: 'PUT',
  body: inputBody,
  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Content-Type' => 'application/json',
  'Accept' => 'application/json'
}

result = RestClient.put '/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Content-Type': 'application/json',
  'Accept': 'application/json'
}

r = requests.put('/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Content-Type' => 'application/json',
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('PUT','/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("PUT");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Content-Type": []string{"application/json"},
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("PUT", "/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`PUT /api/vega-backend/v1/catalogs/{id}`

全量更新指定 catalog。catalog 由路径参数 `id` 唯一确定（主键不可改）。

请求体的 `id` 字段**必填**，且必须与路径参数完全一致：
- 缺失 / 格式非法 → 400 `VegaBackend.InvalidParameter.ID`。
- 不一致 → 409 `VegaBackend.Catalog.IDMismatch`。

`name` 改动后若新名称已被其它 catalog 占用，返回 409 `VegaBackend.Catalog.NameExists`。

`enabled` 不能通过 PUT 切换；请求体中的 `enabled` 必须与当前 catalog 状态一致。
如需启用/禁用，使用 `POST /catalogs/{id}/enable` 或 `POST /catalogs/{id}/disable`。

`connector_config` 可修改；修改影响发现范围的字段后，旧 Resource 会在下一次
discover 对齐时标记为 `stale`。

**extensions**：请求体若包含 **`extensions` 键**（含空对象 `{}`），则对该 catalog **整包替换**
`t_entity_extension` 中 `f_entity_id = id` 的全部行；**键未出现**则不修改副表。

> Body parameter

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "description": "string",
  "enabled": true,
  "connector_type": "string",
  "connector_config": {},
  "extensions": {
    "property1": "string",
    "property2": "string"
  }
}
```

<h3 id="修改-catalog-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|body|body|[CatalogRequest](#schemacatalogrequest)|true|none|
|id|path|string|true|catalog ID|

> Example responses

<h3 id="修改-catalog-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|更新成功|None|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数 / 请求体非法。常见 errcode（命名以合入代码为准）：
- `VegaBackend.InvalidParameter.RequestBody`：body schema 不合法
- `VegaBackend.InvalidParameter.ID`：`id` 缺失或格式非法（小写字母/数字/`_`/`-`，首字符须为字母或数字，长度 ≤40）
- `VegaBackend.Extensions.InvalidFormat`：`extensions` 非 object 或 key/value 非 string
- `VegaBackend.Extensions.QuotaExceeded`：条数或长度超限
- `VegaBackend.Extensions.ReservedKey`：key 以 `vega_` 开头
- `VegaBackend.Extensions.MismatchedQueryPairs`：`extension_key` / `extension_value` 数组长度不一致或未成对|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|资源不存在|None|
|406|[Not Acceptable](https://tools.ietf.org/html/rfc7231#section-6.5.6)|Content-Type 不是 application/json|None|
|409|[Conflict](https://tools.ietf.org/html/rfc7231#section-6.5.8)|主键 / 名称冲突。可能的错误码：
- `VegaBackend.Catalog.IDMismatch`
- `VegaBackend.Catalog.NameExists`
- `VegaBackend.Catalog.InvalidParameter`（尝试通过 PUT 切换 enabled）|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="修改-catalog-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 启用 catalog

> Code samples

```shell
# You can also use wget
curl -X POST /api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/enable \
  -H 'Accept: application/json'

```

```http
POST /api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/enable HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/enable',
{
  method: 'POST',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.post '/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/enable',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.post('/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/enable', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('POST','/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/enable', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/enable");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("POST");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("POST", "/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/enable", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`POST /api/vega-backend/v1/catalogs/{id}/enable`

启用 catalog。接口幂等：已启用的 catalog 再次启用返回 204。
从禁用切换为启用时，`health_check_status` 重置为 `unchecked`。

<h3 id="启用-catalog-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|id|path|string|true|catalog ID|

> Example responses

<h3 id="启用-catalog-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|启用成功或已启用|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|资源不存在|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="启用-catalog-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 禁用 catalog

> Code samples

```shell
# You can also use wget
curl -X POST /api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/disable \
  -H 'Accept: application/json'

```

```http
POST /api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/disable HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/disable',
{
  method: 'POST',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.post '/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/disable',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.post('/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/disable', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('POST','/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/disable', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/disable");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("POST");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("POST", "/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/disable", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`POST /api/vega-backend/v1/catalogs/{id}/disable`

禁用 catalog。接口幂等：已禁用的 catalog 再次禁用返回 204。
禁用不会覆盖 `health_check_status`，健康状态保留最近一次检查结果。

<h3 id="禁用-catalog-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|id|path|string|true|catalog ID|

> Example responses

<h3 id="禁用-catalog-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|禁用成功或已禁用|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|资源不存在|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="禁用-catalog-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 获取 catalog 健康状态

> Code samples

```shell
# You can also use wget
curl -X GET /api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/health-status \
  -H 'Accept: application/json'

```

```http
GET /api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/health-status HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/health-status',
{
  method: 'GET',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.get '/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/health-status',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.get('/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/health-status', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('GET','/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/health-status', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/health-status");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("GET");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("GET", "/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/health-status", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`GET /api/vega-backend/v1/catalogs/{id}/health-status`

返回指定 catalog 的最近一次健康检查结果（状态 + 时间 + 详情）。

<h3 id="获取-catalog-健康状态-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|id|path|string|true|catalog ID|

> Example responses

> 200 Response

```json
{
  "id": "string",
  "health_check_status": "healthy",
  "last_check_time": 0,
  "health_check_result": "string"
}
```

<h3 id="获取-catalog-健康状态-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[CatalogHealthStatusEntry](#schemacataloghealthstatusentry)|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="获取-catalog-健康状态-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 测试 catalog 连接

> Code samples

```shell
# You can also use wget
curl -X POST /api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/test-connection \
  -H 'Accept: application/json'

```

```http
POST /api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/test-connection HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/test-connection',
{
  method: 'POST',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.post '/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/test-connection',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.post('/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/test-connection', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('POST','/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/test-connection', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/test-connection");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("POST");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("POST", "/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/test-connection", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`POST /api/vega-backend/v1/catalogs/{id}/test-connection`

以当前持久化的 `connector_config` 真实建立一次到数据源的连接，验证连通性。
与 `health-status`（异步定时检查的最近结果）不同，本端点是**同步实时探测**。

探测结果通过响应体 `success` 字段返回；连不通**不**视为 HTTP 错误：
- 连通 → 200 `{ success: true, message }`
- 连不通 → 200 `{ success: false, message }`
- 非 2xx 仅用于"请求 / 鉴权 / 服务内部"问题，与目标连通性无关。

<h3 id="测试-catalog-连接-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|id|path|string|true|catalog ID|

> Example responses

> 200 Response

```json
{
  "success": true,
  "message": "string"
}
```

<h3 id="测试-catalog-连接-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|探测完成（成功或失败均返回，业务结果看 `success` 字段）|[TestConnectionResult](#schematestconnectionresult)|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|资源不存在|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="测试-catalog-连接-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

# Schemas

<h2 id="tocS_EntityExtensions">EntityExtensions</h2>
<!-- backwards compatibility -->
<a id="schemaentityextensions"></a>
<a id="schema_EntityExtensions"></a>
<a id="tocSentityextensions"></a>
<a id="tocsentityextensions"></a>

```json
{
  "property1": "string",
  "property2": "string"
}

```

扁平 KV 的 JSON object 形态（`string`→`string`）。用于 **`Catalog` / `CatalogRequest` / `CatalogRef`**
上的 **`extensions`** 属性。

存于 `t_entity_extension`；与 `tags` 语义独立。无数据时 JSON 为 `{}`。单实体条数、键值长度以服务端配置为准
（建议 ≤64 条；key ≤128；value ≤512 UTF-8）；禁止 key 以 `vega_` 开头（平台保留）。

**列表**：`include_extensions` 为 true 时，条目返回 `extensions`；默认 false 时可省略以减小负载。
`include_extension_keys` 非空时仅投影所列 key。

**请求体（POST/PUT）**：根对象出现 **`extensions` 键**（含 `{}`）即触发该实体 KV **整包替换**；
**键未出现**则不修改副表（与 `info.description` 一致）。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|**additionalProperties**|string|false|none|none|

<h2 id="tocS_AccountInfo">AccountInfo</h2>
<!-- backwards compatibility -->
<a id="schemaaccountinfo"></a>
<a id="schema_AccountInfo"></a>
<a id="tocSaccountinfo"></a>
<a id="tocsaccountinfo"></a>

```json
{
  "id": "string",
  "type": "string"
}

```

账号信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|false|none|none|
|type|string|false|none|账号类型（user / app / anonymous 等）|

<h2 id="tocS_Catalog">Catalog</h2>
<!-- backwards compatibility -->
<a id="schemacatalog"></a>
<a id="schema_Catalog"></a>
<a id="tocScatalog"></a>
<a id="tocscatalog"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "description": "string",
  "type": "physical",
  "enabled": true,
  "connector_type": "string",
  "connector_config": {},
  "metadata": {},
  "extensions": {
    "property1": "string",
    "property2": "string"
  },
  "health_check_enabled": true,
  "health_check_status": "healthy",
  "last_check_time": 0,
  "health_check_result": "string",
  "creator": {
    "id": "string",
    "type": "string"
  },
  "create_time": 0,
  "updater": {
    "id": "string",
    "type": "string"
  },
  "update_time": 0,
  "operations": [
    "string"
  ]
}

```

Catalog（数据目录）实体

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|catalog ID；小写字母、数字、下划线、连字符，不能以下划线开头，最大长度 40|
|name|string|true|none|catalog 名称，必填，全局唯一，最大长度 255|
|tags|[string]|false|none|标签列表（可选），最多 5 个，每个标签非空且最大长度 40，不能包含特殊字符|
|description|string|false|none|描述，最大长度 1000|
|type|string|true|none|catalog 类型|
|enabled|boolean|true|none|是否启用|
|connector_type|string|true|none|连接器类型标识（参见 connector-type.yaml）|
|connector_config|object|false|none|连接器配置（结构由 ConnectorType.field_config 决定）|
|metadata|object|false|none|扩展元信息|
|extensions|[EntityExtensions](#schemaentityextensions)|false|none|扁平 KV 的 JSON object 形态（`string`→`string`）。用于 **`Catalog` / `CatalogRequest` / `CatalogRef`**<br>上的 **`extensions`** 属性。<br><br>存于 `t_entity_extension`；与 `tags` 语义独立。无数据时 JSON 为 `{}`。单实体条数、键值长度以服务端配置为准<br>（建议 ≤64 条；key ≤128；value ≤512 UTF-8）；禁止 key 以 `vega_` 开头（平台保留）。<br><br>**列表**：`include_extensions` 为 true 时，条目返回 `extensions`；默认 false 时可省略以减小负载。<br>`include_extension_keys` 非空时仅投影所列 key。<br><br>**请求体（POST/PUT）**：根对象出现 **`extensions` 键**（含 `{}`）即触发该实体 KV **整包替换**；<br>**键未出现**则不修改副表（与 `info.description` 一致）。|
|health_check_enabled|boolean|false|none|是否开启周期健康检查|
|health_check_status|string|false|none|最近一次健康检查状态|
|last_check_time|integer(int64)|false|none|最近一次健康检查时间（毫秒时间戳）|
|health_check_result|string|false|none|最近一次健康检查详情|
|creator|[AccountInfo](#schemaaccountinfo)|false|none|账号信息|
|create_time|integer(int64)|false|none|none|
|updater|[AccountInfo](#schemaaccountinfo)|false|none|账号信息|
|update_time|integer(int64)|false|none|none|
|operations|[string]|false|none|当前用户对该 catalog 的操作权限集合|

#### Enumerated Values

|Property|Value|
|---|---|
|type|physical|
|type|logical|
|health_check_status|healthy|
|health_check_status|degraded|
|health_check_status|unhealthy|
|health_check_status|offline|
|health_check_status|unchecked|

<h2 id="tocS_CatalogRequest">CatalogRequest</h2>
<!-- backwards compatibility -->
<a id="schemacatalogrequest"></a>
<a id="schema_CatalogRequest"></a>
<a id="tocScatalogrequest"></a>
<a id="tocscatalogrequest"></a>

```json
{
  "id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "description": "string",
  "enabled": true,
  "connector_type": "string",
  "connector_config": {},
  "extensions": {
    "property1": "string",
    "property2": "string"
  }
}

```

catalog 创建 / 更新请求体。

- POST：`id` 可省略，由后端生成；若携带且已存在，返回 409 `VegaBackend.Catalog.IdExists`。
- PUT：`id` **必填**，且必须与路径参数 `id` 完全一致。缺失或格式非法返回 400 `VegaBackend.InvalidParameter.ID`，不一致返回 409 `VegaBackend.Catalog.IDMismatch`。
- `enabled` 使用 boolean 零值语义：未传按 `false` 处理。PUT 时该值必须与当前 catalog 状态一致；启停切换请使用专用 enable/disable 接口。

可选 **`extensions`**（见 `EntityExtensions`）：写入规则见 `info.description`。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|false|none|catalog ID；POST 时可省略由后端生成，PUT 时必填且必须与路径参数一致；小写字母、数字、下划线、连字符，不能以下划线开头，最大长度 40|
|name|string|true|none|catalog 名称，必填，全局唯一，最大长度 255|
|tags|[string]|false|none|标签列表（可选），最多 5 个，每个标签非空且最大长度 40，不能包含特殊字符|
|description|string|false|none|描述，最大长度 1000|
|enabled|boolean|true|none|是否启用；未传按 false 处理。PUT 不允许切换该字段，启停切换请使用专用 enable/disable 接口。|
|connector_type|string|true|none|连接器类型标识；必须是已注册且 enabled 的类型|
|connector_config|object|false|none|连接器配置；字段由 ConnectorType.field_config 决定|
|extensions|[EntityExtensions](#schemaentityextensions)|false|none|扁平 KV 的 JSON object 形态（`string`→`string`）。用于 **`Catalog` / `CatalogRequest` / `CatalogRef`**<br>上的 **`extensions`** 属性。<br><br>存于 `t_entity_extension`；与 `tags` 语义独立。无数据时 JSON 为 `{}`。单实体条数、键值长度以服务端配置为准<br>（建议 ≤64 条；key ≤128；value ≤512 UTF-8）；禁止 key 以 `vega_` 开头（平台保留）。<br><br>**列表**：`include_extensions` 为 true 时，条目返回 `extensions`；默认 false 时可省略以减小负载。<br>`include_extension_keys` 非空时仅投影所列 key。<br><br>**请求体（POST/PUT）**：根对象出现 **`extensions` 键**（含 `{}`）即触发该实体 KV **整包替换**；<br>**键未出现**则不修改副表（与 `info.description` 一致）。|

<h2 id="tocS_ListCatalogs">ListCatalogs</h2>
<!-- backwards compatibility -->
<a id="schemalistcatalogs"></a>
<a id="schema_ListCatalogs"></a>
<a id="tocSlistcatalogs"></a>
<a id="tocslistcatalogs"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "description": "string",
      "type": "physical",
      "enabled": true,
      "connector_type": "string",
      "connector_config": {},
      "metadata": {},
      "extensions": {
        "property1": "string",
        "property2": "string"
      },
      "health_check_enabled": true,
      "health_check_status": "healthy",
      "last_check_time": 0,
      "health_check_result": "string",
      "creator": {
        "id": "string",
        "type": "string"
      },
      "create_time": 0,
      "updater": {
        "id": "string",
        "type": "string"
      },
      "update_time": 0,
      "operations": [
        "string"
      ]
    }
  ],
  "total_count": 0
}

```

catalog 列表

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[Catalog](#schemacatalog)]|true|none|[Catalog（数据目录）实体]|
|total_count|integer(int64)|true|none|none|

<h2 id="tocS_CatalogRef">CatalogRef</h2>
<!-- backwards compatibility -->
<a id="schemacatalogref"></a>
<a id="schema_CatalogRef"></a>
<a id="tocScatalogref"></a>
<a id="tocscatalogref"></a>

```json
{
  "id": "string",
  "extensions": {
    "property1": "string",
    "property2": "string"
  }
}

```

catalog 引用（创建成功响应）；含 `extensions`

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|none|
|extensions|[EntityExtensions](#schemaentityextensions)|false|none|扁平 KV 的 JSON object 形态（`string`→`string`）。用于 **`Catalog` / `CatalogRequest` / `CatalogRef`**<br>上的 **`extensions`** 属性。<br><br>存于 `t_entity_extension`；与 `tags` 语义独立。无数据时 JSON 为 `{}`。单实体条数、键值长度以服务端配置为准<br>（建议 ≤64 条；key ≤128；value ≤512 UTF-8）；禁止 key 以 `vega_` 开头（平台保留）。<br><br>**列表**：`include_extensions` 为 true 时，条目返回 `extensions`；默认 false 时可省略以减小负载。<br>`include_extension_keys` 非空时仅投影所列 key。<br><br>**请求体（POST/PUT）**：根对象出现 **`extensions` 键**（含 `{}`）即触发该实体 KV **整包替换**；<br>**键未出现**则不修改副表（与 `info.description` 一致）。|

<h2 id="tocS_CatalogHealthStatusEntry">CatalogHealthStatusEntry</h2>
<!-- backwards compatibility -->
<a id="schemacataloghealthstatusentry"></a>
<a id="schema_CatalogHealthStatusEntry"></a>
<a id="tocScataloghealthstatusentry"></a>
<a id="tocscataloghealthstatusentry"></a>

```json
{
  "id": "string",
  "health_check_status": "healthy",
  "last_check_time": 0,
  "health_check_result": "string"
}

```

catalog 健康状态条目

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|catalog ID|
|health_check_status|string|true|none|none|
|last_check_time|integer(int64)|false|none|最近一次检查时间（毫秒时间戳）|
|health_check_result|string|false|none|检查详情 / 错误信息|

#### Enumerated Values

|Property|Value|
|---|---|
|health_check_status|healthy|
|health_check_status|degraded|
|health_check_status|unhealthy|
|health_check_status|offline|
|health_check_status|unchecked|

<h2 id="tocS_TestConnectionResult">TestConnectionResult</h2>
<!-- backwards compatibility -->
<a id="schematestconnectionresult"></a>
<a id="schema_TestConnectionResult"></a>
<a id="tocStestconnectionresult"></a>
<a id="tocstestconnectionresult"></a>

```json
{
  "success": true,
  "message": "string"
}

```

连接测试结果

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|success|boolean|true|none|是否连通|
|message|string|false|none|详情（失败时为错误信息）|



<!-- Generator: Widdershins v4.0.1 -->

<h1 id="connectortype">ConnectorType v0.1.0</h1>

> Scroll down for code samples, example requests and responses. Select a language for code samples from the tabs above or the mobile navigation menu.

Vega Backend 连接器类型（ConnectorType）相关 API。

连接器类型描述一个可注册的数据源驱动（如 mysql、postgresql、opensearch 等），
提供其元信息、字段配置（field_config，兼容 JSON Schema properties）以及
启用状态。Mode 区分本地（local，进程内运行）与远程（remote，独立服务通过
HTTP 调用），Category 区分数据源大类。

Base URLs:

* <a href="/api/vega-backend/v1">/api/vega-backend/v1</a>

<h1 id="connectortype-default">Default</h1>

## 获取连接器类型列表

> Code samples

```shell
# You can also use wget
curl -X GET /api/vega-backend/v1/api/vega-backend/v1/connector-types \
  -H 'Accept: application/json'

```

```http
GET /api/vega-backend/v1/api/vega-backend/v1/connector-types HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/connector-types',
{
  method: 'GET',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.get '/api/vega-backend/v1/api/vega-backend/v1/connector-types',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.get('/api/vega-backend/v1/api/vega-backend/v1/connector-types', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('GET','/api/vega-backend/v1/api/vega-backend/v1/connector-types', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/connector-types");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("GET");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("GET", "/api/vega-backend/v1/api/vega-backend/v1/connector-types", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`GET /api/vega-backend/v1/connector-types`

分页获取已注册的连接器类型；支持按 name、tag、mode、category、enabled 过滤。

<h3 id="获取连接器类型列表-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|name|query|string|false|按名称模糊过滤，匹配名称中包含该值的连接器类型|
|tag|query|string|false|按标签精确过滤，默认为空|
|mode|query|string|false|按运行模式过滤|
|category|query|string|false|按分类过滤|
|enabled|query|boolean|false|按启用状态过滤；不传表示不过滤|
|offset|query|integer(int64)|false|分页偏移量，>=0，默认 0|
|limit|query|integer(int64)|false|每页数量，1-1000，-1 表示不分页，默认 20|
|sort|query|string|false|排序字段，默认 name|
|direction|query|string|false|排序方向，默认 desc|

#### Enumerated Values

|Parameter|Value|
|---|---|
|mode|local|
|mode|remote|
|category|table|
|category|index|
|category|topic|
|category|file|
|category|fileset|
|category|metric|
|category|api|
|sort|name|
|direction|asc|
|direction|desc|

> Example responses

> 200 Response

```json
{
  "entries": [
    {
      "type": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "description": "string",
      "mode": "local",
      "category": "table",
      "endpoint": "string",
      "field_config": {
        "property1": {
          "name": "string",
          "type": "string",
          "description": "string",
          "required": false,
          "encrypted": false
        },
        "property2": {
          "name": "string",
          "type": "string",
          "description": "string",
          "required": false,
          "encrypted": false
        }
      },
      "enabled": true,
      "operations": [
        "string"
      ]
    }
  ],
  "total_count": 0
}
```

<h3 id="获取连接器类型列表-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[ListConnectorTypes](#schemalistconnectortypes)|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数 / 请求体非法|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="获取连接器类型列表-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 注册连接器类型

> Code samples

```shell
# You can also use wget
curl -X POST /api/vega-backend/v1/api/vega-backend/v1/connector-types \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json'

```

```http
POST /api/vega-backend/v1/api/vega-backend/v1/connector-types HTTP/1.1

Content-Type: application/json
Accept: application/json

```

```javascript
const inputBody = '{
  "type": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "description": "string",
  "mode": "local",
  "category": "table",
  "endpoint": "string",
  "field_config": {
    "property1": {
      "name": "string",
      "type": "string",
      "description": "string",
      "required": false,
      "encrypted": false
    },
    "property2": {
      "name": "string",
      "type": "string",
      "description": "string",
      "required": false,
      "encrypted": false
    }
  },
  "enabled": false
}';
const headers = {
  'Content-Type':'application/json',
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/connector-types',
{
  method: 'POST',
  body: inputBody,
  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Content-Type' => 'application/json',
  'Accept' => 'application/json'
}

result = RestClient.post '/api/vega-backend/v1/api/vega-backend/v1/connector-types',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Content-Type': 'application/json',
  'Accept': 'application/json'
}

r = requests.post('/api/vega-backend/v1/api/vega-backend/v1/connector-types', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Content-Type' => 'application/json',
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('POST','/api/vega-backend/v1/api/vega-backend/v1/connector-types', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/connector-types");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("POST");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Content-Type": []string{"application/json"},
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("POST", "/api/vega-backend/v1/api/vega-backend/v1/connector-types", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`POST /api/vega-backend/v1/connector-types`

创建一个新的连接器类型。`type` 必须唯一，已存在时返回 409。

> Body parameter

```json
{
  "type": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "description": "string",
  "mode": "local",
  "category": "table",
  "endpoint": "string",
  "field_config": {
    "property1": {
      "name": "string",
      "type": "string",
      "description": "string",
      "required": false,
      "encrypted": false
    },
    "property2": {
      "name": "string",
      "type": "string",
      "description": "string",
      "required": false,
      "encrypted": false
    }
  },
  "enabled": false
}
```

<h3 id="注册连接器类型-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|body|body|[ConnectorTypeReq](#schemaconnectortypereq)|true|none|

> Example responses

> 201 Response

```json
{
  "type": "string"
}
```

<h3 id="注册连接器类型-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|201|[Created](https://tools.ietf.org/html/rfc7231#section-6.3.2)|创建成功|[ConnectorTypeRef](#schemaconnectortyperef)|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数 / 请求体非法|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|406|[Not Acceptable](https://tools.ietf.org/html/rfc7231#section-6.5.6)|Content-Type 不是 application/json|None|
|409|[Conflict](https://tools.ietf.org/html/rfc7231#section-6.5.8)|同 type 的连接器类型已存在|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="注册连接器类型-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 获取连接器类型详情

> Code samples

```shell
# You can also use wget
curl -X GET /api/vega-backend/v1/api/vega-backend/v1/connector-types/{type} \
  -H 'Accept: application/json'

```

```http
GET /api/vega-backend/v1/api/vega-backend/v1/connector-types/{type} HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}',
{
  method: 'GET',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.get '/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.get('/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('GET','/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("GET");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("GET", "/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`GET /api/vega-backend/v1/connector-types/{type}`

<h3 id="获取连接器类型详情-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|type|path|string|true|连接器类型标识（如 mysql、postgresql）|

> Example responses

> 200 Response

```json
{
  "type": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "description": "string",
  "mode": "local",
  "category": "table",
  "endpoint": "string",
  "field_config": {
    "property1": {
      "name": "string",
      "type": "string",
      "description": "string",
      "required": false,
      "encrypted": false
    },
    "property2": {
      "name": "string",
      "type": "string",
      "description": "string",
      "required": false,
      "encrypted": false
    }
  },
  "enabled": true,
  "operations": [
    "string"
  ]
}
```

<h3 id="获取连接器类型详情-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[ConnectorType](#schemaconnectortype)|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|资源不存在|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="获取连接器类型详情-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 修改连接器类型

> Code samples

```shell
# You can also use wget
curl -X PUT /api/vega-backend/v1/api/vega-backend/v1/connector-types/{type} \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json'

```

```http
PUT /api/vega-backend/v1/api/vega-backend/v1/connector-types/{type} HTTP/1.1

Content-Type: application/json
Accept: application/json

```

```javascript
const inputBody = '{
  "type": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "description": "string",
  "mode": "local",
  "category": "table",
  "endpoint": "string",
  "field_config": {
    "property1": {
      "name": "string",
      "type": "string",
      "description": "string",
      "required": false,
      "encrypted": false
    },
    "property2": {
      "name": "string",
      "type": "string",
      "description": "string",
      "required": false,
      "encrypted": false
    }
  },
  "enabled": false
}';
const headers = {
  'Content-Type':'application/json',
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}',
{
  method: 'PUT',
  body: inputBody,
  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Content-Type' => 'application/json',
  'Accept' => 'application/json'
}

result = RestClient.put '/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Content-Type': 'application/json',
  'Accept': 'application/json'
}

r = requests.put('/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Content-Type' => 'application/json',
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('PUT','/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("PUT");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Content-Type": []string{"application/json"},
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("PUT", "/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`PUT /api/vega-backend/v1/connector-types/{type}`

全量更新指定连接器类型。连接器类型由路径参数 `type` 唯一确定（主键不可改）。

请求体的 `type` 字段**必填**，且必须与路径参数完全一致：
- 缺失 → 400 `VegaBackend.ConnectorType.InvalidParameter.Type`。
- 不一致 → 409 `VegaBackend.ConnectorType.TypeMismatch`。

修改 `name` 时若新名称已被其它类型占用，返回 409 `VegaBackend.ConnectorType.NameExists`。

> Body parameter

```json
{
  "type": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "description": "string",
  "mode": "local",
  "category": "table",
  "endpoint": "string",
  "field_config": {
    "property1": {
      "name": "string",
      "type": "string",
      "description": "string",
      "required": false,
      "encrypted": false
    },
    "property2": {
      "name": "string",
      "type": "string",
      "description": "string",
      "required": false,
      "encrypted": false
    }
  },
  "enabled": false
}
```

<h3 id="修改连接器类型-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|body|body|[ConnectorTypeReq](#schemaconnectortypereq)|true|none|
|type|path|string|true|连接器类型标识（如 mysql、postgresql）|

> Example responses

<h3 id="修改连接器类型-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|更新成功|None|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数 / 请求体非法|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|资源不存在|None|
|406|[Not Acceptable](https://tools.ietf.org/html/rfc7231#section-6.5.6)|Content-Type 不是 application/json|None|
|409|[Conflict](https://tools.ietf.org/html/rfc7231#section-6.5.8)|主键 / 名称冲突。具体场景：
- 请求体 `type` 与路径参数不一致 → `VegaBackend.ConnectorType.TypeMismatch`
- 新 `name` 已被其它连接器类型占用 → `VegaBackend.ConnectorType.NameExists`|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="修改连接器类型-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 删除连接器类型

> Code samples

```shell
# You can also use wget
curl -X DELETE /api/vega-backend/v1/api/vega-backend/v1/connector-types/{type} \
  -H 'Accept: application/json'

```

```http
DELETE /api/vega-backend/v1/api/vega-backend/v1/connector-types/{type} HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}',
{
  method: 'DELETE',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.delete '/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.delete('/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('DELETE','/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("DELETE");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("DELETE", "/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`DELETE /api/vega-backend/v1/connector-types/{type}`

删除指定 type 的连接器类型。`mode=local` 的内置类型不可删除（403）。

<h3 id="删除连接器类型-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|type|path|string|true|连接器类型标识（如 mysql、postgresql）|

> Example responses

<h3 id="删除连接器类型-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|删除成功|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|403|[Forbidden](https://tools.ietf.org/html/rfc7231#section-6.5.3)|不可删除（如 local 内置类型）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|资源不存在|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="删除连接器类型-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 启用连接器类型

> Code samples

```shell
# You can also use wget
curl -X POST /api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}/enable \
  -H 'Accept: application/json'

```

```http
POST /api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}/enable HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}/enable',
{
  method: 'POST',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.post '/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}/enable',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.post('/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}/enable', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('POST','/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}/enable', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}/enable");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("POST");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("POST", "/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}/enable", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`POST /api/vega-backend/v1/connector-types/{type}/enable`

启用指定连接器类型；幂等，重复调用不报错。

<h3 id="启用连接器类型-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|type|path|string|true|连接器类型标识|

> Example responses

<h3 id="启用连接器类型-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|启用成功|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|资源不存在|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="启用连接器类型-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 停用连接器类型

> Code samples

```shell
# You can also use wget
curl -X POST /api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}/disable \
  -H 'Accept: application/json'

```

```http
POST /api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}/disable HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}/disable',
{
  method: 'POST',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.post '/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}/disable',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.post('/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}/disable', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('POST','/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}/disable', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}/disable");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("POST");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("POST", "/api/vega-backend/v1/api/vega-backend/v1/connector-types/{type}/disable", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`POST /api/vega-backend/v1/connector-types/{type}/disable`

停用指定连接器类型；幂等，重复调用不报错。

<h3 id="停用连接器类型-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|type|path|string|true|连接器类型标识|

> Example responses

<h3 id="停用连接器类型-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|停用成功|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|资源不存在|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="停用连接器类型-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

# Schemas

<h2 id="tocS_ConnectorFieldConfig">ConnectorFieldConfig</h2>
<!-- backwards compatibility -->
<a id="schemaconnectorfieldconfig"></a>
<a id="schema_ConnectorFieldConfig"></a>
<a id="tocSconnectorfieldconfig"></a>
<a id="tocsconnectorfieldconfig"></a>

```json
{
  "name": "string",
  "type": "string",
  "description": "string",
  "required": false,
  "encrypted": false
}

```

连接器配置字段元信息（兼容 JSON Schema properties）

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|字段显示名|
|type|string|true|none|字段类型|
|description|string|false|none|字段描述|
|required|boolean|false|none|是否必填|
|encrypted|boolean|false|none|是否需要加密存储（自定义扩展）|

#### Enumerated Values

|Property|Value|
|---|---|
|type|string|
|type|integer|
|type|number|
|type|boolean|
|type|object|
|type|array|

<h2 id="tocS_ConnectorType">ConnectorType</h2>
<!-- backwards compatibility -->
<a id="schemaconnectortype"></a>
<a id="schema_ConnectorType"></a>
<a id="tocSconnectortype"></a>
<a id="tocsconnectortype"></a>

```json
{
  "type": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "description": "string",
  "mode": "local",
  "category": "table",
  "endpoint": "string",
  "field_config": {
    "property1": {
      "name": "string",
      "type": "string",
      "description": "string",
      "required": false,
      "encrypted": false
    },
    "property2": {
      "name": "string",
      "type": "string",
      "description": "string",
      "required": false,
      "encrypted": false
    }
  },
  "enabled": true,
  "operations": [
    "string"
  ]
}

```

已注册的连接器类型

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|type|string|true|none|连接器类型标识，全局唯一|
|name|string|true|none|连接器类型显示名|
|tags|[string]|false|none|标签|
|description|string|false|none|类型描述|
|mode|string|true|none|运行模式|
|category|string|true|none|分类|
|endpoint|string|false|none|仅 remote 模式下的远程服务地址|
|field_config|object|false|none|字段配置（兼容 JSON Schema properties），key 为字段标识|
|» **additionalProperties**|[ConnectorFieldConfig](#schemaconnectorfieldconfig)|false|none|连接器配置字段元信息（兼容 JSON Schema properties）|
|enabled|boolean|true|none|是否启用|
|operations|[string]|false|none|该连接器类型支持的操作集合|

#### Enumerated Values

|Property|Value|
|---|---|
|mode|local|
|mode|remote|
|category|table|
|category|index|
|category|topic|
|category|file|
|category|fileset|
|category|metric|
|category|api|

<h2 id="tocS_ConnectorTypeReq">ConnectorTypeReq</h2>
<!-- backwards compatibility -->
<a id="schemaconnectortypereq"></a>
<a id="schema_ConnectorTypeReq"></a>
<a id="tocSconnectortypereq"></a>
<a id="tocsconnectortypereq"></a>

```json
{
  "type": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "description": "string",
  "mode": "local",
  "category": "table",
  "endpoint": "string",
  "field_config": {
    "property1": {
      "name": "string",
      "type": "string",
      "description": "string",
      "required": false,
      "encrypted": false
    },
    "property2": {
      "name": "string",
      "type": "string",
      "description": "string",
      "required": false,
      "encrypted": false
    }
  },
  "enabled": false
}

```

连接器类型创建 / 更新请求体。

- `type` 在 POST 与 PUT 中都**必填**，且必须自描述资源主键。
- POST：全局唯一，已存在返回 409 `VegaBackend.ConnectorType.TypeExists`。
- PUT：必须与路径参数 `type` **完全一致**，缺失返回 400 `VegaBackend.ConnectorType.InvalidParameter.Type`，不一致返回 409 `VegaBackend.ConnectorType.TypeMismatch`。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|type|string|true|none|连接器类型标识；POST 与 PUT 都必填，PUT 时必须与路径参数一致|
|name|string|true|none|连接器类型显示名|
|tags|[string]|false|none|标签|
|description|string|false|none|类型描述|
|mode|string|true|none|运行模式|
|category|string|true|none|分类|
|endpoint|string|false|none|仅 remote 模式下的远程服务地址|
|field_config|object|false|none|字段配置（兼容 JSON Schema properties），key 为字段标识|
|» **additionalProperties**|[ConnectorFieldConfig](#schemaconnectorfieldconfig)|false|none|连接器配置字段元信息（兼容 JSON Schema properties）|
|enabled|boolean|false|none|是否启用|

#### Enumerated Values

|Property|Value|
|---|---|
|mode|local|
|mode|remote|
|category|table|
|category|index|
|category|topic|
|category|file|
|category|fileset|
|category|metric|
|category|api|

<h2 id="tocS_ListConnectorTypes">ListConnectorTypes</h2>
<!-- backwards compatibility -->
<a id="schemalistconnectortypes"></a>
<a id="schema_ListConnectorTypes"></a>
<a id="tocSlistconnectortypes"></a>
<a id="tocslistconnectortypes"></a>

```json
{
  "entries": [
    {
      "type": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "description": "string",
      "mode": "local",
      "category": "table",
      "endpoint": "string",
      "field_config": {
        "property1": {
          "name": "string",
          "type": "string",
          "description": "string",
          "required": false,
          "encrypted": false
        },
        "property2": {
          "name": "string",
          "type": "string",
          "description": "string",
          "required": false,
          "encrypted": false
        }
      },
      "enabled": true,
      "operations": [
        "string"
      ]
    }
  ],
  "total_count": 0
}

```

连接器类型列表

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[ConnectorType](#schemaconnectortype)]|true|none|条目列表|
|total_count|integer(int64)|true|none|总条数|

<h2 id="tocS_ConnectorTypeRef">ConnectorTypeRef</h2>
<!-- backwards compatibility -->
<a id="schemaconnectortyperef"></a>
<a id="schema_ConnectorTypeRef"></a>
<a id="tocSconnectortyperef"></a>
<a id="tocsconnectortyperef"></a>

```json
{
  "type": "string"
}

```

连接器类型引用

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|type|string|true|none|连接器类型标识|



<!-- Generator: Widdershins v4.0.1 -->

<h1 id="discoverschedule">DiscoverSchedule v0.1.0</h1>

> Scroll down for code samples, example requests and responses. Select a language for code samples from the tabs above or the mobile navigation menu.

Vega Backend 资源发现调度（DiscoverSchedule）相关 API。

DiscoverSchedule 是**cron 配置**实体，不直接承载执行：

- 调度器到点触发后产出一条 `trigger_type=scheduled` 的 DiscoverTask（详见 [discover-task.yaml](discover-task.yaml)）
- 用户通过本规范增删改查 DiscoverSchedule，通过 enable/disable 控制其是否生效
- 一次性发现走 `POST /catalogs/{id}/discover`（[discover-task.yaml](discover-task.yaml) 末尾），不通过本规范

每个 DiscoverSchedule 归属一个 Catalog（多对一）。`catalog_id` 在创建后**不可修改**。

端点设计遵循 [vega-backend/CLAUDE.md] 的"端点设计规则"——状态切换走动作端点
`enable` / `disable`，与 ConnectorType 风格对齐；不暴露 `enabled` 字段为可写。

**错误码新增** `VegaBackend.DiscoverSchedule.*` 系列，与其它资源解耦。

Base URLs:

* <a href="/api/vega-backend/v1">/api/vega-backend/v1</a>

<h1 id="discoverschedule-default">Default</h1>

## 创建发现调度

> Code samples

```shell
# You can also use wget
curl -X POST /api/vega-backend/v1/api/vega-backend/v1/discover-schedules \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json'

```

```http
POST /api/vega-backend/v1/api/vega-backend/v1/discover-schedules HTTP/1.1

Content-Type: application/json
Accept: application/json

```

```javascript
const inputBody = '{
  "id": "string",
  "name": "string",
  "catalog_id": "string",
  "cron_expr": "string",
  "start_time": 0,
  "end_time": 0,
  "enabled": true,
  "strategies": [
    "insert"
  ]
}';
const headers = {
  'Content-Type':'application/json',
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/discover-schedules',
{
  method: 'POST',
  body: inputBody,
  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Content-Type' => 'application/json',
  'Accept' => 'application/json'
}

result = RestClient.post '/api/vega-backend/v1/api/vega-backend/v1/discover-schedules',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Content-Type': 'application/json',
  'Accept': 'application/json'
}

r = requests.post('/api/vega-backend/v1/api/vega-backend/v1/discover-schedules', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Content-Type' => 'application/json',
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('POST','/api/vega-backend/v1/api/vega-backend/v1/discover-schedules', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/discover-schedules");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("POST");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Content-Type": []string{"application/json"},
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("POST", "/api/vega-backend/v1/api/vega-backend/v1/discover-schedules", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`POST /api/vega-backend/v1/discover-schedules`

创建 DiscoverSchedule。`name` 与 `catalog_id` 必填且 catalog 必须存在；不存在返回 404
`VegaBackend.Catalog.NotFound`。

- body 中 `enabled=true` 时，创建后立即注册到 cron 引擎；`false` 时仅入库不调度。
- `cron_expr` 必填，标准 5 字段格式；非法返回 400 `VegaBackend.DiscoverSchedule.InvalidCronExpr`。
- `strategies` 可空（表示全部）；非空时元素必须是 `insert` / `delete` / `update` 的子集。
- `start_time` / `end_time` 均 ≥ 0；`end_time>0` 时要求 `start_time ≤ end_time`；非法返回
  400 `VegaBackend.DiscoverSchedule.InvalidTimeRange`。

> Body parameter

```json
{
  "id": "string",
  "name": "string",
  "catalog_id": "string",
  "cron_expr": "string",
  "start_time": 0,
  "end_time": 0,
  "enabled": true,
  "strategies": [
    "insert"
  ]
}
```

<h3 id="创建发现调度-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|body|body|[DiscoverScheduleRequest](#schemadiscoverschedulerequest)|true|none|

> Example responses

> 201 Response

```json
{
  "id": "string"
}
```

<h3 id="创建发现调度-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|201|[Created](https://tools.ietf.org/html/rfc7231#section-6.3.2)|创建成功|Inline|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数 / 请求体非法。常见 errcode：
- `VegaBackend.InvalidParameter.RequestBody`：body schema 不合法
- `VegaBackend.DiscoverSchedule.InvalidCronExpr`：cron 表达式非法
- `VegaBackend.DiscoverSchedule.InvalidStrategies`：strategies 元素非法
- `VegaBackend.DiscoverSchedule.InvalidTimeRange`：`start_time` / `end_time` 非法|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|资源不存在。errcode：
- `VegaBackend.DiscoverSchedule.NotFound`：schedule id 不存在
- `VegaBackend.Catalog.NotFound`：创建时 catalog_id 不存在|None|
|406|[Not Acceptable](https://tools.ietf.org/html/rfc7231#section-6.5.6)|Content-Type 不是 application/json|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误。常见 errcode：
- `VegaBackend.DiscoverSchedule.InternalError.GetFailed`
- `VegaBackend.DiscoverSchedule.InternalError.CreateFailed`
- `VegaBackend.DiscoverSchedule.InternalError.UpdateFailed`
- `VegaBackend.DiscoverSchedule.InternalError.DeleteFailed`
- `VegaBackend.DiscoverSchedule.InternalError.GetAccountNamesFailed`|None|

<h3 id="创建发现调度-responseschema">Response Schema</h3>

Status Code **201**

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» id|string|true|none|新建 DiscoverSchedule 的 ID|

<aside class="success">
This operation does not require authentication
</aside>

## 获取调度列表

> Code samples

```shell
# You can also use wget
curl -X GET /api/vega-backend/v1/api/vega-backend/v1/discover-schedules \
  -H 'Accept: application/json'

```

```http
GET /api/vega-backend/v1/api/vega-backend/v1/discover-schedules HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/discover-schedules',
{
  method: 'GET',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.get '/api/vega-backend/v1/api/vega-backend/v1/discover-schedules',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.get('/api/vega-backend/v1/api/vega-backend/v1/discover-schedules', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('GET','/api/vega-backend/v1/api/vega-backend/v1/discover-schedules', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/discover-schedules");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("GET");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("GET", "/api/vega-backend/v1/api/vega-backend/v1/discover-schedules", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`GET /api/vega-backend/v1/discover-schedules`

分页 + filter 获取 DiscoverSchedule 列表；支持按 name 模糊过滤。

<h3 id="获取调度列表-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|name|query|string|false|按名称模糊过滤，匹配名称中包含该值的调度|
|catalog_id|query|string|false|按归属 catalog 过滤|
|enabled|query|boolean|false|按启用状态过滤；不传表示不过滤|
|offset|query|integer(int64)|false|分页偏移量，>=0，默认 0|
|limit|query|integer(int64)|false|每页数量，1-1000，-1 表示不分页，默认 20|
|sort|query|string|false|排序字段|
|direction|query|string|false|排序方向|

#### Enumerated Values

|Parameter|Value|
|---|---|
|sort|name|
|sort|create_time|
|sort|update_time|
|sort|next_run|
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
      "catalog_id": "string",
      "cron_expr": "string",
      "start_time": 0,
      "end_time": 0,
      "enabled": true,
      "strategies": [
        "insert"
      ],
      "last_run": 0,
      "next_run": 0,
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

<h3 id="获取调度列表-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[ListDiscoverSchedules](#schemalistdiscoverschedules)|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数 / 请求体非法。常见 errcode：
- `VegaBackend.InvalidParameter.RequestBody`：body schema 不合法
- `VegaBackend.DiscoverSchedule.InvalidCronExpr`：cron 表达式非法
- `VegaBackend.DiscoverSchedule.InvalidStrategies`：strategies 元素非法
- `VegaBackend.DiscoverSchedule.InvalidTimeRange`：`start_time` / `end_time` 非法|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误。常见 errcode：
- `VegaBackend.DiscoverSchedule.InternalError.GetFailed`
- `VegaBackend.DiscoverSchedule.InternalError.CreateFailed`
- `VegaBackend.DiscoverSchedule.InternalError.UpdateFailed`
- `VegaBackend.DiscoverSchedule.InternalError.DeleteFailed`
- `VegaBackend.DiscoverSchedule.InternalError.GetAccountNamesFailed`|None|

<h3 id="获取调度列表-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 获取调度详情

> Code samples

```shell
# You can also use wget
curl -X GET /api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id} \
  -H 'Accept: application/json'

```

```http
GET /api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id} HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}',
{
  method: 'GET',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.get '/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.get('/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('GET','/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("GET");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("GET", "/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`GET /api/vega-backend/v1/discover-schedules/{id}`

<h3 id="获取调度详情-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|id|path|string|true|DiscoverSchedule ID|

> Example responses

> 200 Response

```json
{
  "id": "string",
  "name": "string",
  "catalog_id": "string",
  "cron_expr": "string",
  "start_time": 0,
  "end_time": 0,
  "enabled": true,
  "strategies": [
    "insert"
  ],
  "last_run": 0,
  "next_run": 0,
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

<h3 id="获取调度详情-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[DiscoverSchedule](#schemadiscoverschedule)|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|资源不存在。errcode：
- `VegaBackend.DiscoverSchedule.NotFound`：schedule id 不存在
- `VegaBackend.Catalog.NotFound`：创建时 catalog_id 不存在|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误。常见 errcode：
- `VegaBackend.DiscoverSchedule.InternalError.GetFailed`
- `VegaBackend.DiscoverSchedule.InternalError.CreateFailed`
- `VegaBackend.DiscoverSchedule.InternalError.UpdateFailed`
- `VegaBackend.DiscoverSchedule.InternalError.DeleteFailed`
- `VegaBackend.DiscoverSchedule.InternalError.GetAccountNamesFailed`|None|

<h3 id="获取调度详情-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 严格更新调度

> Code samples

```shell
# You can also use wget
curl -X PUT /api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id} \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json'

```

```http
PUT /api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id} HTTP/1.1

Content-Type: application/json
Accept: application/json

```

```javascript
const inputBody = '{
  "id": "string",
  "name": "string",
  "catalog_id": "string",
  "cron_expr": "string",
  "start_time": 0,
  "end_time": 0,
  "enabled": true,
  "strategies": [
    "insert"
  ]
}';
const headers = {
  'Content-Type':'application/json',
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}',
{
  method: 'PUT',
  body: inputBody,
  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Content-Type' => 'application/json',
  'Accept' => 'application/json'
}

result = RestClient.put '/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Content-Type': 'application/json',
  'Accept': 'application/json'
}

r = requests.put('/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Content-Type' => 'application/json',
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('PUT','/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("PUT");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Content-Type": []string{"application/json"},
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("PUT", "/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`PUT /api/vega-backend/v1/discover-schedules/{id}`

**严格全量替换**，**只允许变更"配置"字段**（`name` / `cron_expr` / `strategies` /
`start_time` / `end_time`）。下列字段约束如下：

| 字段 | 约束 | 失败 errcode |
|---|---|---|
| `catalog_id` | 必须显式携带且等于当前值（空串 / 缺失同样视为不一致） | `DiscoverSchedule.CatalogMismatch`（409） |
| `enabled` | 必须与当前一致；状态切换走 `POST .../enable` `disable` | `DiscoverSchedule.EnabledFieldNotAllowed`（409） |

`id` 字段在 body 中携带时被忽略，path 为权威来源。其它系统维护字段
（`last_run` / `next_run` / 时间戳 / 操作者）携带时静默忽略。

若 schedule 当前 `enabled=true`，更新后调度器会重新注册以应用新 cron。

> Body parameter

```json
{
  "id": "string",
  "name": "string",
  "catalog_id": "string",
  "cron_expr": "string",
  "start_time": 0,
  "end_time": 0,
  "enabled": true,
  "strategies": [
    "insert"
  ]
}
```

<h3 id="严格更新调度-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|body|body|[DiscoverScheduleRequest](#schemadiscoverschedulerequest)|true|none|
|id|path|string|true|DiscoverSchedule ID|

> Example responses

<h3 id="严格更新调度-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|更新成功|None|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数 / 请求体非法。常见 errcode：
- `VegaBackend.InvalidParameter.RequestBody`：body schema 不合法
- `VegaBackend.DiscoverSchedule.InvalidCronExpr`：cron 表达式非法
- `VegaBackend.DiscoverSchedule.InvalidStrategies`：strategies 元素非法
- `VegaBackend.DiscoverSchedule.InvalidTimeRange`：`start_time` / `end_time` 非法|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|资源不存在。errcode：
- `VegaBackend.DiscoverSchedule.NotFound`：schedule id 不存在
- `VegaBackend.Catalog.NotFound`：创建时 catalog_id 不存在|None|
|406|[Not Acceptable](https://tools.ietf.org/html/rfc7231#section-6.5.6)|Content-Type 不是 application/json|None|
|409|[Conflict](https://tools.ietf.org/html/rfc7231#section-6.5.8)|PUT 严格更新冲突。errcode：
- `VegaBackend.DiscoverSchedule.CatalogMismatch`
- `VegaBackend.DiscoverSchedule.EnabledFieldNotAllowed`|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误。常见 errcode：
- `VegaBackend.DiscoverSchedule.InternalError.GetFailed`
- `VegaBackend.DiscoverSchedule.InternalError.CreateFailed`
- `VegaBackend.DiscoverSchedule.InternalError.UpdateFailed`
- `VegaBackend.DiscoverSchedule.InternalError.DeleteFailed`
- `VegaBackend.DiscoverSchedule.InternalError.GetAccountNamesFailed`|None|

<h3 id="严格更新调度-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 删除调度

> Code samples

```shell
# You can also use wget
curl -X DELETE /api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id} \
  -H 'Accept: application/json'

```

```http
DELETE /api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id} HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}',
{
  method: 'DELETE',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.delete '/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.delete('/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('DELETE','/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("DELETE");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("DELETE", "/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`DELETE /api/vega-backend/v1/discover-schedules/{id}`

删除 DiscoverSchedule，从 cron 引擎注销并从 DB 删除。

**不删除已有 DiscoverTask 历史**——该 schedule 历史触发的 DiscoverTask 保留
`schedule_id` 字段作为孤儿引用，便于审计追溯。正在执行的 DiscoverTask 不受影响。

<h3 id="删除调度-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|id|path|string|true|DiscoverSchedule ID|

> Example responses

<h3 id="删除调度-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|删除成功|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|资源不存在。errcode：
- `VegaBackend.DiscoverSchedule.NotFound`：schedule id 不存在
- `VegaBackend.Catalog.NotFound`：创建时 catalog_id 不存在|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误。常见 errcode：
- `VegaBackend.DiscoverSchedule.InternalError.GetFailed`
- `VegaBackend.DiscoverSchedule.InternalError.CreateFailed`
- `VegaBackend.DiscoverSchedule.InternalError.UpdateFailed`
- `VegaBackend.DiscoverSchedule.InternalError.DeleteFailed`
- `VegaBackend.DiscoverSchedule.InternalError.GetAccountNamesFailed`|None|

<h3 id="删除调度-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 启用调度

> Code samples

```shell
# You can also use wget
curl -X POST /api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}/enable \
  -H 'Accept: application/json'

```

```http
POST /api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}/enable HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}/enable',
{
  method: 'POST',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.post '/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}/enable',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.post('/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}/enable', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('POST','/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}/enable', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}/enable");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("POST");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("POST", "/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}/enable", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`POST /api/vega-backend/v1/discover-schedules/{id}/enable`

启用 DiscoverSchedule 并注册到 cron 引擎。**幂等**：对已 enable 的 schedule
再 enable 返回 204，不报错。

<h3 id="启用调度-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|id|path|string|true|DiscoverSchedule ID|

> Example responses

<h3 id="启用调度-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|启用成功（含已启用的幂等返回）|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|资源不存在。errcode：
- `VegaBackend.DiscoverSchedule.NotFound`：schedule id 不存在
- `VegaBackend.Catalog.NotFound`：创建时 catalog_id 不存在|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误。常见 errcode：
- `VegaBackend.DiscoverSchedule.InternalError.GetFailed`
- `VegaBackend.DiscoverSchedule.InternalError.CreateFailed`
- `VegaBackend.DiscoverSchedule.InternalError.UpdateFailed`
- `VegaBackend.DiscoverSchedule.InternalError.DeleteFailed`
- `VegaBackend.DiscoverSchedule.InternalError.GetAccountNamesFailed`|None|

<h3 id="启用调度-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 停用调度

> Code samples

```shell
# You can also use wget
curl -X POST /api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}/disable \
  -H 'Accept: application/json'

```

```http
POST /api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}/disable HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}/disable',
{
  method: 'POST',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.post '/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}/disable',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.post('/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}/disable', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('POST','/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}/disable', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}/disable");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("POST");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("POST", "/api/vega-backend/v1/api/vega-backend/v1/discover-schedules/{id}/disable", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`POST /api/vega-backend/v1/discover-schedules/{id}/disable`

停用 DiscoverSchedule 并从 cron 引擎注销。**幂等**：对已 disable 的 schedule
再 disable 返回 204，不报错。

**不影响已经入队 / 正在执行的 DiscoverTask**；这些任务执行时已经持有所需上下文。

<h3 id="停用调度-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|id|path|string|true|DiscoverSchedule ID|

> Example responses

<h3 id="停用调度-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|停用成功（含已停用的幂等返回）|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|资源不存在。errcode：
- `VegaBackend.DiscoverSchedule.NotFound`：schedule id 不存在
- `VegaBackend.Catalog.NotFound`：创建时 catalog_id 不存在|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误。常见 errcode：
- `VegaBackend.DiscoverSchedule.InternalError.GetFailed`
- `VegaBackend.DiscoverSchedule.InternalError.CreateFailed`
- `VegaBackend.DiscoverSchedule.InternalError.UpdateFailed`
- `VegaBackend.DiscoverSchedule.InternalError.DeleteFailed`
- `VegaBackend.DiscoverSchedule.InternalError.GetAccountNamesFailed`|None|

<h3 id="停用调度-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
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

操作者信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|none|
|type|string|true|none|none|
|name|string|false|none|none|

<h2 id="tocS_DiscoverSchedule">DiscoverSchedule</h2>
<!-- backwards compatibility -->
<a id="schemadiscoverschedule"></a>
<a id="schema_DiscoverSchedule"></a>
<a id="tocSdiscoverschedule"></a>
<a id="tocsdiscoverschedule"></a>

```json
{
  "id": "string",
  "name": "string",
  "catalog_id": "string",
  "cron_expr": "string",
  "start_time": 0,
  "end_time": 0,
  "enabled": true,
  "strategies": [
    "insert"
  ],
  "last_run": 0,
  "next_run": 0,
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

发现调度实体

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|全局唯一 ID|
|name|string|true|none|调度名称|
|catalog_id|string|true|none|关联 catalog；创建后**不可修改**|
|cron_expr|string|true|none|标准 5 字段 cron 表达式|
|start_time|integer(int64)|false|none|调度生效起始时间，毫秒时间戳；0 表示无起始限制。<br>调度器到点触发时，若当前时间小于 `start_time` 则跳过本次执行。|
|end_time|integer(int64)|false|none|调度结束时间，毫秒时间戳；0 表示无结束时间。<br>调度器到点触发时，若当前时间大于 `end_time` 则**自动 disable** 该 schedule 并从 cron 引擎注销。|
|enabled|boolean|true|none|是否启用；**只读**输出字段，状态切换走 `POST .../enable` `POST .../disable`|
|strategies|[string]|true|none|发现策略集合，元素为 `insert` / `delete` / `update`；空数组表示全部。|
|last_run|integer(int64)|false|none|上次执行时间，毫秒时间戳；系统维护|
|next_run|integer(int64)|false|none|下次预计执行时间，毫秒时间戳；系统维护|
|creator|[AccountInfo](#schemaaccountinfo)|true|none|操作者信息|
|create_time|integer(int64)|true|none|创建时间，毫秒时间戳|
|updater|[AccountInfo](#schemaaccountinfo)|true|none|操作者信息|
|update_time|integer(int64)|true|none|更新时间，毫秒时间戳|

<h2 id="tocS_DiscoverScheduleRequest">DiscoverScheduleRequest</h2>
<!-- backwards compatibility -->
<a id="schemadiscoverschedulerequest"></a>
<a id="schema_DiscoverScheduleRequest"></a>
<a id="tocSdiscoverschedulerequest"></a>
<a id="tocsdiscoverschedulerequest"></a>

```json
{
  "id": "string",
  "name": "string",
  "catalog_id": "string",
  "cron_expr": "string",
  "start_time": 0,
  "end_time": 0,
  "enabled": true,
  "strategies": [
    "insert"
  ]
}

```

创建 / 更新请求体。

- 创建时 `name`、`catalog_id` 与 `cron_expr` 必填；`enabled` 默认 false（可同时传 true 一步启用）
- PUT 时 `catalog_id` 必填且必须与当前一致（空串 / 缺失视为不一致 → 409 CatalogMismatch）；
  `enabled` 必须与当前一致（否则 409 EnabledFieldNotAllowed）；`id` 可省略，携带时被忽略

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|false|none|创建时省略；PUT 时如携带必须与 path 一致|
|name|string|true|none|调度名称，必填，最大长度 255|
|catalog_id|string|true|none|关联 catalog；创建必填，PUT 时必须与当前一致|
|cron_expr|string|true|none|标准 5 字段 cron 表达式（必填）|
|start_time|integer(int64)|false|none|调度生效起始时间，毫秒时间戳；0 表示无起始限制。须 ≥ 0|
|end_time|integer(int64)|false|none|调度结束时间，毫秒时间戳；0 表示无结束时间。<br>须 ≥ 0，且 `end_time>0` 时要求 `start_time ≤ end_time`|
|enabled|boolean|false|none|创建时可选（默认 false）；PUT 时必须与当前一致，否则 409 EnabledFieldNotAllowed|
|strategies|[string]|false|none|发现策略，元素必须是 `insert` / `delete` / `update` 的子集；空数组表示全部|

<h2 id="tocS_ListDiscoverSchedules">ListDiscoverSchedules</h2>
<!-- backwards compatibility -->
<a id="schemalistdiscoverschedules"></a>
<a id="schema_ListDiscoverSchedules"></a>
<a id="tocSlistdiscoverschedules"></a>
<a id="tocslistdiscoverschedules"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "name": "string",
      "catalog_id": "string",
      "cron_expr": "string",
      "start_time": 0,
      "end_time": 0,
      "enabled": true,
      "strategies": [
        "insert"
      ],
      "last_run": 0,
      "next_run": 0,
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

调度列表响应

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[DiscoverSchedule](#schemadiscoverschedule)]|true|none|[发现调度实体]|
|total_count|integer(int64)|true|none|none|



<!-- Generator: Widdershins v4.0.1 -->

<h1 id="discovertask">DiscoverTask v0.1.0</h1>

> Scroll down for code samples, example requests and responses. Select a language for code samples from the tabs above or the mobile navigation menu.

Vega Backend 资源发现任务（DiscoverTask）相关 API。

DiscoverTask 是**执行审计记录**，由两条路径产生：

- 手动触发：`POST /catalogs/{id}/discover`（动作端点，本规范末尾覆盖）
- 定时触发：worker 内部由 `DiscoverSchedule`（见 [discover-schedule.yaml](discover-schedule.yaml)）按 cron 触发

状态机（`pending → running → completed/failed`）由 worker 单向推进；
用户不能 cancel / retry / restart。对外仅暴露 list / get / delete 三类只读 + 清理操作。

端点设计遵循 [vega-backend/CLAUDE.md] 的"端点设计规则"：批量删除走 path
（`DELETE /discover-tasks/{ids}`，逗号分隔），按父资源过滤一律走 query
（如 `GET /discover-tasks?catalog_id=` / `?schedule_id=`），不提供嵌套列表视图。

Base URLs:

* <a href="/api/vega-backend/v1">/api/vega-backend/v1</a>

<h1 id="discovertask-default">Default</h1>

## 获取发现任务列表

> Code samples

```shell
# You can also use wget
curl -X GET /api/vega-backend/v1/api/vega-backend/v1/discover-tasks \
  -H 'Accept: application/json'

```

```http
GET /api/vega-backend/v1/api/vega-backend/v1/discover-tasks HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/discover-tasks',
{
  method: 'GET',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.get '/api/vega-backend/v1/api/vega-backend/v1/discover-tasks',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.get('/api/vega-backend/v1/api/vega-backend/v1/discover-tasks', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('GET','/api/vega-backend/v1/api/vega-backend/v1/discover-tasks', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/discover-tasks");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("GET");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("GET", "/api/vega-backend/v1/api/vega-backend/v1/discover-tasks", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`GET /api/vega-backend/v1/discover-tasks`

分页获取发现任务；支持按 catalog_id / schedule_id / status / trigger_type 过滤。

<h3 id="获取发现任务列表-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|catalog_id|query|string|false|按归属 catalog 过滤|
|schedule_id|query|string|false|按归属 DiscoverSchedule 过滤；trigger_type=manual 的 task 该字段恒为空字符串。|
|status|query|string|false|按状态过滤|
|trigger_type|query|string|false|按触发方式过滤|
|offset|query|integer(int64)|false|分页偏移量，>=0，默认 0|
|limit|query|integer(int64)|false|每页数量，默认 20|
|sort|query|string|false|排序字段（当前数据访问层固定按 create_time 倒序，sort 参数预留）|
|direction|query|string|false|排序方向|

#### Detailed descriptions

**schedule_id**: 按归属 DiscoverSchedule 过滤；trigger_type=manual 的 task 该字段恒为空字符串。

#### Enumerated Values

|Parameter|Value|
|---|---|
|status|pending|
|status|running|
|status|completed|
|status|failed|
|trigger_type|manual|
|trigger_type|scheduled|
|direction|asc|
|direction|desc|

> Example responses

> 200 Response

```json
{
  "entries": [
    {
      "id": "string",
      "catalog_id": "string",
      "schedule_id": "string",
      "strategies": [
        "insert"
      ],
      "trigger_type": "manual",
      "status": "pending",
      "progress": 0,
      "message": "string",
      "start_time": 0,
      "finish_time": 0,
      "result": {
        "catalog_id": "string",
        "new_count": 0,
        "stale_count": 0,
        "unchanged_count": 0,
        "message": "string"
      },
      "creator": {
        "id": "string",
        "type": "string",
        "name": "string"
      },
      "create_time": 0
    }
  ],
  "total_count": 0
}
```

<h3 id="获取发现任务列表-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[ListDiscoverTasks](#schemalistdiscovertasks)|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数 / 请求体非法|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="获取发现任务列表-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 获取发现任务详情

> Code samples

```shell
# You can also use wget
curl -X GET /api/vega-backend/v1/api/vega-backend/v1/discover-tasks/{id} \
  -H 'Accept: application/json'

```

```http
GET /api/vega-backend/v1/api/vega-backend/v1/discover-tasks/{id} HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/discover-tasks/{id}',
{
  method: 'GET',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.get '/api/vega-backend/v1/api/vega-backend/v1/discover-tasks/{id}',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.get('/api/vega-backend/v1/api/vega-backend/v1/discover-tasks/{id}', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('GET','/api/vega-backend/v1/api/vega-backend/v1/discover-tasks/{id}', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/discover-tasks/{id}");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("GET");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("GET", "/api/vega-backend/v1/api/vega-backend/v1/discover-tasks/{id}", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`GET /api/vega-backend/v1/discover-tasks/{id}`

<h3 id="获取发现任务详情-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|id|path|string|true|DiscoverTask ID|

> Example responses

> 200 Response

```json
{
  "id": "string",
  "catalog_id": "string",
  "schedule_id": "string",
  "strategies": [
    "insert"
  ],
  "trigger_type": "manual",
  "status": "pending",
  "progress": 0,
  "message": "string",
  "start_time": 0,
  "finish_time": 0,
  "result": {
    "catalog_id": "string",
    "new_count": 0,
    "stale_count": 0,
    "unchanged_count": 0,
    "message": "string"
  },
  "creator": {
    "id": "string",
    "type": "string",
    "name": "string"
  },
  "create_time": 0
}
```

<h3 id="获取发现任务详情-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[DiscoverTask](#schemadiscovertask)|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|资源不存在|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="获取发现任务详情-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 删除发现任务（整体事务）

> Code samples

```shell
# You can also use wget
curl -X DELETE /api/vega-backend/v1/api/vega-backend/v1/discover-tasks/{ids} \
  -H 'Accept: application/json'

```

```http
DELETE /api/vega-backend/v1/api/vega-backend/v1/discover-tasks/{ids} HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/discover-tasks/{ids}',
{
  method: 'DELETE',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.delete '/api/vega-backend/v1/api/vega-backend/v1/discover-tasks/{ids}',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.delete('/api/vega-backend/v1/api/vega-backend/v1/discover-tasks/{ids}', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('DELETE','/api/vega-backend/v1/api/vega-backend/v1/discover-tasks/{ids}', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/discover-tasks/{ids}");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("DELETE");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("DELETE", "/api/vega-backend/v1/api/vega-backend/v1/discover-tasks/{ids}", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`DELETE /api/vega-backend/v1/discover-tasks/{ids}`

整体事务语义：所有 id 通过预校验后才进入删除阶段，任一预校验失败整批不删。

预校验顺序：

1. 任一 id 处于 `pending` / `running` → 409 `VegaBackend.DiscoverTask.HasRunningExecution`，
   `error_details` 携带 `{ running_ids: [...] }`。状态拦截**不可绕过**——避免删除
   掉 worker 正在写入的 task 留下孤儿数据。需等待任务进入终态（completed/failed）。
2. 任一 id 不存在（且未启用 `ignore_missing`）→ 404 `VegaBackend.DiscoverTask.NotFound`，
   `error_details` 携带 `{ missing_ids: [...] }`。
3. 全部通过 → 逐条删除，返回 204。

<h3 id="删除发现任务（整体事务）-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|ignore_missing|query|boolean|false|放宽不存在性检查：缺失 id 视为已删除（静默跳过），其它 id 正常删。|
|ids|path|string|true|DiscoverTask ID 列表，逗号分隔（单条即长度为 1 的退化情形）。|

#### Detailed descriptions

**ignore_missing**: 放宽不存在性检查：缺失 id 视为已删除（静默跳过），其它 id 正常删。
**不影响** pending/running 拦截。

**ids**: DiscoverTask ID 列表，逗号分隔（单条即长度为 1 的退化情形）。
重复 id 在 service 层会被去重。

> Example responses

<h3 id="删除发现任务（整体事务）-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|删除成功|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|资源不存在|None|
|409|[Conflict](https://tools.ietf.org/html/rfc7231#section-6.5.8)|状态冲突：task 处于 pending/running 无法删除|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="删除发现任务（整体事务）-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 手动触发 catalog 资源发现

> Code samples

```shell
# You can also use wget
curl -X POST /api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/discover \
  -H 'Accept: application/json'

```

```http
POST /api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/discover HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/discover',
{
  method: 'POST',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.post '/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/discover',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.post('/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/discover', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('POST','/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/discover', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/discover");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("POST");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("POST", "/api/vega-backend/v1/api/vega-backend/v1/catalogs/{id}/discover", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`POST /api/vega-backend/v1/catalogs/{id}/discover`

对指定 catalog 触发一次资源发现。**异步语义**：服务端创建一条 `trigger_type=manual`
的 DiscoverTask 并立即返回 task id；实际发现执行由 worker 异步推进。客户端可用
返回的 task id 通过 `GET /discover-tasks/{id}` 轮询执行进度。

无 request body。本端点保留 RPC 风格嵌套形态——是对 catalog 的动作而非创建顶层
task；详见 [discover_task_redesign.md] §1 非目标。

仅支持 `type=physical` 的 catalog；`type=logical` 会返回 400。

<h3 id="手动触发-catalog-资源发现-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|id|path|string|true|Catalog ID|

> Example responses

> 200 Response

```json
{
  "id": "string"
}
```

<h3 id="手动触发-catalog-资源发现-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|触发成功，DiscoverTask 已创建并入队|[DiscoverTriggerResponse](#schemadiscovertriggerresponse)|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数 / 请求体非法|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|Catalog 不存在|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="手动触发-catalog-资源发现-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
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

操作者信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|账户 ID|
|type|string|true|none|账户类型|
|name|string|false|none|显示名|

<h2 id="tocS_DiscoverResult">DiscoverResult</h2>
<!-- backwards compatibility -->
<a id="schemadiscoverresult"></a>
<a id="schema_DiscoverResult"></a>
<a id="tocSdiscoverresult"></a>
<a id="tocsdiscoverresult"></a>

```json
{
  "catalog_id": "string",
  "new_count": 0,
  "stale_count": 0,
  "unchanged_count": 0,
  "message": "string"
}

```

发现任务执行结果（仅在 status=completed 时有意义）

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|catalog_id|string|true|none|关联 catalog ID|
|new_count|integer|true|none|本次发现的新增资源数|
|stale_count|integer|true|none|本次发现已失效的资源数|
|unchanged_count|integer|true|none|本次发现未变化的资源数|
|message|string|true|none|执行说明|

<h2 id="tocS_DiscoverTask">DiscoverTask</h2>
<!-- backwards compatibility -->
<a id="schemadiscovertask"></a>
<a id="schema_DiscoverTask"></a>
<a id="tocSdiscovertask"></a>
<a id="tocsdiscovertask"></a>

```json
{
  "id": "string",
  "catalog_id": "string",
  "schedule_id": "string",
  "strategies": [
    "insert"
  ],
  "trigger_type": "manual",
  "status": "pending",
  "progress": 0,
  "message": "string",
  "start_time": 0,
  "finish_time": 0,
  "result": {
    "catalog_id": "string",
    "new_count": 0,
    "stale_count": 0,
    "unchanged_count": 0,
    "message": "string"
  },
  "creator": {
    "id": "string",
    "type": "string",
    "name": "string"
  },
  "create_time": 0
}

```

发现任务实体

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|全局唯一 ID|
|catalog_id|string|true|none|关联 catalog ID|
|schedule_id|string|true|none|关联的 DiscoverSchedule ID。trigger_type=manual 时为空字符串。|
|strategies|[string]|true|none|发现策略，可为以下零或多个：`insert` / `delete` / `update`。<br>空数组表示对所有策略执行。|
|trigger_type|string|true|none|触发方式|
|status|string|true|none|状态|
|progress|integer|true|none|进度，0-100|
|message|string|true|none|状态说明 / 错误信息|
|start_time|integer(int64)|false|none|开始执行时间，毫秒级时间戳；未开始时为 0|
|finish_time|integer(int64)|false|none|完成时间，毫秒级时间戳；未完成时为 0|
|result|[DiscoverResult](#schemadiscoverresult)|false|none|执行结果（仅 completed 状态下非空）|
|creator|[AccountInfo](#schemaaccountinfo)|true|none|操作者信息|
|create_time|integer(int64)|true|none|创建时间，毫秒级时间戳|

#### Enumerated Values

|Property|Value|
|---|---|
|trigger_type|manual|
|trigger_type|scheduled|
|status|pending|
|status|running|
|status|completed|
|status|failed|

<h2 id="tocS_DiscoverTriggerResponse">DiscoverTriggerResponse</h2>
<!-- backwards compatibility -->
<a id="schemadiscovertriggerresponse"></a>
<a id="schema_DiscoverTriggerResponse"></a>
<a id="tocSdiscovertriggerresponse"></a>
<a id="tocsdiscovertriggerresponse"></a>

```json
{
  "id": "string"
}

```

手动触发响应（异步——返回新建的 DiscoverTask ID）

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|新建 DiscoverTask 的 ID；可用 `GET /discover-tasks/{id}` 轮询进度|

<h2 id="tocS_ListDiscoverTasks">ListDiscoverTasks</h2>
<!-- backwards compatibility -->
<a id="schemalistdiscovertasks"></a>
<a id="schema_ListDiscoverTasks"></a>
<a id="tocSlistdiscovertasks"></a>
<a id="tocslistdiscovertasks"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "catalog_id": "string",
      "schedule_id": "string",
      "strategies": [
        "insert"
      ],
      "trigger_type": "manual",
      "status": "pending",
      "progress": 0,
      "message": "string",
      "start_time": 0,
      "finish_time": 0,
      "result": {
        "catalog_id": "string",
        "new_count": 0,
        "stale_count": 0,
        "unchanged_count": 0,
        "message": "string"
      },
      "creator": {
        "id": "string",
        "type": "string",
        "name": "string"
      },
      "create_time": 0
    }
  ],
  "total_count": 0
}

```

发现任务列表

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[DiscoverTask](#schemadiscovertask)]|true|none|条目列表|
|total_count|integer(int64)|true|none|总条数|



<!-- Generator: Widdershins v4.0.1 -->

<h1 id="raw-query">Raw Query v0.1.0</h1>

> Scroll down for code samples, example requests and responses. Select a language for code samples from the tabs above or the mobile navigation menu.

Vega Backend 原生查询端点（`POST /resources/query`）相关 API。

本端点把 query 透传给底层连接器执行，**不做结构化 filter 解析**：

- SQL 引擎（mysql / postgresql / clickhouse / 达梦 / oracle）→ `query` 字段为 SQL 字符串
- 索引引擎（opensearch / elasticsearch）→ `query` 字段为 DSL JSON 对象

与 `POST /resources/{id}/data`（[resource-data.yaml](resource-data.yaml)）的分工：

| 维度 | `POST /resources/query` | `POST /resources/{id}/data` (Override:GET) |
|---|---|---|
| 主键 | 无（不绑定 resource） | 绑定 `:id` |
| 输入 | 原生 SQL / DSL + `resource_type` | 结构化 `filter_condition` + 分页 |
| 输出 | 列元数据 + raw entries + 流式 cursor | 文档对象 + 可选 total_count |
| 流式 | ✅ `query_type=stream` + `query_id` 游标 | ❌ |
| 用途 | 跨 resource 联表 / 复杂 SQL / 流式大数据 | 应用层结构化访问 |

**安全说明**：本端点把原生 SQL/DSL 直传给底层数据源，是高权限能力。调用方应当
受 RBAC 约束；外部 OAuth 调用方建议在网关层做白名单 / 限流。

Base URLs:

* <a href="/api/vega-backend/v1">/api/vega-backend/v1</a>

<h1 id="raw-query-default">Default</h1>

## 执行原生查询（SQL 或 DSL）

> Code samples

```shell
# You can also use wget
curl -X POST /api/vega-backend/v1/api/vega-backend/v1/resources/query \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json'

```

```http
POST /api/vega-backend/v1/api/vega-backend/v1/resources/query HTTP/1.1

Content-Type: application/json
Accept: application/json

```

```javascript
const inputBody = '{
  "query": "string",
  "query_type": "standard",
  "query_id": "string",
  "resource_type": "mysql",
  "stream_size": 10000,
  "query_timeout": 60
}';
const headers = {
  'Content-Type':'application/json',
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/resources/query',
{
  method: 'POST',
  body: inputBody,
  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Content-Type' => 'application/json',
  'Accept' => 'application/json'
}

result = RestClient.post '/api/vega-backend/v1/api/vega-backend/v1/resources/query',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Content-Type': 'application/json',
  'Accept': 'application/json'
}

r = requests.post('/api/vega-backend/v1/api/vega-backend/v1/resources/query', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Content-Type' => 'application/json',
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('POST','/api/vega-backend/v1/api/vega-backend/v1/resources/query', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/resources/query");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("POST");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Content-Type": []string{"application/json"},
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("POST", "/api/vega-backend/v1/api/vega-backend/v1/resources/query", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`POST /api/vega-backend/v1/resources/query`

**请求体的 `query` 字段类型依赖 `resource_type`**：

- `mysql` / `postgresql` / `clickhouse` / `dameng` / `oracle` → SQL 字符串
  示例：`{"query": "SELECT * FROM t WHERE id = 1", "resource_type": "mysql"}`
- `opensearch` / `elasticsearch` → DSL JSON 对象
  示例：`{"query": {"query": {"match_all": {}}}, "resource_type": "opensearch"}`

**流式查询**：`query_type=stream` 启用游标模式。首次请求不带 `query_id`，
服务端在响应 `stats.query_id` 中返回；后续请求携带该 `query_id` 继续游标，
直到 `stats.has_more=false`。流式仅 OpenSearch 提供 search_after 语义。

**支持的 `resource_type`** 由 `interfaces.GetSupportedConnectorTypesForQuery()`
定义；不在白名单内返回 400 `VegaBackend.InvalidParameter.ResourceType`。

> Body parameter

```json
{
  "query": "string",
  "query_type": "standard",
  "query_id": "string",
  "resource_type": "mysql",
  "stream_size": 10000,
  "query_timeout": 60
}
```

<h3 id="执行原生查询（sql-或-dsl）-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|body|body|[RawQueryRequest](#schemarawqueryrequest)|true|none|

> Example responses

> 200 Response

```json
{
  "columns": [
    {
      "name": "string",
      "type": "string"
    }
  ],
  "entries": [
    {}
  ],
  "stats": {
    "is_timeout": true,
    "query_id": "string",
    "has_more": true,
    "search_after": [
      null
    ],
    "offset": 0
  },
  "total_count": 0
}
```

<h3 id="执行原生查询（sql-或-dsl）-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|查询成功|[RawQueryResponse](#schemarawqueryresponse)|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数 / 请求体非法。常见 errcode：
- `VegaBackend.InvalidParameter.RequestBody`：body schema 不合法
- `VegaBackend.InvalidParameter.ResourceType`：`resource_type` 缺失或不在白名单
- `VegaBackend.InvalidParameter.StreamSize`：`stream_size` 超出 [100, 10000]
- `VegaBackend.Query.InvalidParameter.QueryTimeout`：`query_timeout` 超出 [1, 3600]|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|406|[Not Acceptable](https://tools.ietf.org/html/rfc7231#section-6.5.6)|Content-Type 不是 application/json|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误。常见 errcode：
- `VegaBackend.Query.ExecuteFailed`：底层连接器执行查询失败（SQL 语法、权限、超时等）|None|

<h3 id="执行原生查询（sql-或-dsl）-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

# Schemas

<h2 id="tocS_RawQueryRequest">RawQueryRequest</h2>
<!-- backwards compatibility -->
<a id="schemarawqueryrequest"></a>
<a id="schema_RawQueryRequest"></a>
<a id="tocSrawqueryrequest"></a>
<a id="tocsrawqueryrequest"></a>

```json
{
  "query": "string",
  "query_type": "standard",
  "query_id": "string",
  "resource_type": "mysql",
  "stream_size": 10000,
  "query_timeout": 60
}

```

原生查询请求

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|query|any|true|none|查询语句。类型依赖 `resource_type`：<br>- SQL 引擎用字符串<br>- 索引引擎（OpenSearch/Elasticsearch）用 JSON 对象|

oneOf

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|string|false|none|SQL 字符串（mysql / postgresql 等）|

xor

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|» *anonymous*|object|false|none|DSL JSON 对象（OpenSearch / Elasticsearch）|

continued

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|query_type|string|false|none|查询类型；`stream` 启用游标，需配合 `query_id` / `stats.has_more`|
|query_id|string|false|none|流式查询游标 ID。首次请求留空，服务端在响应 `stats.query_id` 中返回；<br>后续请求带上该 ID 继续游标。|
|resource_type|string|true|none|底层连接器类型，决定 query 字段如何解释。<br>支持值由后端 `GetSupportedConnectorTypesForQuery()` 配置。|
|stream_size|integer(int64)|false|none|流式查询每批数据量，[100, 10000]，默认 10000|
|query_timeout|integer(int64)|false|none|查询超时（秒），[1, 3600]，默认 60|

#### Enumerated Values

|Property|Value|
|---|---|
|query_type|standard|
|query_type|stream|

<h2 id="tocS_RawQueryResponse">RawQueryResponse</h2>
<!-- backwards compatibility -->
<a id="schemarawqueryresponse"></a>
<a id="schema_RawQueryResponse"></a>
<a id="tocSrawqueryresponse"></a>
<a id="tocsrawqueryresponse"></a>

```json
{
  "columns": [
    {
      "name": "string",
      "type": "string"
    }
  ],
  "entries": [
    {}
  ],
  "stats": {
    "is_timeout": true,
    "query_id": "string",
    "has_more": true,
    "search_after": [
      null
    ],
    "offset": 0
  },
  "total_count": 0
}

```

原生查询响应

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|columns|[[ColumnInfo](#schemacolumninfo)]|true|none|列元数据|
|entries|[object]|true|none|查询结果（每条为一行/一个文档，结构开放）|
|stats|[QueryStats](#schemaquerystats)|true|none|查询统计 / 流式 cursor 状态|
|total_count|integer(int64)|true|none|总条数（标准查询返回完整命中数；流式查询为已返回累计数）|

<h2 id="tocS_ColumnInfo">ColumnInfo</h2>
<!-- backwards compatibility -->
<a id="schemacolumninfo"></a>
<a id="schema_ColumnInfo"></a>
<a id="tocScolumninfo"></a>
<a id="tocscolumninfo"></a>

```json
{
  "name": "string",
  "type": "string"
}

```

列元数据

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|列名|
|type|string|true|none|列类型（取决于底层连接器）|

<h2 id="tocS_QueryStats">QueryStats</h2>
<!-- backwards compatibility -->
<a id="schemaquerystats"></a>
<a id="schema_QueryStats"></a>
<a id="tocSquerystats"></a>
<a id="tocsquerystats"></a>

```json
{
  "is_timeout": true,
  "query_id": "string",
  "has_more": true,
  "search_after": [
    null
  ],
  "offset": 0
}

```

查询统计 / 流式 cursor 状态

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|is_timeout|boolean|true|none|是否因 query_timeout 触发超时|
|query_id|string|false|none|流式查询 ID。流式首次响应时由服务端生成；客户端在后续请求里回传<br>以继续游标。标准查询时为空。|
|has_more|boolean|true|none|是否还有更多数据（流式查询用；标准查询恒为 false）|
|search_after|[any]|false|none|OpenSearch 流式查询的 search_after 值；服务端内部使用，客户端透传即可。|
|offset|integer|true|none|已获取到的累计数据条数（流式查询模式）|



<!-- Generator: Widdershins v4.0.1 -->

<h1 id="resource-data">Resource Data v0.1.0</h1>

> Scroll down for code samples, example requests and responses. Select a language for code samples from the tabs above or the mobile navigation menu.

Vega Backend Resource 数据子资源（`/resources/{id}/data`）相关 API。

本规范覆盖两类语义：

1. **通用资源数据查询**：`POST /resources/{id}/data` + `X-HTTP-Method-Override: GET`，
   对任意 category 的 Resource 执行数据查询（filter + 分页）。
2. **dataset documents 的写/删/单条访问**：`PUT` / `GET single` / `DELETE` 与
   `POST` 的 `Override: POST` / `Override: DELETE` 分支，仅对 `Resource.category=dataset` 生效。

**dataset 不顶层化**：dataset 在系统中只是 `Resource.category=dataset` 的特化形态，
无独立 ID 体系。CRUD 走 `/resources/{id}`，仅 documents 子资源（即本规范）独立 API surface。

端点设计遵循 [vega-backend/CLAUDE.md] 的"端点设计规则"。`POST /data` 用
`X-HTTP-Method-Override` 头多分发，是为了兼容现有 query 语义并在同一条路径上
暴露 create / delete-by-filter，避免在仓库内引入新的"动作端点"形态。

**错误码复用 Resource 系列**：本规范不引入 `Dataset.*` 专属错误码；单条文档不存在
与 resource 不存在共用 `VegaBackend.Resource.NotFound`，靠 `error_details` 区分。

Base URLs:

* <a href="/api/vega-backend/v1">/api/vega-backend/v1</a>

<h1 id="resource-data-default">Default</h1>

## Resource 数据多分发端点

> Code samples

```shell
# You can also use wget
curl -X POST /api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json' \
  -H 'X-HTTP-Method-Override: GET'

```

```http
POST /api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data HTTP/1.1

Content-Type: application/json
Accept: application/json
X-HTTP-Method-Override: GET

```

```javascript
const inputBody = '{
  "filter_condition": {},
  "offset": 0,
  "limit": 20,
  "sort": "string",
  "direction": "asc",
  "need_total": false
}';
const headers = {
  'Content-Type':'application/json',
  'Accept':'application/json',
  'X-HTTP-Method-Override':'GET'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data',
{
  method: 'POST',
  body: inputBody,
  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Content-Type' => 'application/json',
  'Accept' => 'application/json',
  'X-HTTP-Method-Override' => 'GET'
}

result = RestClient.post '/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Content-Type': 'application/json',
  'Accept': 'application/json',
  'X-HTTP-Method-Override': 'GET'
}

r = requests.post('/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Content-Type' => 'application/json',
    'Accept' => 'application/json',
    'X-HTTP-Method-Override' => 'GET',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('POST','/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("POST");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Content-Type": []string{"application/json"},
        "Accept": []string{"application/json"},
        "X-HTTP-Method-Override": []string{"GET"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("POST", "/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`POST /api/vega-backend/v1/resources/{id}/data`

**必须**带 `X-HTTP-Method-Override` 头，值是 `GET` / `POST` / `DELETE` 之一
（大小写不敏感）；缺失或值非法返回 400 `VegaBackend.InvalidParameter.OverrideMethod`。

三种分支：

| Override | 行为 | category 限制 | body schema |
|---|---|---|---|
| `GET` | 列表 / 查询文档 | 任意（兼容现有通用查询） | `ResourceDataQueryParams` |
| `POST` | 批量创建文档 | **dataset 强制** | `DocumentArray`（每条 id 可选，无则后端生成） |
| `DELETE` | 按 filter 删除文档 | **dataset 强制** | `ResourceDataQueryParams`（`filter_condition` 必填非空） |

**`Override: GET` 响应**：200 + `{ entries, total_count? }`。`total_count` 仅在
`ResourceDataQueryParams.need_total=true` 时返回（保留现有行为）。

**`Override: POST` 响应**：201 + `{ ids: [...] }`。后端为无 id 的文档生成 id。

**`Override: DELETE` 响应**：204。空 `filter_condition` 拒绝 400 避免误删全表。

非 dataset category 的 resource 调 Override=POST/DELETE → 400
`VegaBackend.Resource.InternalError.InvalidCategory`。

> Body parameter

```json
{
  "filter_condition": {},
  "offset": 0,
  "limit": 20,
  "sort": "string",
  "direction": "asc",
  "need_total": false
}
```

<h3 id="resource-数据多分发端点-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|X-HTTP-Method-Override|header|string|true|必填；值为 GET / POST / DELETE 之一|
|body|body|any|true|none|
|id|path|string|true|Resource ID|

#### Enumerated Values

|Parameter|Value|
|---|---|
|X-HTTP-Method-Override|GET|
|X-HTTP-Method-Override|POST|
|X-HTTP-Method-Override|DELETE|

> Example responses

> 200 Response

```json
{
  "entries": [
    {
      "id": "string"
    }
  ],
  "total_count": 0
}
```

<h3 id="resource-数据多分发端点-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|Override=GET 查询成功|[ListResponse](#schemalistresponse)|
|201|[Created](https://tools.ietf.org/html/rfc7231#section-6.3.2)|Override=POST 创建成功|[IdsResponse](#schemaidsresponse)|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|Override=DELETE 删除成功|None|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数 / 请求体非法。常见 errcode：
- `VegaBackend.InvalidParameter.OverrideMethod`：POST 缺/错 Override 头
- `VegaBackend.InvalidParameter.RequestBody`：body schema / 必填字段不合法
- `VegaBackend.InvalidParameter.FilterCondition`：filter_condition 解析失败
- `VegaBackend.Resource.InternalError.InvalidCategory`：写/删/单条 GET 操作针对非 dataset resource|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|Resource 或文档不存在。errcode `VegaBackend.Resource.NotFound`；
通过 `error_details` 字符串区分是 resource 还是 document 找不到。|None|
|406|[Not Acceptable](https://tools.ietf.org/html/rfc7231#section-6.5.6)|Content-Type 不是 application/json|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误（如底层 OpenSearch / 索引故障）|None|

<h3 id="resource-数据多分发端点-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 批量 update（upsert）文档

> Code samples

```shell
# You can also use wget
curl -X PUT /api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json'

```

```http
PUT /api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data HTTP/1.1

Content-Type: application/json
Accept: application/json

```

```javascript
const inputBody = '[
  {
    "id": "string"
  }
]';
const headers = {
  'Content-Type':'application/json',
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data',
{
  method: 'PUT',
  body: inputBody,
  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Content-Type' => 'application/json',
  'Accept' => 'application/json'
}

result = RestClient.put '/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Content-Type': 'application/json',
  'Accept': 'application/json'
}

r = requests.put('/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Content-Type' => 'application/json',
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('PUT','/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("PUT");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Content-Type": []string{"application/json"},
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("PUT", "/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`PUT /api/vega-backend/v1/resources/{id}/data`

批量更新 dataset 文档，**id 强制**：body 数组中每条文档必须带 `id` 字段，缺失整批拒绝。

语义：upsert——id 存在则替换，不存在则按该 id 创建（service 层调 `UpsertDocuments`）。

category 限制：仅 `dataset`，否则 400 `VegaBackend.Resource.InternalError.InvalidCategory`。
空数组 body 拒绝 400 `VegaBackend.InvalidParameter.RequestBody`。

> Body parameter

```json
[
  {
    "id": "string"
  }
]
```

<h3 id="批量-update（upsert）文档-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|body|body|[DocumentArray](#schemadocumentarray)|true|none|
|id|path|string|true|Resource ID|

> Example responses

> 200 Response

```json
{
  "ids": [
    "string"
  ]
}
```

<h3 id="批量-update（upsert）文档-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|更新成功|[IdsResponse](#schemaidsresponse)|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数 / 请求体非法。常见 errcode：
- `VegaBackend.InvalidParameter.OverrideMethod`：POST 缺/错 Override 头
- `VegaBackend.InvalidParameter.RequestBody`：body schema / 必填字段不合法
- `VegaBackend.InvalidParameter.FilterCondition`：filter_condition 解析失败
- `VegaBackend.Resource.InternalError.InvalidCategory`：写/删/单条 GET 操作针对非 dataset resource|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|Resource 或文档不存在。errcode `VegaBackend.Resource.NotFound`；
通过 `error_details` 字符串区分是 resource 还是 document 找不到。|None|
|406|[Not Acceptable](https://tools.ietf.org/html/rfc7231#section-6.5.6)|Content-Type 不是 application/json|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误（如底层 OpenSearch / 索引故障）|None|

<h3 id="批量-update（upsert）文档-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 按 ids 批量删除 dataset 文档（best-effort）

> Code samples

```shell
# You can also use wget
curl -X DELETE /api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data/{doc_ids} \
  -H 'Accept: application/json'

```

```http
DELETE /api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data/{doc_ids} HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data/{doc_ids}',
{
  method: 'DELETE',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.delete '/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data/{doc_ids}',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.delete('/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data/{doc_ids}', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('DELETE','/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data/{doc_ids}', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data/{doc_ids}");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("DELETE");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("DELETE", "/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data/{doc_ids}", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`DELETE /api/vega-backend/v1/resources/{id}/data/{doc_ids}`

批量删除 dataset 文档。**best-effort 语义**：
- 缺失 id 静默跳过，不返回 404 / 4xx
- 不接受 `ignore_missing` 选项

与 `/build-tasks/{ids}` / `/discover-tasks/{ids}` 的"整体事务 + ignore_missing"
不同；documents 场景的 missing 容忍度高，统一行为更简洁。

category 限制：仅 `dataset`。

<h3 id="按-ids-批量删除-dataset-文档（best-effort）-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|id|path|string|true|Resource ID|
|doc_ids|path|string|true|文档 ID 列表，逗号分隔（单条即长度 1 的退化情形）。|

#### Detailed descriptions

**doc_ids**: 文档 ID 列表，逗号分隔（单条即长度 1 的退化情形）。
缺失 id 静默跳过；本端点不接受 `?ignore_missing` 参数。

> Example responses

<h3 id="按-ids-批量删除-dataset-文档（best-effort）-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|删除成功|None|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数 / 请求体非法。常见 errcode：
- `VegaBackend.InvalidParameter.OverrideMethod`：POST 缺/错 Override 头
- `VegaBackend.InvalidParameter.RequestBody`：body schema / 必填字段不合法
- `VegaBackend.InvalidParameter.FilterCondition`：filter_condition 解析失败
- `VegaBackend.Resource.InternalError.InvalidCategory`：写/删/单条 GET 操作针对非 dataset resource|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|Resource 或文档不存在。errcode `VegaBackend.Resource.NotFound`；
通过 `error_details` 字符串区分是 resource 还是 document 找不到。|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误（如底层 OpenSearch / 索引故障）|None|

<h3 id="按-ids-批量删除-dataset-文档（best-effort）-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 获取单条文档

> Code samples

```shell
# You can also use wget
curl -X GET /api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data/{doc_id} \
  -H 'Accept: application/json'

```

```http
GET /api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data/{doc_id} HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data/{doc_id}',
{
  method: 'GET',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.get '/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data/{doc_id}',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.get('/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data/{doc_id}', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('GET','/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data/{doc_id}', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data/{doc_id}");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("GET");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("GET", "/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data/{doc_id}", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`GET /api/vega-backend/v1/resources/{id}/data/{doc_id}`

category 限制：仅 `dataset`。

文档不存在返回 404 `VegaBackend.Resource.NotFound`（与 resource 不存在共用同一
errcode；靠 `error_details: "document {doc_id} not found"` 区分）。

<h3 id="获取单条文档-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|id|path|string|true|Resource ID|
|doc_id|path|string|true|文档 ID|

> Example responses

> 200 Response

```json
{
  "id": "string"
}
```

<h3 id="获取单条文档-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[Document](#schemadocument)|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数 / 请求体非法。常见 errcode：
- `VegaBackend.InvalidParameter.OverrideMethod`：POST 缺/错 Override 头
- `VegaBackend.InvalidParameter.RequestBody`：body schema / 必填字段不合法
- `VegaBackend.InvalidParameter.FilterCondition`：filter_condition 解析失败
- `VegaBackend.Resource.InternalError.InvalidCategory`：写/删/单条 GET 操作针对非 dataset resource|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|Resource 或文档不存在。errcode `VegaBackend.Resource.NotFound`；
通过 `error_details` 字符串区分是 resource 还是 document 找不到。|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误（如底层 OpenSearch / 索引故障）|None|

<h3 id="获取单条文档-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 单条 update（upsert）文档

> Code samples

```shell
# You can also use wget
curl -X PUT /api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data/{doc_id} \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json'

```

```http
PUT /api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data/{doc_id} HTTP/1.1

Content-Type: application/json
Accept: application/json

```

```javascript
const inputBody = '{
  "id": "string"
}';
const headers = {
  'Content-Type':'application/json',
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data/{doc_id}',
{
  method: 'PUT',
  body: inputBody,
  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Content-Type' => 'application/json',
  'Accept' => 'application/json'
}

result = RestClient.put '/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data/{doc_id}',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Content-Type': 'application/json',
  'Accept': 'application/json'
}

r = requests.put('/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data/{doc_id}', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Content-Type' => 'application/json',
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('PUT','/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data/{doc_id}', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data/{doc_id}");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("PUT");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Content-Type": []string{"application/json"},
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("PUT", "/api/vega-backend/v1/api/vega-backend/v1/resources/{id}/data/{doc_id}", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`PUT /api/vega-backend/v1/resources/{id}/data/{doc_id}`

单条文档更新。**doc_id 以 path 为准**——body 不需要 `id` 字段，即使携带也以 path
覆盖（path 与 body id 不一致时仅记 warn log，不报错）。

语义同批量 PUT：upsert（id 存在则替换，不存在则按该 id 创建）。

category 限制：仅 `dataset`。

> Body parameter

```json
{
  "id": "string"
}
```

<h3 id="单条-update（upsert）文档-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|body|body|[Document](#schemadocument)|true|none|
|id|path|string|true|Resource ID|
|doc_id|path|string|true|文档 ID|

> Example responses

> 200 Response

```json
{
  "ids": [
    "string"
  ]
}
```

<h3 id="单条-update（upsert）文档-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|更新成功|[IdsResponse](#schemaidsresponse)|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数 / 请求体非法。常见 errcode：
- `VegaBackend.InvalidParameter.OverrideMethod`：POST 缺/错 Override 头
- `VegaBackend.InvalidParameter.RequestBody`：body schema / 必填字段不合法
- `VegaBackend.InvalidParameter.FilterCondition`：filter_condition 解析失败
- `VegaBackend.Resource.InternalError.InvalidCategory`：写/删/单条 GET 操作针对非 dataset resource|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|Resource 或文档不存在。errcode `VegaBackend.Resource.NotFound`；
通过 `error_details` 字符串区分是 resource 还是 document 找不到。|None|
|406|[Not Acceptable](https://tools.ietf.org/html/rfc7231#section-6.5.6)|Content-Type 不是 application/json|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误（如底层 OpenSearch / 索引故障）|None|

<h3 id="单条-update（upsert）文档-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

# Schemas

<h2 id="tocS_Document">Document</h2>
<!-- backwards compatibility -->
<a id="schemadocument"></a>
<a id="schema_Document"></a>
<a id="tocSdocument"></a>
<a id="tocsdocument"></a>

```json
{
  "id": "string"
}

```

文档对象。content 字段集开放，由具体 dataset schema 决定。
- 单条 PUT 时不应携带 `id`；如携带，以 path 为准（仅记 warn log）。
- 批量 PUT 时**必须**携带 `id`。
- 批量 POST（创建）时 `id` 可选，无则后端生成。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|false|none|文档 ID|

<h2 id="tocS_DocumentArray">DocumentArray</h2>
<!-- backwards compatibility -->
<a id="schemadocumentarray"></a>
<a id="schema_DocumentArray"></a>
<a id="tocSdocumentarray"></a>
<a id="tocsdocumentarray"></a>

```json
[
  {
    "id": "string"
  }
]

```

文档数组

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|*anonymous*|[[Document](#schemadocument)]|false|none|文档数组|

<h2 id="tocS_ResourceDataQueryParams">ResourceDataQueryParams</h2>
<!-- backwards compatibility -->
<a id="schemaresourcedataqueryparams"></a>
<a id="schema_ResourceDataQueryParams"></a>
<a id="tocSresourcedataqueryparams"></a>
<a id="tocsresourcedataqueryparams"></a>

```json
{
  "filter_condition": {},
  "offset": 0,
  "limit": 20,
  "sort": "string",
  "direction": "asc",
  "need_total": false
}

```

查询 / 删除参数。`Override: GET` 用 filter+分页查询；`Override: DELETE` 用
`filter_condition` 删除（filter 必须非空，否则 400）。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|filter_condition|object|false|none|过滤条件，结构由 `FilterCondCfg` 决定。Override=DELETE 时必填且非空。|
|offset|integer(int64)|false|none|分页偏移量，>=0|
|limit|integer(int64)|false|none|每页数量|
|sort|string|false|none|排序字段|
|direction|string|false|none|排序方向|
|need_total|boolean|false|none|是否在响应里携带 total_count|

#### Enumerated Values

|Property|Value|
|---|---|
|direction|asc|
|direction|desc|

<h2 id="tocS_ListResponse">ListResponse</h2>
<!-- backwards compatibility -->
<a id="schemalistresponse"></a>
<a id="schema_ListResponse"></a>
<a id="tocSlistresponse"></a>
<a id="tocslistresponse"></a>

```json
{
  "entries": [
    {
      "id": "string"
    }
  ],
  "total_count": 0
}

```

查询响应。`total_count` 仅在请求 `need_total=true` 时返回。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[Document](#schemadocument)]|true|none|文档条目|
|total_count|integer(int64)|false|none|总条数（仅 need_total=true 时返回）|

<h2 id="tocS_IdsResponse">IdsResponse</h2>
<!-- backwards compatibility -->
<a id="schemaidsresponse"></a>
<a id="schema_IdsResponse"></a>
<a id="tocSidsresponse"></a>
<a id="tocsidsresponse"></a>

```json
{
  "ids": [
    "string"
  ]
}

```

写入操作的响应，含成功的文档 ID 数组

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|ids|[string]|true|none|文档 ID 列表|



<!-- Generator: Widdershins v4.0.1 -->

<h1 id="resource">Resource v0.2.1</h1>

> Scroll down for code samples, example requests and responses. Select a language for code samples from the tabs above or the mobile navigation menu.

Vega Backend Resource（数据资源）相关 API。

Resource 是数据资源主实体，归属于某个 Catalog。每个 Resource 有一种 `category`：

| category | 类型 | 说明 |
|---|---|---|
| `table` | 物理 | 关系表（mysql / postgresql / clickhouse / 达梦 / oracle） |
| `index` | 物理 | OpenSearch / Elasticsearch 索引 |
| `topic` | 物理 | 消息队列 topic（计划中） |
| `file` | 物理 | 单文件 |
| `fileset` | 物理 | 文件集（计划中） |
| `metric` | 物理 | 指标（计划中） |
| `api` | 物理 | API 数据源（计划中） |
| `logicview` | 逻辑 | 衍生 / 复合视图，由 `logic_definition` 定义 |
| `dataset` | 逻辑 | 文档集合（RAG 索引），documents 子资源走 [resource-data.yaml] |

`status` 取值：`active` / `disabled` / `deprecated` / `stale`。

**可检索业务 KV（`extensions`，Issue #382，方案 B）**

- 与 `tags` 不同，`extensions` 为 **扁平 string→string**，存于 **`t_entity_extension`**；
  `f_entity_id` 与 `t_resource.f_id` **同一全局唯一 ID 空间**（见设计文档不变量）。
- **创建 / 更新**：请求体可选 **`extensions`**；**键出现**（含 `{}`）即整包替换副表行；
  **键未出现**则不修改。
- **读取**：详情与列表（`include_extensions` 为 true 时）返回 **`extensions`**。
- **列表**：`include_extensions`、`include_extension_keys`；筛选 **`extension_key` / `extension_value`**
  数组 query（与 `catalog.yaml` 一致）。
- 设计依据：
  [catalog-resource-labels-scheme-b-design.md](../../../design/vega/features/vega-backend/dip-for-extension/catalog-resource-labels-scheme-b-design.md)

**持久化（与 `migrations/mariadb`、`migrations/dm8` 惯例对齐）**

- 与 Catalog 共用 **`t_entity_extension`** 表；**无** `f_scope` 列；主键 `(f_entity_id, f_key)`。
- 列名、索引、时间字段风格与 `catalog.yaml` / 设计文档 **§3** 一致；删除 resource 时同事务删除
  `f_entity_id` 等于该 resource `f_id` 的全部扩展行。

**子资源端点指引**：

- 数据访问（dataset 文档 / 通用查询）→ [resource-data.yaml](resource-data.yaml)
- 原生 SQL/DSL 查询 → [raw-query.yaml](raw-query.yaml)
- 构建任务（streaming / batch / embedding）→ [build-task.yaml](build-task.yaml)
- 资源发现 → [discover-task.yaml](discover-task.yaml)

端点设计遵循 [vega-backend/CLAUDE.md] 的"端点设计规则"：批量 GET / DELETE 走 path
（`/resources/{ids}` 逗号分隔，单条退化），列表过滤走 query。

Base URLs:

* <a href="/api/vega-backend/v1">/api/vega-backend/v1</a>

<h1 id="resource-default">Default</h1>

## 获取资源列表

> Code samples

```shell
# You can also use wget
curl -X GET /api/vega-backend/v1/api/vega-backend/v1/resources \
  -H 'Accept: application/json'

```

```http
GET /api/vega-backend/v1/api/vega-backend/v1/resources HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/resources',
{
  method: 'GET',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.get '/api/vega-backend/v1/api/vega-backend/v1/resources',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.get('/api/vega-backend/v1/api/vega-backend/v1/resources', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('GET','/api/vega-backend/v1/api/vega-backend/v1/resources', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/resources");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("GET");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("GET", "/api/vega-backend/v1/api/vega-backend/v1/resources", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`GET /api/vega-backend/v1/resources`

分页 + 多维过滤获取 Resource 列表。

**KV 筛选**：`extension_key` / `extension_value`（数组 query）；
语义同 [catalog.yaml](catalog.yaml) 列表；与 `catalog_id`、`category` 等过滤 **AND** 组合。

<h3 id="获取资源列表-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|name|query|string|false|按名称模糊过滤，匹配名称中包含该值的资源|
|catalog_id|query|string|false|按归属 catalog 过滤|
|category|query|string|false|按类别过滤|
|status|query|string|false|按状态过滤|
|database|query|string|false|按所属 database 过滤（实例级 catalog 时有意义）|
|offset|query|integer(int64)|false|分页偏移量，>=0，默认 0|
|limit|query|integer(int64)|false|每页数量，默认 20|
|sort|query|string|false|排序字段|
|direction|query|string|false|排序方向|
|extension_key|query|array[string]|false|与 `extension_value` 成对；多条件 AND。等长数组；等值匹配 `t_entity_extension.f_key`。|
|extension_value|query|array[string]|false|与 `extension_key` 成对；语义见 `extension_key`。|
|include_extensions|query|boolean|false|为 true 时列表条目带 `extensions`；默认 false。|
|include_extension_keys|query|string|false|逗号分隔 key；在 `include_extensions` 为 true 时仅返回列出的 key。|

#### Detailed descriptions

**include_extensions**: 为 true 时列表条目带 `extensions`；默认 false。

#### Enumerated Values

|Parameter|Value|
|---|---|
|category|table|
|category|file|
|category|fileset|
|category|api|
|category|metric|
|category|topic|
|category|index|
|category|logicview|
|category|dataset|
|status|active|
|status|disabled|
|status|deprecated|
|status|stale|
|sort|name|
|sort|create_time|
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
      "catalog_id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "description": "string",
      "category": "table",
      "status": "active",
      "status_message": "string",
      "database": "string",
      "source_identifier": "string",
      "source_metadata": {},
      "extensions": {
        "property1": "string",
        "property2": "string"
      },
      "schema_definition": [
        {
          "name": "string",
          "type": "string",
          "display_name": "string",
          "description": "string",
          "original_name": "string",
          "original_type": "string",
          "original_description": "string",
          "features": [
            {
              "name": "string",
              "display_name": "string",
              "feature_type": "keyword",
              "description": "string",
              "ref_property": "string",
              "is_default": true,
              "is_native": true,
              "config": {}
            }
          ],
          "attributes": {},
          "extensions": {
            "property1": "string",
            "property2": "string"
          }
        }
      ],
      "index_name": "string",
      "logic_type": "derived",
      "logic_definition": [
        {
          "id": "string",
          "name": "string",
          "type": "string",
          "inputs": [
            "string"
          ],
          "config": {},
          "output_fields": [
            {}
          ]
        }
      ],
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
      "update_time": 0,
      "operations": [
        "string"
      ]
    }
  ],
  "total_count": 0
}
```

<h3 id="获取资源列表-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[ListResources](#schemalistresources)|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数 / 请求体非法。常见 errcode：
- `VegaBackend.InvalidParameter.RequestBody`：body schema 不合法
- `VegaBackend.Resource.InvalidParameter.*`：具体字段非法
- `VegaBackend.Resource.CategoryNotCreatable`：尝试通过 API 创建非 `dataset`/`logicview` 类资源
- `VegaBackend.Dataset.*`：dataset 创建时 `schema_definition` 为空 / 字段名 / 长度 / 重复 / 类型 / 特征校验失败
- `VegaBackend.LogicView.*`：logicview 创建时 `logic_definition` / 字段 / 特征校验失败
- `VegaBackend.Extensions.*`：`extensions` 形状、配额、保留 key；或 query 数组未成对|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="获取资源列表-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 创建资源

> Code samples

```shell
# You can also use wget
curl -X POST /api/vega-backend/v1/api/vega-backend/v1/resources \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json'

```

```http
POST /api/vega-backend/v1/api/vega-backend/v1/resources HTTP/1.1

Content-Type: application/json
Accept: application/json

```

```javascript
const inputBody = '{
  "id": "string",
  "catalog_id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "description": "string",
  "category": "table",
  "status": "active",
  "database": "string",
  "source_identifier": "string",
  "source_metadata": {},
  "extensions": {
    "property1": "string",
    "property2": "string"
  },
  "schema_definition": [
    {
      "name": "string",
      "type": "string",
      "display_name": "string",
      "description": "string",
      "original_name": "string",
      "original_type": "string",
      "original_description": "string",
      "features": [
        {
          "name": "string",
          "display_name": "string",
          "feature_type": "keyword",
          "description": "string",
          "ref_property": "string",
          "is_default": true,
          "is_native": true,
          "config": {}
        }
      ],
      "attributes": {},
      "extensions": {
        "property1": "string",
        "property2": "string"
      }
    }
  ],
  "logic_definition": [
    {
      "id": "string",
      "name": "string",
      "type": "string",
      "inputs": [
        "string"
      ],
      "config": {},
      "output_fields": [
        {}
      ]
    }
  ]
}';
const headers = {
  'Content-Type':'application/json',
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/resources',
{
  method: 'POST',
  body: inputBody,
  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Content-Type' => 'application/json',
  'Accept' => 'application/json'
}

result = RestClient.post '/api/vega-backend/v1/api/vega-backend/v1/resources',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Content-Type': 'application/json',
  'Accept': 'application/json'
}

r = requests.post('/api/vega-backend/v1/api/vega-backend/v1/resources', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Content-Type' => 'application/json',
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('POST','/api/vega-backend/v1/api/vega-backend/v1/resources', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/resources");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("POST");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Content-Type": []string{"application/json"},
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("POST", "/api/vega-backend/v1/api/vega-backend/v1/resources", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`POST /api/vega-backend/v1/resources`

创建 Resource。`catalog_id` 必填且 catalog 必须存在；不存在返回 404
`VegaBackend.Resource.CatalogNotFound`。

- `id` 字段可选；后端在不指定时生成。
- `category` 必须为支持的取值之一。**仅 `dataset` 与 `logicview` 允许通过本 API 创建**；
  其他类别（`table` / `file` / `fileset` / `api` / `metric` / `topic` / `index`）由 discover 任务产出，
  直接调用本接口创建会返回 400 `VegaBackend.Resource.CategoryNotCreatable`。
- `name` 在 catalog 内唯一；冲突返回 409 `VegaBackend.Resource.NameExists`。
- `category=logicview` 必须给 `logic_definition`。
- `category=dataset` 必须给非空 `schema_definition`；字段名/长度/重复/类型/特征均会校验，
  违反返回 400 `VegaBackend.Dataset.*`（详见错误码族）。
- 可选 **`extensions`**：与插入 `t_resource` **同一事务**内写入 `t_entity_extension`。

> Body parameter

```json
{
  "id": "string",
  "catalog_id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "description": "string",
  "category": "table",
  "status": "active",
  "database": "string",
  "source_identifier": "string",
  "source_metadata": {},
  "extensions": {
    "property1": "string",
    "property2": "string"
  },
  "schema_definition": [
    {
      "name": "string",
      "type": "string",
      "display_name": "string",
      "description": "string",
      "original_name": "string",
      "original_type": "string",
      "original_description": "string",
      "features": [
        {
          "name": "string",
          "display_name": "string",
          "feature_type": "keyword",
          "description": "string",
          "ref_property": "string",
          "is_default": true,
          "is_native": true,
          "config": {}
        }
      ],
      "attributes": {},
      "extensions": {
        "property1": "string",
        "property2": "string"
      }
    }
  ],
  "logic_definition": [
    {
      "id": "string",
      "name": "string",
      "type": "string",
      "inputs": [
        "string"
      ],
      "config": {},
      "output_fields": [
        {}
      ]
    }
  ]
}
```

<h3 id="创建资源-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|body|body|[ResourceRequest](#schemaresourcerequest)|true|none|

> Example responses

> 201 Response

```json
{
  "id": "string",
  "catalog_id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "description": "string",
  "category": "table",
  "status": "active",
  "status_message": "string",
  "database": "string",
  "source_identifier": "string",
  "source_metadata": {},
  "extensions": {
    "property1": "string",
    "property2": "string"
  },
  "schema_definition": [
    {
      "name": "string",
      "type": "string",
      "display_name": "string",
      "description": "string",
      "original_name": "string",
      "original_type": "string",
      "original_description": "string",
      "features": [
        {
          "name": "string",
          "display_name": "string",
          "feature_type": "keyword",
          "description": "string",
          "ref_property": "string",
          "is_default": true,
          "is_native": true,
          "config": {}
        }
      ],
      "attributes": {},
      "extensions": {
        "property1": "string",
        "property2": "string"
      }
    }
  ],
  "index_name": "string",
  "logic_type": "derived",
  "logic_definition": [
    {
      "id": "string",
      "name": "string",
      "type": "string",
      "inputs": [
        "string"
      ],
      "config": {},
      "output_fields": [
        {}
      ]
    }
  ],
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
  "update_time": 0,
  "operations": [
    "string"
  ]
}
```

<h3 id="创建资源-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|201|[Created](https://tools.ietf.org/html/rfc7231#section-6.3.2)|创建成功|[Resource](#schemaresource)|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数 / 请求体非法。常见 errcode：
- `VegaBackend.InvalidParameter.RequestBody`：body schema 不合法
- `VegaBackend.Resource.InvalidParameter.*`：具体字段非法
- `VegaBackend.Resource.CategoryNotCreatable`：尝试通过 API 创建非 `dataset`/`logicview` 类资源
- `VegaBackend.Dataset.*`：dataset 创建时 `schema_definition` 为空 / 字段名 / 长度 / 重复 / 类型 / 特征校验失败
- `VegaBackend.LogicView.*`：logicview 创建时 `logic_definition` / 字段 / 特征校验失败
- `VegaBackend.Extensions.*`：`extensions` 形状、配额、保留 key；或 query 数组未成对|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|Resource 或 Catalog 不存在。errcode：
- `VegaBackend.Resource.NotFound`：resource id 不存在
- `VegaBackend.Resource.CatalogNotFound`：创建时 catalog_id 不存在|None|
|406|[Not Acceptable](https://tools.ietf.org/html/rfc7231#section-6.5.6)|Content-Type 不是 application/json|None|
|409|[Conflict](https://tools.ietf.org/html/rfc7231#section-6.5.8)|资源冲突。常见 errcode：
- `VegaBackend.Resource.NameExists`：catalog 内 name 已被占用|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="创建资源-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 批量获取资源

> Code samples

```shell
# You can also use wget
curl -X GET /api/vega-backend/v1/api/vega-backend/v1/resources/{ids} \
  -H 'Accept: application/json'

```

```http
GET /api/vega-backend/v1/api/vega-backend/v1/resources/{ids} HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/resources/{ids}',
{
  method: 'GET',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.get '/api/vega-backend/v1/api/vega-backend/v1/resources/{ids}',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.get('/api/vega-backend/v1/api/vega-backend/v1/resources/{ids}', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('GET','/api/vega-backend/v1/api/vega-backend/v1/resources/{ids}', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/resources/{ids}");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("GET");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("GET", "/api/vega-backend/v1/api/vega-backend/v1/resources/{ids}", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`GET /api/vega-backend/v1/resources/{ids}`

按 ids 批量取 Resource。**整体事务语义**：任一 id 不存在返回 404
`VegaBackend.Resource.NotFound`，`error_details` 含具体缺失 id。
每条 `Resource` 含 **`extensions`**（无副表行时为 `{}`）。

<h3 id="批量获取资源-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|ids|path|string|true|Resource ID 列表，逗号分隔（单条即长度 1 的退化情形）。|

#### Detailed descriptions

**ids**: Resource ID 列表，逗号分隔（单条即长度 1 的退化情形）。

> Example responses

> 200 Response

```json
{
  "entries": [
    {
      "id": "string",
      "catalog_id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "description": "string",
      "category": "table",
      "status": "active",
      "status_message": "string",
      "database": "string",
      "source_identifier": "string",
      "source_metadata": {},
      "extensions": {
        "property1": "string",
        "property2": "string"
      },
      "schema_definition": [
        {
          "name": "string",
          "type": "string",
          "display_name": "string",
          "description": "string",
          "original_name": "string",
          "original_type": "string",
          "original_description": "string",
          "features": [
            {
              "name": "string",
              "display_name": "string",
              "feature_type": "keyword",
              "description": "string",
              "ref_property": "string",
              "is_default": true,
              "is_native": true,
              "config": {}
            }
          ],
          "attributes": {},
          "extensions": {
            "property1": "string",
            "property2": "string"
          }
        }
      ],
      "index_name": "string",
      "logic_type": "derived",
      "logic_definition": [
        {
          "id": "string",
          "name": "string",
          "type": "string",
          "inputs": [
            "string"
          ],
          "config": {},
          "output_fields": [
            {}
          ]
        }
      ],
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
      "update_time": 0,
      "operations": [
        "string"
      ]
    }
  ]
}
```

<h3 id="批量获取资源-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|ok|[BatchResources](#schemabatchresources)|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|Resource 或 Catalog 不存在。errcode：
- `VegaBackend.Resource.NotFound`：resource id 不存在
- `VegaBackend.Resource.CatalogNotFound`：创建时 catalog_id 不存在|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="批量获取资源-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 批量删除资源

> Code samples

```shell
# You can also use wget
curl -X DELETE /api/vega-backend/v1/api/vega-backend/v1/resources/{ids} \
  -H 'Accept: application/json'

```

```http
DELETE /api/vega-backend/v1/api/vega-backend/v1/resources/{ids} HTTP/1.1

Accept: application/json

```

```javascript

const headers = {
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/resources/{ids}',
{
  method: 'DELETE',

  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Accept' => 'application/json'
}

result = RestClient.delete '/api/vega-backend/v1/api/vega-backend/v1/resources/{ids}',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Accept': 'application/json'
}

r = requests.delete('/api/vega-backend/v1/api/vega-backend/v1/resources/{ids}', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('DELETE','/api/vega-backend/v1/api/vega-backend/v1/resources/{ids}', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/resources/{ids}");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("DELETE");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("DELETE", "/api/vega-backend/v1/api/vega-backend/v1/resources/{ids}", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`DELETE /api/vega-backend/v1/resources/{ids}`

按 ids 批量删除 Resource。**整体事务**：所有 id 通过预校验后才进入删除阶段，
任一不存在返回 404 `VegaBackend.Resource.NotFound`，整批不删。

可选 `?ignore_missing=true`：忽略不存在的 id（视为已删除），其它 id 正常删。

删除成功后须同事务移除 `t_entity_extension` 中对应 `f_entity_id` 的行。

<h3 id="批量删除资源-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|ignore_missing|query|boolean|false|忽略不存在的 id；默认 false|
|ids|path|string|true|Resource ID 列表，逗号分隔（单条即长度 1 的退化情形）。|

#### Detailed descriptions

**ids**: Resource ID 列表，逗号分隔（单条即长度 1 的退化情形）。

> Example responses

<h3 id="批量删除资源-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|删除成功|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|Resource 或 Catalog 不存在。errcode：
- `VegaBackend.Resource.NotFound`：resource id 不存在
- `VegaBackend.Resource.CatalogNotFound`：创建时 catalog_id 不存在|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="批量删除资源-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

## 更新资源

> Code samples

```shell
# You can also use wget
curl -X PUT /api/vega-backend/v1/api/vega-backend/v1/resources/{id} \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json'

```

```http
PUT /api/vega-backend/v1/api/vega-backend/v1/resources/{id} HTTP/1.1

Content-Type: application/json
Accept: application/json

```

```javascript
const inputBody = '{
  "id": "string",
  "catalog_id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "description": "string",
  "category": "table",
  "status": "active",
  "database": "string",
  "source_identifier": "string",
  "source_metadata": {},
  "extensions": {
    "property1": "string",
    "property2": "string"
  },
  "schema_definition": [
    {
      "name": "string",
      "type": "string",
      "display_name": "string",
      "description": "string",
      "original_name": "string",
      "original_type": "string",
      "original_description": "string",
      "features": [
        {
          "name": "string",
          "display_name": "string",
          "feature_type": "keyword",
          "description": "string",
          "ref_property": "string",
          "is_default": true,
          "is_native": true,
          "config": {}
        }
      ],
      "attributes": {},
      "extensions": {
        "property1": "string",
        "property2": "string"
      }
    }
  ],
  "logic_definition": [
    {
      "id": "string",
      "name": "string",
      "type": "string",
      "inputs": [
        "string"
      ],
      "config": {},
      "output_fields": [
        {}
      ]
    }
  ]
}';
const headers = {
  'Content-Type':'application/json',
  'Accept':'application/json'
};

fetch('/api/vega-backend/v1/api/vega-backend/v1/resources/{id}',
{
  method: 'PUT',
  body: inputBody,
  headers: headers
})
.then(function(res) {
    return res.json();
}).then(function(body) {
    console.log(body);
});

```

```ruby
require 'rest-client'
require 'json'

headers = {
  'Content-Type' => 'application/json',
  'Accept' => 'application/json'
}

result = RestClient.put '/api/vega-backend/v1/api/vega-backend/v1/resources/{id}',
  params: {
  }, headers: headers

p JSON.parse(result)

```

```python
import requests
headers = {
  'Content-Type': 'application/json',
  'Accept': 'application/json'
}

r = requests.put('/api/vega-backend/v1/api/vega-backend/v1/resources/{id}', headers = headers)

print(r.json())

```

```php
<?php

require 'vendor/autoload.php';

$headers = array(
    'Content-Type' => 'application/json',
    'Accept' => 'application/json',
);

$client = new \GuzzleHttp\Client();

// Define array of request body.
$request_body = array();

try {
    $response = $client->request('PUT','/api/vega-backend/v1/api/vega-backend/v1/resources/{id}', array(
        'headers' => $headers,
        'json' => $request_body,
       )
    );
    print_r($response->getBody()->getContents());
 }
 catch (\GuzzleHttp\Exception\BadResponseException $e) {
    // handle exception or api errors.
    print_r($e->getMessage());
 }

 // ...

```

```java
URL obj = new URL("/api/vega-backend/v1/api/vega-backend/v1/resources/{id}");
HttpURLConnection con = (HttpURLConnection) obj.openConnection();
con.setRequestMethod("PUT");
int responseCode = con.getResponseCode();
BufferedReader in = new BufferedReader(
    new InputStreamReader(con.getInputStream()));
String inputLine;
StringBuffer response = new StringBuffer();
while ((inputLine = in.readLine()) != null) {
    response.append(inputLine);
}
in.close();
System.out.println(response.toString());

```

```go
package main

import (
       "bytes"
       "net/http"
)

func main() {

    headers := map[string][]string{
        "Content-Type": []string{"application/json"},
        "Accept": []string{"application/json"},
    }

    data := bytes.NewBuffer([]byte{jsonReq})
    req, err := http.NewRequest("PUT", "/api/vega-backend/v1/api/vega-backend/v1/resources/{id}", data)
    req.Header = headers

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}

```

`PUT /api/vega-backend/v1/resources/{id}`

更新 Resource。

- `name` 在 catalog 内唯一；改名为已占用值返回 409 `VegaBackend.Resource.NameExists`。
- 不能跨 catalog 移动（`catalog_id` 与原值不一致时按 service 层规则处理）。
- **extensions**：请求体若包含 **`extensions` 键**（含 `{}`）则整包替换该 resource 的 `t_entity_extension` 行；
  **键未出现**则不修改副表。

> Body parameter

```json
{
  "id": "string",
  "catalog_id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "description": "string",
  "category": "table",
  "status": "active",
  "database": "string",
  "source_identifier": "string",
  "source_metadata": {},
  "extensions": {
    "property1": "string",
    "property2": "string"
  },
  "schema_definition": [
    {
      "name": "string",
      "type": "string",
      "display_name": "string",
      "description": "string",
      "original_name": "string",
      "original_type": "string",
      "original_description": "string",
      "features": [
        {
          "name": "string",
          "display_name": "string",
          "feature_type": "keyword",
          "description": "string",
          "ref_property": "string",
          "is_default": true,
          "is_native": true,
          "config": {}
        }
      ],
      "attributes": {},
      "extensions": {
        "property1": "string",
        "property2": "string"
      }
    }
  ],
  "logic_definition": [
    {
      "id": "string",
      "name": "string",
      "type": "string",
      "inputs": [
        "string"
      ],
      "config": {},
      "output_fields": [
        {}
      ]
    }
  ]
}
```

<h3 id="更新资源-parameters">Parameters</h3>

|Name|In|Type|Required|Description|
|---|---|---|---|---|
|body|body|[ResourceRequest](#schemaresourcerequest)|true|none|
|id|path|string|true|Resource ID|

> Example responses

<h3 id="更新资源-responses">Responses</h3>

|Status|Meaning|Description|Schema|
|---|---|---|---|
|204|[No Content](https://tools.ietf.org/html/rfc7231#section-6.3.5)|更新成功|None|
|400|[Bad Request](https://tools.ietf.org/html/rfc7231#section-6.5.1)|请求参数 / 请求体非法。常见 errcode：
- `VegaBackend.InvalidParameter.RequestBody`：body schema 不合法
- `VegaBackend.Resource.InvalidParameter.*`：具体字段非法
- `VegaBackend.Resource.CategoryNotCreatable`：尝试通过 API 创建非 `dataset`/`logicview` 类资源
- `VegaBackend.Dataset.*`：dataset 创建时 `schema_definition` 为空 / 字段名 / 长度 / 重复 / 类型 / 特征校验失败
- `VegaBackend.LogicView.*`：logicview 创建时 `logic_definition` / 字段 / 特征校验失败
- `VegaBackend.Extensions.*`：`extensions` 形状、配额、保留 key；或 query 数组未成对|None|
|401|[Unauthorized](https://tools.ietf.org/html/rfc7235#section-3.1)|未授权（OAuth Token 校验失败）|None|
|404|[Not Found](https://tools.ietf.org/html/rfc7231#section-6.5.4)|Resource 或 Catalog 不存在。errcode：
- `VegaBackend.Resource.NotFound`：resource id 不存在
- `VegaBackend.Resource.CatalogNotFound`：创建时 catalog_id 不存在|None|
|406|[Not Acceptable](https://tools.ietf.org/html/rfc7231#section-6.5.6)|Content-Type 不是 application/json|None|
|409|[Conflict](https://tools.ietf.org/html/rfc7231#section-6.5.8)|资源冲突。常见 errcode：
- `VegaBackend.Resource.NameExists`：catalog 内 name 已被占用|None|
|500|[Internal Server Error](https://tools.ietf.org/html/rfc7231#section-6.6.1)|服务内部错误|None|

<h3 id="更新资源-responseschema">Response Schema</h3>

<aside class="success">
This operation does not require authentication
</aside>

# Schemas

<h2 id="tocS_EntityExtensions">EntityExtensions</h2>
<!-- backwards compatibility -->
<a id="schemaentityextensions"></a>
<a id="schema_EntityExtensions"></a>
<a id="tocSentityextensions"></a>
<a id="tocsentityextensions"></a>

```json
{
  "property1": "string",
  "property2": "string"
}

```

扁平 KV 的 JSON object（`string`→`string`）。用于 **`Resource` / `ResourceRequest`** 上的 **`extensions`**
属性；语义与约束同 [catalog.yaml](catalog.yaml) 中 `EntityExtensions`。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|**additionalProperties**|string|false|none|none|

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

操作者信息

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|none|
|type|string|true|none|none|
|name|string|false|none|none|

<h2 id="tocS_Property">Property</h2>
<!-- backwards compatibility -->
<a id="schemaproperty"></a>
<a id="schema_Property"></a>
<a id="tocSproperty"></a>
<a id="tocsproperty"></a>

```json
{
  "name": "string",
  "type": "string",
  "display_name": "string",
  "description": "string",
  "original_name": "string",
  "original_type": "string",
  "original_description": "string",
  "features": [
    {
      "name": "string",
      "display_name": "string",
      "feature_type": "keyword",
      "description": "string",
      "ref_property": "string",
      "is_default": true,
      "is_native": true,
      "config": {}
    }
  ],
  "attributes": {},
  "extensions": {
    "property1": "string",
    "property2": "string"
  }
}

```

Schema 字段定义

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|字段名|
|type|string|true|none|VEGA 统一类型，取值见 [vega-backend/CLAUDE.md]:<br>integer / unsigned_integer / float / decimal / string / text /<br>date / datetime / time / boolean / binary / json / vector|
|display_name|string|false|none|显示名|
|description|string|false|none|none|
|original_name|string|false|none|源端字段原名（建表脚本里的字段名）。<br>discover 每次刷新，**永远反映源端当前值**，不是首次扫描快照。|
|original_type|string|false|none|源端原始类型字符串（如 mysql 的 `varchar(255)`）。<br>discover 每次刷新，永远反映源端当前值。|
|original_description|string|false|none|源端字段原始描述（如 mysql COLUMN_COMMENT）。<br>discover 每次刷新，永远反映源端当前值。|
|features|[[PropertyFeature](#schemapropertyfeature)]|false|none|字段特性集合|
|attributes|object|false|none|connector-specific 扩展属性，opaque|
|extensions|object|false|none|业务域外扁平 KV（`string`→`string`），**仅展示**；不参与 `GET /resources` 列表按 key/value 筛选。<br>持久化在 `schema_definition` JSON 内，**不**写入 `t_entity_extension`。与根级 Resource `extensions` 形态一致，见<br>[catalog-resource-labels-scheme-b-design.md](../../../design/vega/features/vega-backend/dip-for-extension/catalog-resource-labels-scheme-b-design.md) §6.11。|
|» **additionalProperties**|string|false|none|none|

<h2 id="tocS_PropertyFeature">PropertyFeature</h2>
<!-- backwards compatibility -->
<a id="schemapropertyfeature"></a>
<a id="schema_PropertyFeature"></a>
<a id="tocSpropertyfeature"></a>
<a id="tocspropertyfeature"></a>

```json
{
  "name": "string",
  "display_name": "string",
  "feature_type": "keyword",
  "description": "string",
  "ref_property": "string",
  "is_default": true,
  "is_native": true,
  "config": {}
}

```

字段特性（决定字段在索引/查询/向量化中的行为）

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|name|string|true|none|none|
|display_name|string|false|none|none|
|feature_type|string|true|none|特性类型|
|description|string|false|none|none|
|ref_property|string|false|none|关联到的字段名（如 vector 特性引用源字段）|
|is_default|boolean|false|none|none|
|is_native|boolean|false|none|none|
|config|object|false|none|特性配置，opaque|

#### Enumerated Values

|Property|Value|
|---|---|
|feature_type|keyword|
|feature_type|fulltext|
|feature_type|vector|

<h2 id="tocS_LogicDefinitionNode">LogicDefinitionNode</h2>
<!-- backwards compatibility -->
<a id="schemalogicdefinitionnode"></a>
<a id="schema_LogicDefinitionNode"></a>
<a id="tocSlogicdefinitionnode"></a>
<a id="tocslogicdefinitionnode"></a>

```json
{
  "id": "string",
  "name": "string",
  "type": "string",
  "inputs": [
    "string"
  ],
  "config": {},
  "output_fields": [
    {}
  ]
}

```

逻辑视图（`category=logicview`）定义图中的一个节点。

节点类型 `type` 已知取值（不限于）：

- `resource` — 引用一个 resource 作为数据源；`config` 形如 `ResourceNodeCfg`
- `join` — 多输入连接；`config` 形如 `JoinNodeCfg`（含 join_type / join_on / filters）
- 其他类型（filter / aggregate 等）按 connector 实现扩展

`config` 字段结构依赖 `type`，OpenAPI 不展开为 union——保留 opaque object。
`inputs` 是上游节点 ID 列表，构成 DAG。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|节点 ID（逻辑视图内 unique）|
|name|string|true|none|节点名（用于 SQL 引用 / 调试）|
|type|string|true|none|节点类型|
|inputs|[string]|true|none|上游节点 ID 列表（构成 DAG）|
|config|object|true|none|节点配置，结构依赖 `type`；opaque|
|output_fields|[object]|true|none|节点输出字段集合|

<h2 id="tocS_Resource">Resource</h2>
<!-- backwards compatibility -->
<a id="schemaresource"></a>
<a id="schema_Resource"></a>
<a id="tocSresource"></a>
<a id="tocsresource"></a>

```json
{
  "id": "string",
  "catalog_id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "description": "string",
  "category": "table",
  "status": "active",
  "status_message": "string",
  "database": "string",
  "source_identifier": "string",
  "source_metadata": {},
  "extensions": {
    "property1": "string",
    "property2": "string"
  },
  "schema_definition": [
    {
      "name": "string",
      "type": "string",
      "display_name": "string",
      "description": "string",
      "original_name": "string",
      "original_type": "string",
      "original_description": "string",
      "features": [
        {
          "name": "string",
          "display_name": "string",
          "feature_type": "keyword",
          "description": "string",
          "ref_property": "string",
          "is_default": true,
          "is_native": true,
          "config": {}
        }
      ],
      "attributes": {},
      "extensions": {
        "property1": "string",
        "property2": "string"
      }
    }
  ],
  "index_name": "string",
  "logic_type": "derived",
  "logic_definition": [
    {
      "id": "string",
      "name": "string",
      "type": "string",
      "inputs": [
        "string"
      ],
      "config": {},
      "output_fields": [
        {}
      ]
    }
  ],
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
  "update_time": 0,
  "operations": [
    "string"
  ]
}

```

Data Resource 实体。

- `operations` 是后端计算的"该资源支持的操作集合"，**只读**字段（创建 / 更新
  请求中不接受）。
- `index_name` 由构建任务填充，外部一般不直接设置。
- `schema_definition` / `logic_definition` 互斥使用：物理资源用前者，逻辑视图
  用后者。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|true|none|资源 ID；小写字母、数字、下划线、连字符，不能以下划线开头，最大长度 40|
|catalog_id|string|true|none|所属 catalog ID|
|name|string|true|none|资源名称，必填，最大长度 255|
|tags|[string]|false|none|标签列表（可选），最多 5 个，每个标签非空且最大长度 40，不能包含特殊字符|
|description|string|false|none|描述，最大长度 1000|
|category|string|true|none|资源类型|
|status|string|true|none|资源状态|
|status_message|string|false|none|状态说明（stale/deprecated 时填充）|
|database|string|false|none|所属数据库（实例级 Catalog 时填充）|
|source_identifier|string|true|none|源端标识（原始表名 / 路径 / topic 名等）|
|source_metadata|object|false|none|源端配置（JSON），由 connector 实现解释|
|extensions|[EntityExtensions](#schemaentityextensions)|false|none|扁平 KV 的 JSON object（`string`→`string`）。用于 **`Resource` / `ResourceRequest`** 上的 **`extensions`**<br>属性；语义与约束同 [catalog.yaml](catalog.yaml) 中 `EntityExtensions`。|
|schema_definition|[[Property](#schemaproperty)]|false|none|Schema 定义（物理资源使用）|
|index_name|string|false|none|关联索引名（由构建任务填充）|
|logic_type|string|false|none|逻辑视图类型（仅 category=logicview 有意义）|
|logic_definition|[[LogicDefinitionNode](#schemalogicdefinitionnode)]|false|none|逻辑视图定义（仅 category=logicview 有意义）|
|creator|[AccountInfo](#schemaaccountinfo)|true|none|操作者信息|
|create_time|integer(int64)|true|none|创建时间，毫秒时间戳|
|updater|[AccountInfo](#schemaaccountinfo)|true|none|操作者信息|
|update_time|integer(int64)|true|none|更新时间，毫秒时间戳|
|operations|[string]|false|none|后端计算的支持操作集合（只读）|

#### Enumerated Values

|Property|Value|
|---|---|
|category|table|
|category|file|
|category|fileset|
|category|api|
|category|metric|
|category|topic|
|category|index|
|category|logicview|
|category|dataset|
|status|active|
|status|disabled|
|status|deprecated|
|status|stale|
|logic_type|derived|
|logic_type|composite|

<h2 id="tocS_ResourceRequest">ResourceRequest</h2>
<!-- backwards compatibility -->
<a id="schemaresourcerequest"></a>
<a id="schema_ResourceRequest"></a>
<a id="tocSresourcerequest"></a>
<a id="tocsresourcerequest"></a>

```json
{
  "id": "string",
  "catalog_id": "string",
  "name": "string",
  "tags": [
    "string"
  ],
  "description": "string",
  "category": "table",
  "status": "active",
  "database": "string",
  "source_identifier": "string",
  "source_metadata": {},
  "extensions": {
    "property1": "string",
    "property2": "string"
  },
  "schema_definition": [
    {
      "name": "string",
      "type": "string",
      "display_name": "string",
      "description": "string",
      "original_name": "string",
      "original_type": "string",
      "original_description": "string",
      "features": [
        {
          "name": "string",
          "display_name": "string",
          "feature_type": "keyword",
          "description": "string",
          "ref_property": "string",
          "is_default": true,
          "is_native": true,
          "config": {}
        }
      ],
      "attributes": {},
      "extensions": {
        "property1": "string",
        "property2": "string"
      }
    }
  ],
  "logic_definition": [
    {
      "id": "string",
      "name": "string",
      "type": "string",
      "inputs": [
        "string"
      ],
      "config": {},
      "output_fields": [
        {}
      ]
    }
  ]
}

```

创建 / 更新请求体。与 `Resource` 实体相比，去掉了后端计算 / 时间戳 / 操作者
等只读字段。

可选 **`extensions`**（见 `EntityExtensions` 与 `catalog.yaml` `info.description`）。

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|id|string|false|none|可选；创建时不指定则后端生成；小写字母、数字、下划线、连字符，不能以下划线开头，最大长度 40|
|catalog_id|string|true|none|所属 catalog ID（必填）|
|name|string|true|none|资源名称，必填，最大长度 255|
|tags|[string]|false|none|标签列表（可选），最多 5 个，每个标签非空且最大长度 40，不能包含特殊字符|
|description|string|false|none|描述，最大长度 1000|
|category|string|true|none|资源类型|
|status|string|false|none|none|
|database|string|false|none|none|
|source_identifier|string|false|none|none|
|source_metadata|object|false|none|none|
|extensions|[EntityExtensions](#schemaentityextensions)|false|none|扁平 KV 的 JSON object（`string`→`string`）。用于 **`Resource` / `ResourceRequest`** 上的 **`extensions`**<br>属性；语义与约束同 [catalog.yaml](catalog.yaml) 中 `EntityExtensions`。|
|schema_definition|[[Property](#schemaproperty)]|false|none|[Schema 字段定义]|
|logic_definition|[[LogicDefinitionNode](#schemalogicdefinitionnode)]|false|none|[逻辑视图（`category=logicview`）定义图中的一个节点。<br><br>节点类型 `type` 已知取值（不限于）：<br><br>- `resource` — 引用一个 resource 作为数据源；`config` 形如 `ResourceNodeCfg`<br>- `join` — 多输入连接；`config` 形如 `JoinNodeCfg`（含 join_type / join_on / filters）<br>- 其他类型（filter / aggregate 等）按 connector 实现扩展<br><br>`config` 字段结构依赖 `type`，OpenAPI 不展开为 union——保留 opaque object。<br>`inputs` 是上游节点 ID 列表，构成 DAG。<br>]|

#### Enumerated Values

|Property|Value|
|---|---|
|category|table|
|category|file|
|category|fileset|
|category|api|
|category|metric|
|category|topic|
|category|index|
|category|logicview|
|category|dataset|
|status|active|
|status|disabled|
|status|deprecated|
|status|stale|

<h2 id="tocS_ListResources">ListResources</h2>
<!-- backwards compatibility -->
<a id="schemalistresources"></a>
<a id="schema_ListResources"></a>
<a id="tocSlistresources"></a>
<a id="tocslistresources"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "catalog_id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "description": "string",
      "category": "table",
      "status": "active",
      "status_message": "string",
      "database": "string",
      "source_identifier": "string",
      "source_metadata": {},
      "extensions": {
        "property1": "string",
        "property2": "string"
      },
      "schema_definition": [
        {
          "name": "string",
          "type": "string",
          "display_name": "string",
          "description": "string",
          "original_name": "string",
          "original_type": "string",
          "original_description": "string",
          "features": [
            {
              "name": "string",
              "display_name": "string",
              "feature_type": "keyword",
              "description": "string",
              "ref_property": "string",
              "is_default": true,
              "is_native": true,
              "config": {}
            }
          ],
          "attributes": {},
          "extensions": {
            "property1": "string",
            "property2": "string"
          }
        }
      ],
      "index_name": "string",
      "logic_type": "derived",
      "logic_definition": [
        {
          "id": "string",
          "name": "string",
          "type": "string",
          "inputs": [
            "string"
          ],
          "config": {},
          "output_fields": [
            {}
          ]
        }
      ],
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
      "update_time": 0,
      "operations": [
        "string"
      ]
    }
  ],
  "total_count": 0
}

```

资源列表响应

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[Resource](#schemaresource)]|true|none|[Data Resource 实体。<br><br>- `operations` 是后端计算的"该资源支持的操作集合"，**只读**字段（创建 / 更新<br>  请求中不接受）。<br>- `index_name` 由构建任务填充，外部一般不直接设置。<br>- `schema_definition` / `logic_definition` 互斥使用：物理资源用前者，逻辑视图<br>  用后者。<br>]|
|total_count|integer(int64)|true|none|none|

<h2 id="tocS_BatchResources">BatchResources</h2>
<!-- backwards compatibility -->
<a id="schemabatchresources"></a>
<a id="schema_BatchResources"></a>
<a id="tocSbatchresources"></a>
<a id="tocsbatchresources"></a>

```json
{
  "entries": [
    {
      "id": "string",
      "catalog_id": "string",
      "name": "string",
      "tags": [
        "string"
      ],
      "description": "string",
      "category": "table",
      "status": "active",
      "status_message": "string",
      "database": "string",
      "source_identifier": "string",
      "source_metadata": {},
      "extensions": {
        "property1": "string",
        "property2": "string"
      },
      "schema_definition": [
        {
          "name": "string",
          "type": "string",
          "display_name": "string",
          "description": "string",
          "original_name": "string",
          "original_type": "string",
          "original_description": "string",
          "features": [
            {
              "name": "string",
              "display_name": "string",
              "feature_type": "keyword",
              "description": "string",
              "ref_property": "string",
              "is_default": true,
              "is_native": true,
              "config": {}
            }
          ],
          "attributes": {},
          "extensions": {
            "property1": "string",
            "property2": "string"
          }
        }
      ],
      "index_name": "string",
      "logic_type": "derived",
      "logic_definition": [
        {
          "id": "string",
          "name": "string",
          "type": "string",
          "inputs": [
            "string"
          ],
          "config": {},
          "output_fields": [
            {}
          ]
        }
      ],
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
      "update_time": 0,
      "operations": [
        "string"
      ]
    }
  ]
}

```

批量获取响应（无 total_count，因为整体事务且数量等于请求 ids 数）

### Properties

|Name|Type|Required|Restrictions|Description|
|---|---|---|---|---|
|entries|[[Resource](#schemaresource)]|true|none|[Data Resource 实体。<br><br>- `operations` 是后端计算的"该资源支持的操作集合"，**只读**字段（创建 / 更新<br>  请求中不接受）。<br>- `index_name` 由构建任务填充，外部一般不直接设置。<br>- `schema_definition` / `logic_definition` 互斥使用：物理资源用前者，逻辑视图<br>  用后者。<br>]|



