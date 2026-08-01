/******************************************************************************
* 版权信息：中电科金仓（北京）科技股份有限公司

* 作者：KingbaseES

* 文件名：notify.go

* 功能描述：监听/通知相关接口

* 其它说明：


* 修改记录：
  1.修改时间：

  2.修改人：

  3.修改内容：

******************************************************************************/

package gokb

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

func recvNotification(rb *readBuf) (n *Notification) {
	bePid := rb.int32()
	channel := rb.string()
	extra := rb.string()
	n = &Notification{bePid, channel, extra}
	return
}

// NewListenerConn创建一个新的ListenerConn
func NewListenerConn(name string, notificationChan chan<- *Notification) (lc *ListenerConn, err error) {
	lc, err = newDialListenerConn(defaultDialer{}, name, notificationChan)
	return
}

func newDialListenerConn(d Dialer, name string, c chan<- *Notification) (lc *ListenerConn, err error) {
	cn, err := DialOpen(d, name)
	if nil != err {
		lc = nil
		return
	}

	l := &ListenerConn{
		cn: cn.(*conn), notificationChan: c,
		connState: connStateIdle, replyChan: make(chan message, 2),
	}

	go l.listenerConnMain()
	lc = l
	err = nil
	return
}

// 只允许一个程序在连接上运行查询
// 所以在连接上发送的goroutine必须保持senderLock。
// 如果发生了不可恢复的错误,应放弃ListenerConn,并返回错误。
func (lc *ListenerConn) acquireSenderLock() (err error) {
	// 必须先获得senderLock以避免死锁
	lc.senderLock.Lock()
	lc.connectionLock.Lock()
	err = lc.err
	lc.connectionLock.Unlock()
	if nil != err {
		lc.senderLock.Unlock()
		return
	}
	err = nil
	return
}

func (lc *ListenerConn) releaseSenderLock() {
	lc.senderLock.Unlock()
	return
}

// setState将协议状态修改为newState。如果不允许修改到当前状态,返回false。
func (lc *ListenerConn) setState(newState int32) (state bool) {
	var expectedState int32

	switch newState {
	case connStateIdle:
		expectedState = connStateExpectReadyForQuery
	case connStateExpectResponse:
		expectedState = connStateIdle
	case connStateExpectReadyForQuery:
		expectedState = connStateExpectResponse
	default:
		panic(fmt.Sprintf("unexpected listenerConnState %d", newState))
	}
	state = atomic.CompareAndSwapInt32(&lc.connState, expectedState, newState)
	return
}

// 从kingbase后端接收消息,转发通知和查询回复,并保持内部状态与协议状态同步。
// 当连接丢失时返回,连接将会消失或应该被丢弃,因为我们不能在状态上与服务器后端达成一致。
func (lc *ListenerConn) listenerConnLoop() (err error) {
	defer errRecoverNoErrBadConn(&err)

	r := &readBuf{}
	for {
		t, recvErr := lc.cn.recvMessage(r)
		err = recvErr
		if nil != err {
			return err
		}

		switch t {
		case 'A':
			// recvNotification将所有数据复制,所以不需要担心缓冲区被覆盖。
			lc.notificationChan <- recvNotification(r)

		case 'T', 'D':

		case 'E':
			// 即使不在查询中,我们也可能收到错误响应;
			// 预计服务器将在此之后关闭连接
			// 但我们应该确保我们显示的错误是来自错误响应的错误 而不是io.ErrUnexpectedEOF。
			if !lc.setState(connStateExpectReadyForQuery) {
				err = parseError(r)
				return
			}
			lc.replyChan <- message{t, parseError(r)}

		case 'C', 'I':
			if !lc.setState(connStateExpectReadyForQuery) {
				err = fmt.Errorf("unexpected CommandComplete")
				return
			}

		case 'Z':
			if !lc.setState(connStateIdle) {
				err = fmt.Errorf("unexpected ReadyForQuery")
				return
			}
			lc.replyChan <- message{t, nil}

		case 'S':
		case 'N':
			if n := lc.cn.noticeHandler; nil != n {
				n(parseError(r))
			}
		default:
			err = fmt.Errorf("unexpected message %q from server in listenerConnLoop", t)
			return
		}
	}
}

