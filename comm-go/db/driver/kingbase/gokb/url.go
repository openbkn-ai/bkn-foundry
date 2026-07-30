/******************************************************************************
* 版权信息：中电科金仓（北京）科技股份有限公司

* 作者：KingbaseES

* 文件名：url.go

* 功能描述：url串处理相关接口

* 其它说明：

* 修改记录：
  1.修改时间：

  2.修改人：

  3.修改内容：

******************************************************************************/

package gokb

import (
	"fmt"
	"net"
	nurl "net/url"
	"sort"
	"strings"
)

// sql.Open()已经支持用户提供URL作为连接串:
//
//	sql.Open("kingbase", "kingbase://bob:secret@1.2.3.4:54321/mydb?sslmode=verify-full")
//
// 所以ParseURL一般不再被使用，但为了向后兼容所以保留
//
// ParseURL将url转为driver.Open需要的连接串
// 比如:
//
//	"kingbase://bob:secret@1.2.3.4:54321/mydb?sslmode=verify-full"
//
// 转为:
//
//	"user=bob password=secret host=1.2.3.4 port=54321 dbname=mydb sslmode=verify-full"
//
// 最简短的例子:
//
//	"kingbase://"
//
// 将转为空，并使用默认的连接参数
func ParseURL(url string) (s string, err error) {
	u, err := nurl.Parse(url)
	if nil != err {
		return "", err
	}

	if u.Scheme != "kingbase" {
		return "", fmt.Errorf("invalid connection protocol: %s", u.Scheme)
	}

	var kvs []string
	escaper := strings.NewReplacer(` `, `\ `, `'`, `\'`, `\`, `\\`)
	accrue := func(k, v string) {
		if "" != v {
			kvs = append(kvs, k+"="+escaper.Replace(v))
		}
	}

	if nil != u.User {
		v := u.User.Username()
		accrue("user", v)

		v, _ = u.User.Password()
		accrue("password", v)
	}

	if host, port, err := net.SplitHostPort(u.Host); nil != err {
		accrue("host", u.Host)
	} else {
		accrue("host", host)
		accrue("port", port)
	}

	if "" != u.Path {
		accrue("dbname", u.Path[1:])
	}

	q := u.Query()
	for k := range q {
		accrue(k, q.Get(k))
	}

	sort.Strings(kvs)
	return strings.Join(kvs, " "), nil
}
