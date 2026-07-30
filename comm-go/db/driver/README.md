# Installation
Simple install the package to your [$GOPATH](https://github.com/golang/go/wiki/GOPATH "GOPATH") with the [go tool](https://golang.org/cmd/go/ "go command") from shell:
```bash

```
Make sure [Git is installed](https://git-scm.com/downloads) on your machine and in your system's `PATH`.
# DNS Format
[username[:password]@][protocol[(host[:port])]]/dbname[?param1=value1&...&paramN=valueN]
username 用户名，password密码，protocol协议，host主机名，port 端口，dbname数据库名，paramN参数名，valueN，对应参数的值
parameter is optional,others are necessary.protocol use tcp.
参考https://github.com/go-sql-driver/mysql#parameters，连接的数据库不是mysql时,其他数据库不支持的参数会直接屏蔽掉
dsn example:
  without optional parameter: root:eisoo.com123@tcp(localhost:3320)/test
  with optional parameter: root:eisoo.com123@tcp(localhost:3320)/test?timeout=10s

# Usage
```
type RDSDriver
    // Open new Connection.
    // See https://github.com/go-sql-driver/mysql#dsn-data-source-name for how
    // the DSN string is formatted
    Open(dsn string) (driver.Conn, error)
```
# Example
Examples are available in example directory.
```go
import (
	_ "github.com/openbkn-ai/bkn-comm-go/db/driver"
)

// ...

```