// listenerConnMain主要用于接收并处理来自数据库的通知等消息
// 主要是调用listenerConnLoop实现处理消息
func (lc *ListenerConn) listenerConnMain() {
	err := lc.listenerConnLoop()

	// listenerConnLoop已经处理完来自数据库的消息，先关闭数据库连接
	// 一个连接可以在此处关闭也可以被使用该连接的其它调用者关闭
	// 当本连接关闭后，另一个尝试关闭该连接的调用者会获得net.errClosed错误
	// 所以主动关闭本连接的调用者会获得更加准确有意义的错误消息
	// 也可能没有错误，但需要覆盖net.errClosed
	// 如果两个调用者在对套接字操作时连接丢失那么获得任一个调用者的错误都是可以接受的
	lc.connectionLock.Lock()
	if nil == lc.err {
		lc.err = err
	}
	lc.cn.Close()
	lc.connectionLock.Unlock()

	// 发送中的查询,确保没有调用者等待对该查询的响应
	close(lc.replyChan)

	// 通知监听者已完成
	close(lc.notificationChan)
	return
}

// 将监听器发送到服务器
func (lc *ListenerConn) Listen(channel string) (state bool, err error) {
	state, err = lc.ExecSimpleQuery("LISTEN " + QuoteIdentifier(channel))
	return
}

// Unlisten向服务器发送一个不监听的查询
func (lc *ListenerConn) Unlisten(channel string) (state bool, err error) {
	state, err = lc.ExecSimpleQuery("UNLISTEN " + QuoteIdentifier(channel))
	return
}

// UnlistenAll向服务器发送“UNLISTEN *”查询
func (lc *ListenerConn) UnlistenAll() (state bool, err error) {
	state, err = lc.ExecSimpleQuery("UNLISTEN *")
	return
}

// Ping远程服务器以确保它还存在。Non-nil错误意味着连接失败,应该被抛弃。
func (lc *ListenerConn) Ping() (err error) {
	sent, err := lc.ExecSimpleQuery("")
	if !sent {
		return
	}
	if nil != err {
		panic(err)
	}
	err = nil
	return
}

// 尝试在连接上发送查询。如果发送查询失败,返回错误,调用者应该关闭此连接。
// 调用者必须持有senderLock
func (lc *ListenerConn) sendSimpleQuery(query string) (err error) {
	defer errRecoverNoErrBadConn(&err)

	// 在发送查询之前必须设置连接状态
	if !lc.setState(connStateExpectResponse) {
		panic("two queries running at the same time")
	}

	// 不能在这里使用l.cn.writeBuf,
	// 因为它使用的是listenerConnLoop可能会重写缓冲区。
	wb := &writeBuf{
		buf: []byte("Q\x00\x00\x00\x00"),
		pos: 1,
	}
	wb.string(query)
	lc.cn.send(wb)
	err = nil
	return
}

// ExecSimpleQuery在连接上执行一个“简单查询”（即一个没有参数的查询）。可能的返回值为：
// 1）“执行”是正确的；在数据库服务器上完成了查询，
// 如果查询失败，错误将设置为数据库返回的错误,否则错误将为nil。
// 2）如果“执行”是错误的,则无法在远程服务器上执行查询。错误将是Non-nil。
// 在调用ExecSimpleQuery后返回一个已执行=false值，
// 连接要么关闭,要么将在此后不久关闭,所有随后执行的查询将返回一个错误。
func (lc *ListenerConn) ExecSimpleQuery(query string) (executed bool, err error) {
	if err = lc.acquireSenderLock(); nil != err {
		executed = false
		return
	}
	defer lc.releaseSenderLock()

	err = lc.sendSimpleQuery(query)
	if nil != err {
		lc.connectionLock.Lock()
		// 设置之前没有设置的错误指针
		if nil == lc.err {
			lc.err = err
		}
		lc.connectionLock.Unlock()
		lc.cn.c.Close()
		executed = false
		return
	}

	for {
		m, ok := <-lc.replyChan
		if !ok {
			// 失去了与服务器的连接,不要等待响应。错误消息已经被设置
			lc.connectionLock.Lock()
			lcErr := lc.err
			lc.connectionLock.Unlock()
			executed = false
			err = lcErr
			return
		}
		switch m.typ {
		case 'Z':
			if nil != m.err {
				panic("m.err != nil")
			}
			executed = true
			return

		case 'E':
			if nil == m.err {
				panic("m.err == nil")
			}
			err = m.err

		default:
			executed = false
			err = fmt.Errorf("unknown response for simple query: %q", m.typ)
			return
		}
	}
}

// 关闭连接
func (lc *ListenerConn) Close() (err error) {
	lc.connectionLock.Lock()
	if nil != lc.err {
		lc.connectionLock.Unlock()
		err = errListenerConnClosed
		return
	}
	lc.err = errListenerConnClosed
	lc.connectionLock.Unlock()
	// 不能在没有持有senderLock的情况下发送任何东西
	err = lc.cn.c.Close()
	return
}

// Err()返回连接关闭的原因
func (lc *ListenerConn) Err() (err error) {
	err = lc.err
	return
}

