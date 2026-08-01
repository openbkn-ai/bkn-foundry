//go:build darwin

/******************************************************************************
* 版权信息：中电科金仓（北京）科技股份有限公司

* 作者：KingbaseES

* 文件名：conn_darwin.go

* 功能描述：macOS平台前后端通信相关接口

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
	"syscall"
	"time"
)

func CreateDialer(timeout timeoutParams) net.Dialer {
	dialer := net.Dialer{
		Timeout:   time.Duration(timeout.connect_timeout) * time.Second,
		KeepAlive: time.Duration(timeout.keepalive_interval) * time.Second, //此处参数同时作用于keepalive_idle和keepalive_interval
		Control: func(network, address string, c syscall.RawConn) error {
			var controlErr error
			err := c.Control(func(fd uintptr) {
				// macOS使用TCP_KEEPALIVE而不是TCP_KEEPIDLE
				// TCP_KEEPALIVE在macOS上的值是0x10
				const TCP_KEEPALIVE = 0x10

				// 设置keepalive参数
				// 注意：macOS不支持TCP_KEEPCNT和TCP_USER_TIMEOUT
				// 只设置TCP_KEEPALIVE (相当于Linux的TCP_KEEPIDLE)
				controlErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, TCP_KEEPALIVE, timeout.keepalive_interval)
				if controlErr != nil {
					return
				}
			})
			if err != nil {
				return fmt.Errorf("raw control error: %w", err)
			}
			return controlErr
		},
	}
	return dialer
}
