/******************************************************************************
* 版权信息：中电科金仓（北京）科技股份有限公司

* 作者：KingbaseES

* 文件名：connector.go

* 功能描述：连接相关的接口

* 其它说明：

* 修改记录：
  1.修改时间：

  2.修改人：

  3.修改内容：

******************************************************************************/

package gokb

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Connect使用此Connector的固定配置返回到数据库的连接
func (c *Connector) Connect(ctx context.Context) (driver.Conn, error) { return c.open(ctx) }

// Driver返回此Connector的底层驱动
func (c *Connector) Driver() driver.Driver { return &Driver{} }

// NewConnector返回具有给定dsn固定配置的gokb驱动的Connector
// 返回的Connector可用于创建任意数量的等效Conn
// 返回的Connector也将用于database/sql的OpenDB函数
func NewConnector(dsn string) (conn *Connector, err error) {
	o := make(values)
	var hosts []string
	var ports []string
	var timeout timeoutParams

	// 按以下顺序使用连接参数:
	//
	// * 默认值
	// * 环境变量
	// * 传入的具体连接参数
	o["host"] = "localhost"
	o["port"] = "54321"
	// 额外的浮点数可设置为3，但和KES的V7和之前版本会有冲突
	o["extra_float_digits"] = "2"
	for k, v := range parseEnviron(os.Environ()) {
		o[k] = v
	}

	if strings.HasPrefix(dsn, "kingbase://") {
		dsn, err = ParseURL(dsn)
		if nil != err {
			return nil, err
		}
	}

	if hosts, ports, err = parseOpts(dsn, o); err != nil {
		return nil, err
	}

	// 未给出应用名时使用默认的应用名
	if fallback, ok := o["fallback_application_name"]; ok {
		if _, ok := o["application_name"]; !ok {
			o["application_name"] = fallback
		}
	}

	// 不能使用除UTF-8以外的客户端编码，允许显示地设置为UTF-8
	// option中也可以设置客户端编码但一般将client_encoding作为单独的连接参数发送
	if enc, ok := o["client_encoding"]; ok && !isUTF8(enc) {
		return nil, errors.New("client_encoding must be absent or 'UTF8'")
	}
	o["client_encoding"] = "UTF8"
	if datestyle, ok := o["datestyle"]; ok {
		if "ISO, MDY" != datestyle {
			return nil, fmt.Errorf("setting datestyle must be absent or %v; got %v", "ISO, MDY", datestyle)
		}
	} else {
		o["datestyle"] = "ISO, MDY"
	}

	// 如果没有提供用户名，则使用当前操作系统的的用户名
	if _, ok := o["user"]; !ok {
		u, err := userCurrent()
		if nil != err {
			return nil, err
		}
		o["user"] = u
	}

	if v, ok := o["connect_timeout"]; ok {
		timeout.connect_timeout, err = strconv.Atoi(v)
		if nil != err {
			return nil, err
		}
	} else {
		timeout.connect_timeout = 0
	}
	if v, ok := o["keepalive_interval"]; ok {
		timeout.keepalive_interval, err = strconv.Atoi(v)
		if nil != err {
			return nil, err
		}
	} else {
		timeout.keepalive_interval = 0 //配置为0则go使用自身的默认表现15s
	}
	if v, ok := o["keepalive_count"]; ok {
		timeout.keepalive_count, err = strconv.Atoi(v)
		if nil != err {
			return nil, err
		}
	} else {
		timeout.keepalive_count = 10 //次数使用0并不会使用go的默认值10而是会报错无法连接，所以此处显式赋10
	}
	if v, ok := o["tcp_user_timeout"]; ok {
		timeout.tcp_user_timeout, err = strconv.Atoi(v)
		if nil != err {
			return nil, err
		}
	} else {
		timeout.tcp_user_timeout = 0
	}
	return &Connector{
		opts:   o,
		dialer: defaultDialer{d: CreateDialer(timeout)},
		hosts:  hosts,
		ports:  ports,
	}, nil
}