// NewListener创建一个用监听/通知的新数据库连接。
// 应该将名称设置为用于建立数据库连接的连接字符串
// minReconnectInterval控制在连接丢失后重新建立数据库连接的持续时间。
// 每次连续故障后,此间隔将增加一倍,直到达到maxReconnectInterval。
// 成功地完成连接的建立程序将这个区间重新设置为minReconnectInterval。
// 最后一个参数eventCallback可以设置为一个函数,当底层数据库连接的状态发生变化时,监听器将调用它。
// 这个回调将被goroutine调用,它在通知通道上发送通知,因此您应该尽量避免从回调中进行可能耗时的操作。
func NewListener(name string, minReconnectInterval time.Duration, maxReconnectInterval time.Duration, eventCallback EventCallbackType) (l *Listener) {
	l = NewDialListener(defaultDialer{}, name, minReconnectInterval, maxReconnectInterval, eventCallback)
	return
}

// nNewDialListener类似NewListener,但需要一个Dialer
func NewDialListener(d Dialer, name string, minReconnectInterval time.Duration, maxReconnectInterval time.Duration, eventCallback EventCallbackType) (l *Listener) {

	l = &Listener{name: name,
		minReconnectInterval: minReconnectInterval, maxReconnectInterval: maxReconnectInterval,
		dialer: d, eventCallback: eventCallback,
		channels: make(map[string]struct{}), Notify: make(chan *Notification, 32),
	}
	l.reconnectCond = sync.NewCond(&l.lock)
	go l.listenerMain()
	return l
}

// NotificationChannel为这个监听器返回通知通道。
// 这是与通知相同的通道,在监听者的生命周期内不会重新创建。
func (listener *Listener) NotificationChannel() <-chan *Notification {
	return listener.Notify
}

// Listen开始监听频道上的通知。调用此函数将阻塞，直到从服务器接收到通知。
// 注意，监听器在连接丢失后自动重新建立连接，因此如果无法重新建立连接，这个函数可能会无限期地阻塞。
// 监听只会在三个条件下失败:
// 1）频道已经打开。返回的错误为ErrChannelAlreadyOpen
// 2）在远程服务器上执行查询,但是Kingbase返回错误消息以响应查询。返回的错误是一个包含服务器提供的错误信息的gokb.Error
// 3）在请求完成之前在Listener上调用了Close
// 通道名称是大小写敏感的。
func (listener *Listener) Listen(channel string) (err error) {
	listener.lock.Lock()
	defer listener.lock.Unlock()

	if listener.isClosed {
		err = errListenerClosed
		return
	}

	// 服务器允许在一个已经打开的频道上进行LISTEN
	_, exists := listener.channels[channel]
	if exists {
		err = ErrChannelAlreadyOpen
		return
	}

	if nil != listener.cn {
		gotResponse, resErr := listener.cn.Listen(channel)
		err = resErr
		if gotResponse && nil != err {
			return
		}
	}

	listener.channels[channel] = struct{}{}
	for nil == listener.cn {
		listener.reconnectCond.Wait()
		if listener.isClosed {
			err = errListenerClosed
			return
		}
	}
	err = nil
	return
}

// 从监听器的通道列表中删除一个通道。
// 如果没有连接,立即返回没有错误。
// 如果监听器不在指定通道上监听，返回ErrChannelNotOpen。
func (listener *Listener) Unlisten(channel string) (err error) {
	listener.lock.Lock()
	defer listener.lock.Unlock()

	if listener.isClosed {
		err = errListenerClosed
		return
	}

	// 类似LISTEN
	_, exists := listener.channels[channel]
	if !exists {
		err = ErrChannelNotOpen
		return
	}

	if nil != listener.cn {
		gotResponse, resErr := listener.cn.Unlisten(channel)
		err = resErr
		if gotResponse && nil != err {
			return
		}
	}

	delete(listener.channels, channel)
	err = nil
	return
}

// 从监听器的通道列表中删除所有的通道。
// 如果没有连接,立即返回没有错误。
// 注意,即使在不监听后,仍然可能会收到任何删除通道的通知。
func (listener *Listener) UnlistenAll() (err error) {
	listener.lock.Lock()
	defer listener.lock.Unlock()

	if listener.isClosed {
		err = errListenerClosed
		return
	}

	if nil != listener.cn {
		gotResponse, resErr := listener.cn.UnlistenAll()
		err = resErr
		if gotResponse && nil != err {
			return
		}
	}

	listener.channels = make(map[string]struct{})
	err = nil
	return nil
}

