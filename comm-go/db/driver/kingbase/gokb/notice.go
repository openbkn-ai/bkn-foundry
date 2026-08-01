//go:build go1.10

/*
*****************************************************************************
* 版权信息：中电科金仓（北京）科技股份有限公司

* 作者：KingbaseES

* 文件名：notice.go

* 功能描述：notice相关接口

* 其它说明：go1.10及以上版本可用

  - 修改记录：
    1.修改时间：

    2.修改人：

    3.修改内容：

*****************************************************************************
*/
package gokb

import (
	"context"
	"database/sql/driver"
)

// NoticeHandler返回给定连接上的通知处理程序(如果有的话)
// 如果c不是kb的连接则会发生pannic
func NoticeHandler(dc driver.Conn) (f func(*Error)) {
	return dc.(*conn).noticeHandler
}

// SetNoticeHandler设置指定连接上的指定的通知处理程序
// 如果c不是kb的连接则会发生pannic
// 一个空的handler可以用来进行取消设置
//
// 注意: 通知处理为同步执行，在处理程序返回之前不会继续处理命令
func SetNoticeHandler(dc driver.Conn, handler func(*Error)) {
	dc.(*conn).noticeHandler = handler
}

// NoticeHandlerConnector封装了一个常规的connector并在其上设置了一个通知处理程序
type NoticeHandlerConnector struct {
	driver.Connector
	noticeHandler func(*Error)
}

// Connect调用底层connector的connect方法然后设置通知处理程序
func (n *NoticeHandlerConnector) Connect(ctx context.Context) (dc driver.Conn, err error) {
	dc, err = n.Connector.Connect(ctx)
	if nil == err {
		SetNoticeHandler(dc, n.noticeHandler)
	}
	return dc, err
}

// ConnectorNoticeHandler返回当前设置的通知处理程序(如果有的话)
// 如果给定的connector不是ConnectorWithNoticeHandler的结果，则返回空
func ConnectorNoticeHandler(c driver.Connector) (f func(*Error)) {
	if c, ok := c.(*NoticeHandlerConnector); ok {
		return c.noticeHandler
	} else {
		return nil
	}
}

// ConnectorWithNoticeHandler创建或设置指定connector的指定处理程序
// 如果指定的connector是先前调用该函数返回的结果，则只需在指定的connector上设置并返回它即可
// 否则将返回一个新的connector，封装指定的connector并设置通知处理程序
// 可以用一个空的通知处理程序取消设置
//
// 返回的connector将用于database/sql.OpenDB.
//
// 注意:通知处理程序为同步执行，在返回之前不会继续处理命令
func ConnectorWithNoticeHandler(c driver.Connector, handler func(*Error)) (nhc *NoticeHandlerConnector) {
	if c, ok := c.(*NoticeHandlerConnector); ok {
		c.noticeHandler = handler
		return c
	} else {
		return &NoticeHandlerConnector{Connector: c, noticeHandler: handler}
	}
}
