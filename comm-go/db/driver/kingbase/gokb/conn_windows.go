//go:build windows

/******************************************************************************
* 版权信息：中电科金仓（北京）科技股份有限公司

* 作者：KingbaseES

* 文件名：conn.go

* 功能描述：前后端通信相关接口

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

	"golang.org/x/sys/windows"
)

func CreateDialer(timeout timeoutParams) net.Dialer {
	dialer := net.Dialer{
		Timeout:   time.Duration(timeout.connect_timeout) * time.Second,
		KeepAlive: time.Duration(timeout.keepalive_interval) * time.Second, //此处参数同时作用于keepalive_idle和keepalive_interval
		Control: func(network, address string, c syscall.RawConn) error {
			var controlErr error
			err := c.Control(func(fd uintptr) {
				// Windows特有的TCP配置
				handle := windows.Handle(fd)
				// 设置Keep-Alive参数（Windows方式）
				var keepAlive uint32 = 1
				controlErr = windows.SetsockoptInt(handle, windows.SOL_SOCKET, windows.SO_KEEPALIVE, int(keepAlive))
				if controlErr != nil {
					return
				}

				// Windows的TCP_NODELAY相当于禁用Nagle算法
				var noDelay uint32 = 1
				controlErr = windows.SetsockoptInt(handle, windows.IPPROTO_TCP, windows.TCP_NODELAY, int(noDelay))
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