// Ping远程服务器以确保它还存在。Non-nil返回值意味着没有活跃的连接。
func (listener *Listener) Ping() (err error) {
	listener.lock.Lock()
	defer listener.lock.Unlock()

	if listener.isClosed {
		err = errListenerClosed
		return
	}
	if nil == listener.cn {
		err = errors.New("no connection")
		return
	}
	err = listener.cn.Ping()
	return
}

// 在失去服务器连接后清理。返回包含导致连接丢失的原因的l.cn.Err()
func (listener *Listener) disconnectCleanup() (err error) {
	listener.lock.Lock()
	defer listener.lock.Unlock()

	// 检查；在通道关闭前，不需要调用Err()。
	select {
	case _, ok := <-listener.connNotificationChan:
		if ok {
			panic("connNotificationChan not closed")
		}
	default:
		panic("connNotificationChan not closed")
	}

	err = listener.cn.Err()
	listener.cn.Close()
	listener.cn = nil
	return
}

// 同步想要在连接建立后与服务器监听的通道列表。
func (listener *Listener) resync(cn *ListenerConn, notificationChan <-chan *Notification) (err error) {
	doneChan := make(chan error)
	go func(notificationChan <-chan *Notification) {
		for channel := range listener.channels {
			// 如果得到响应，将错误返回给调用者
			gotResponse, err := cn.Listen(channel)
			if gotResponse && nil != err {
				doneChan <- err
				return
			}

			// 如果无法到达服务器，等待notificationChan关闭，
			// 然后从连接中返回错误消息，类似ListenerConn的接口
			if nil != err {
				for range notificationChan {
				}
				doneChan <- cn.Err()
				return
			}
		}
		doneChan <- nil
	}(notificationChan)

	// 在同步过程中忽略通知,以避免死锁
	for {
		select {
		case _, ok := <-notificationChan:
			if !ok {
				notificationChan = nil
			}
		case err := <-doneChan:
			return err
		}
	}
}

func (listener *Listener) closed() (state bool) {
	listener.lock.Lock()
	defer listener.lock.Unlock()
	state = listener.isClosed
	return
}

func (listener *Listener) connect() (err error) {
	notificationChan := make(chan *Notification, 32)
	cn, err := newDialListenerConn(listener.dialer, listener.name, notificationChan)
	if nil != err {
		return
	}

	listener.lock.Lock()
	defer listener.lock.Unlock()

	err = listener.resync(cn, notificationChan)
	if nil != err {
		cn.Close()
		return
	}

	listener.cn, listener.connNotificationChan = cn, notificationChan
	listener.reconnectCond.Broadcast()
	err = nil
	return
}

// Close()将监听器从数据库中断开，并关闭数据库。
// 如果连接已经关闭则返回错误
func (listener *Listener) Close() (err error) {
	listener.lock.Lock()
	defer listener.lock.Unlock()

	if listener.isClosed {
		err = errListenerClosed
		return
	}

	if nil != listener.cn {
		listener.cn.Close()
	}
	listener.isClosed = true

	// 非阻塞的调用Listen()
	listener.reconnectCond.Broadcast()
	err = nil
	return
}

func (listener *Listener) emitEvent(event ListenerEventType, err error) {
	if nil != listener.eventCallback {
		listener.eventCallback(event, err)
	}
	return
}

// 在可能的情况下保持与服务器的连接，等待通知并发送事件。
func (listener *Listener) listenerConnLoop() {
	var nextReconnect time.Time

	reconnectInterval := listener.minReconnectInterval
	for {
		for {
			err := listener.connect()
			if nil == err {
				break
			}

			if listener.closed() {
				return
			}
			listener.emitEvent(ListenerEventConnectionAttemptFailed, err)

			time.Sleep(reconnectInterval)
			reconnectInterval = reconnectInterval * 2
			if listener.maxReconnectInterval < reconnectInterval {
				reconnectInterval = listener.maxReconnectInterval
			}
		}

		if nextReconnect.IsZero() {
			listener.emitEvent(ListenerEventConnected, nil)
		} else {
			listener.emitEvent(ListenerEventReconnected, nil)
			listener.Notify <- nil
		}

		reconnectInterval = listener.minReconnectInterval
		nextReconnect = time.Now().Add(reconnectInterval)

		for {
			notification, ok := <-listener.connNotificationChan
			if !ok {
				break
			}
			listener.Notify <- notification
		}

		err := listener.disconnectCleanup()
		if listener.closed() {
			return
		}
		listener.emitEvent(ListenerEventDisconnected, err)

		time.Sleep(time.Until(nextReconnect))
	}
	return
}

func (listener *Listener) listenerMain() {
	listener.listenerConnLoop()
	close(listener.Notify)
	return
}
