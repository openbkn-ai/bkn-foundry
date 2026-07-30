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
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unsafe"

	"github.com/openbkn-ai/bkn-comm-go/db/driver/kingbase/gokb/oid"
	"github.com/openbkn-ai/bkn-comm-go/db/driver/kingbase/gokb/oid/mysqlOid"
	"github.com/openbkn-ai/bkn-comm-go/db/driver/kingbase/gokb/oid/oracleOid"
	"github.com/openbkn-ai/bkn-comm-go/db/driver/kingbase/gokb/oid/sqlserverOid"
	"github.com/openbkn-ai/bkn-comm-go/db/driver/kingbase/gokb/scram"
	"github.com/openbkn-ai/bkn-comm-go/db/driver/kingbase/gokb/sm3"

	"github.com/golang-sql/civil"
	"github.com/shopspring/decimal"
)

// Open打开一个到数据库的新连接，name为连接串
// 一般情况下不直接使用该函数，而是通过标准库的database/sql包来使用
func (d *Driver) Open(name string) (dc driver.Conn, err error) {
	dc, err = Open(name)
	return
}

func init() {
	sql.Register("kingbase", &Driver{})
	return
}

/*
isSqlserverProcName在sqlserver模式下判断s是否为存储过程的名字或是一个SQL文本
[..]为一个转义字段，[开启一个转义字段，]结束一个转义字段；
字母、_、#开始一个文本字段；
符号.开始一个非文本非转义字段；
以下情况认为该文本不是一个存储过程名字：
1.在文本字段出现转义开始字符[、(和空格，比如：
_testProc[extra]	#test_proc(extra)	test proc
2.在非文本、非转义字段出现除了转义开始字符[、两个连续的转义结束字符]]、文本开始字符和符号.以外的字符，比如：
[test].@table		123		[[test].123]
*/
func isSqlserverProcName(s string) bool {
	if len(s) == 0 {
		return false
	}
	const (
		outside = iota
		text
		escaped
	)
	st := outside
	var rn1, rPrev rune
	for _, r := range s {
		rPrev = rn1
		rn1 = r
		if st != escaped {
			switch r {
			// No newlines or string sequences.
			case '\n', '\r', '\'', ';':
				return false
			}
		}
		switch st {
		case outside:
			switch {
			case r == '[':
				st = escaped
			case r == ']' && rPrev == ']':
				st = escaped
			case unicode.IsLetter(r):
				st = text
			case r == '_':
				st = text
			case r == '#':
				st = text
			case r == '.':
			default:
				return false
			}
		case text:
			switch {
			case r == '.':
				st = outside
			case r == '[':
				return false
			case r == '(':
				return false
			case unicode.IsSpace(r):
				return false
			}
		case escaped:
			switch {
			case r == ']':
				st = outside
			}
		}
	}
	return true
}

func replaceProcName(query string, args []driver.NamedValue, stNameList []string) (string, []driver.Value, []string, error) {
	isProc := isSqlserverProcName(query)
	argNum := len(args)
	if isProc {
		//获取命名参数的参数值
		var argList []driver.Value
		for _, v := range args {
			argList = append(argList, v.Value)
		}
		//返回值不占用占位符
		for _, t := range argList {
			if _, ret := t.(*ReturnStatus); ret {
				argNum--
			}
		}

		//将存储过程名字替换为CALL调用
		//此时绑定参数的数量必须与调用的存储过程所包含的占位符数量相等
		query = "exec " + query + " "
		for i := 0; i < argNum; i++ {
			query = fmt.Sprintf("%s$%d", query, i+1)
			if i != argNum-1 {
				query = fmt.Sprintf("%s, ", query)
			}
		}

		return query, argList, stNameList, nil
	} else {
		//非sqlserver存储过程名的其它sql文本
		return replaceHolderMarkers(query, args, stNameList)
	}

}

func isNameExist(hoderList map[string]int, name string, markID int) int {
	_, ok := hoderList[name]
	if ok {
		//如果已有该占位符名字，则仍使用原本的匿名数字编号
		return hoderList[name]
	} else {
		//新的占位符名字，将其添加到hoderList中
		hoderList[name] = markID
		return 0
	}
}

func replaceHolderMarkers(query string, args []driver.NamedValue, stNameList []string) (string, []driver.Value, []string, error) {
	var i int
	var markID = 1
	var s string
	var inQuote bool = false
	var nameList []string
	var name string
	q := []rune(query)
	length := len(q)
	hoderList := make(map[string]int)

	if stNameList == nil {
		for i = 0; i < length; i++ {
			ch := q[i]
			if ch == '?' && !inQuote {
				s += "$"
				s += strconv.Itoa(markID)
				markID++
			} else if ch == ':' && !inQuote {
				if i+1 == length {
					s += ":"
					break
				}
				if q[i+1] == ':' {
					s += "::"
					i++
				} else if q[i+1] == '=' {
					s += ":"
				} else if unicode.IsLetter(rune(q[i+1])) && !inQuote {
					//获取所有占位符的名字
					for ; q[i+1] != ',' && q[i+1] != ')' && q[i+1] != ':' && q[i+1] != ' ' && q[i+1] != ';' && q[i+1] != '\n' && q[i+1] != '\t'; i++ {
						name += string(q[i+1])
						if i+2 == length {
							i++
							break
						}
					}
					if ok := isNameExist(hoderList, name, markID); ok != 0 {
						s += "$"
						s += strconv.Itoa(ok)
					} else {
						s += "$"
						s += strconv.Itoa(markID)
						markID++
						nameList = append(nameList, name)
					}
					name = ""
				} else {
					s += "$"
					s += strconv.Itoa(markID)
					markID++
					for ; unicode.IsNumber(rune(q[i+1])); i++ {
						if i+2 == length {
							i++
							break
						}
					}
				}
			} else if ch == '$' && !inQuote {
				if i+1 == length {
					s += "$"
					break
				}
				if unicode.IsLetter(rune(q[i+1])) && !inQuote {
					//获取所有占位符的名字
					for ; q[i+1] != ',' && q[i+1] != ')' && q[i+1] != ':' && q[i+1] != ' ' && q[i+1] != ';' && q[i+1] != '\n' && q[i+1] != '\t'; i++ {
						name += string(q[i+1])
						if i+2 == length {
							i++
							break
						}
					}
					if ok := isNameExist(hoderList, name, markID); ok != 0 {
						s += "$"
						s += strconv.Itoa(ok)
					} else {
						s += "$"
						s += strconv.Itoa(markID)
						markID++
						nameList = append(nameList, name)
					}
					name = ""
				} else {
					s += "$"
				}
			} else if ch == '@' && !inQuote {
				if i+1 == length {
					s += "$"
					break
				}
				if unicode.IsLetter(rune(q[i+1])) && !inQuote {
					//获取所有占位符的名字
					for ; q[i+1] != ',' && q[i+1] != ')' && q[i+1] != ':' && q[i+1] != ' ' && q[i+1] != ';' && q[i+1] != '\n' && q[i+1] != '\t'; i++ {
						name += string(q[i+1])
						if i+2 == length {
							i++
							break
						}
					}
					if ok := isNameExist(hoderList, name, markID); ok != 0 {
						s += "$"
						s += strconv.Itoa(ok)
					} else {
						s += "$"
						s += strconv.Itoa(markID)
						markID++
						nameList = append(nameList, name)
					}
					name = ""
				} else {
					s += "$"
				}
			} else {
				if ch == '\'' {
					inQuote = !inQuote
				}
				s += string(ch)
			}
		}
	} else {
		nameList = stNameList
	}

	if len(args) != 0 { //绑定参数
		if len(nameList) != 0 { //命名占位符
			nameMap := make(map[string]driver.Value)
			n := len(nameList)
			var retVal *ReturnStatus
			for _, v := range args {
				nameMap[v.Name] = v.Value
				if t, ret := v.Value.(*ReturnStatus); ret {
					retVal = t
					n++
				}
			}

			argList := make([]driver.Value, n)
			for i, v := range nameList {
				_, ok := nameMap[v]
				if ok {
					argList[i] = nameMap[v]
				} else {
					return s, argList, nameList, fmt.Errorf("no matched named placeholder:%s", v)
				}
			}
			if retVal != nil {
				argList[n-1] = retVal
			}
			return s, argList, nameList, nil
		} else { //匿名占位符
			argList := make([]driver.Value, len(args))
			for i, v := range args {
				argList[i] = v.Value
			}
			return s, argList, nil, nil
		}
	} else { //无绑定参数
		var argList []driver.Value
		for _, v := range args {
			argList = append(argList, v.Value)
		}
		return s, argList, nil, nil
	}
}

func (s transactionStatus) String() (result string) {
	switch s {
	case txnStatusIdle:
		result = "idle"
		return
	case txnStatusIdleInTransaction:
		result = "idle in transaction"
		return
	case txnStatusInFailedTransaction:
		result = "in a failed transaction"
		return
	default:
		errorf("unknown transactionStatus %d", s)
	}
	panic("not reached")
}

func (d defaultDialer) Dial(network, address string) (nc net.Conn, err error) {
	nc, err = d.d.Dial(network, address)
	return
}
func (d defaultDialer) DialTimeout(network, address string, timeout time.Duration) (nc net.Conn, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	nc, err = d.DialContext(ctx, network, address)
	return
}
func (d defaultDialer) DialContext(ctx context.Context, network, address string) (nc net.Conn, err error) {
	nc, err = d.d.DialContext(ctx, network, address)
	return
}

// 在解析连接串时处理客户端的有关设置
func (cn *conn) handleDriverSettings(o values) (err error) {
	boolSetting := func(key string, val *bool) (fErr error) {
		if value, ok := o[key]; ok {
			if "yes" == value {
				*val = true
			} else if "no" == value {
				*val = false
			} else {
				fErr = fmt.Errorf("unrecognized value %q for %s", value, key)
				return
			}
		}
		fErr = nil
		return
	}
	numSetting := func(key string, val *int) (fErr error) {
		if value, ok := o[key]; ok {
			*val, fErr = strconv.Atoi(value)
		}
		return
	}

	err = boolSetting("disable_prepared_binary_result", &cn.disablePreparedBinaryResult)
	if nil != err {
		return err
	}
	err = boolSetting("binary_parameters", &cn.binaryParameters)
	if nil != err {
		return err
	}

	//获取自增列id
	err = boolSetting("get_last_insert_id", &cn.getLastInsertId.enable)
	cn.getLastInsertId.isInsert = false
	if nil != err {
		return err
	}

	//多主机地址的连接重试次数和每次连接的延迟
	err = numSetting("retry", &cn.retry)
	if nil != err {
		return err
	}
	err = numSetting("delay", &cn.delay)
	if nil != err {
		return err
	}
	//多主机地址是否需要找主
	if value, ok := o["target_session_attrs"]; ok {
		if value == "read-write" {
			cn.requirePrimary = true
		} else if value == "any" {
			cn.requirePrimary = false
		} else {
			return fmt.Errorf("unrecognized value %q for target_session_attrs", value)
		}
	}
	return
}

func (cn *conn) handleKbpass(o values) {
	// 如果提供了密码则不需要处理.kbpass
	if _, ok := o["password"]; ok {
		return
	}
	filename := os.Getenv("KBPASSFILE")
	if "" == filename {
		// 此处的处理不适用于windows，因为windwos的默认文件名为%APPDATA%\kingbase\kbpass.conf
		// 因为glibc的bug:golang.org/issue/13470，此处用$HOME而不用user.Current
		userHome := os.Getenv("HOME")
		if "" == userHome {
			user, err := user.Current()
			if nil != err {
				return
			}
			userHome = user.HomeDir
		}
		filename = filepath.Join(userHome, ".kbpass")
	}
	fileinfo, err := os.Stat(filename)
	if nil != err {
		return
	}
	mode := fileinfo.Mode()
	// 对.kbpass的权限进行验证，与ksql处理类似
	if 0 != mode&(0x77) {
		return
	}
	file, err := os.Open(filename)
	if nil != err {
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(io.Reader(file))
	hostname := o["host"]
	ntw, _ := network(o)
	port, db, username := o["port"], o["dbname"], o["user"]

	getFields := func(s string) (result []string) {
		fs, f := make([]string, 0, 5), make([]rune, 0, len(s))
		esc := false
		for _, c := range s {
			switch {
			case esc:
				f = append(f, c)
				esc = false
			case '\\' == c:
				esc = true
			case ':' == c:
				fs = append(fs, string(f))
				f = f[:0]
			default:
				f = append(f, c)
			}
		}
		result = append(fs, string(f))
		return
	}
	for scanner.Scan() {
		line := scanner.Text()
		if 0 == len(line) || '#' == line[0] {
			continue
		}
		split := getFields(line)
		if 5 != len(split) {
			continue
		}
		if ("*" == split[0] || hostname == split[0] || ("localhost" == split[0] && ("" == hostname || "unix" == ntw))) && ("*" == split[1] || port == split[1]) && ("*" == split[2] || db == split[2]) && ("*" == split[3] || username == split[3]) {
			o["password"] = split[4]
			return
		}
	}
}

func (cn *conn) writeBuf(b byte) (wb *writeBuf) {
	cn.scratch[0] = b
	wb = &writeBuf{
		buf: cn.scratch[:5],
		pos: 1,
	}
	return
}

// Open打开一个到数据库的新连接，name为连接串
// 一般情况下不直接使用该函数，而是通过标准库的database/sql包来使用
func Open(dsn string) (dc driver.Conn, err error) {
	dc, err = DialOpen(defaultDialer{}, dsn)
	return
}

// DialOpen通过dialer打开一个到数据库的新连接
func DialOpen(d Dialer, dsn string) (dc driver.Conn, err error) {
	c, err := NewConnector(dsn)
	if nil != err {
		dc = nil
		return
	}
	//使用连接器中构建的dialer
	//c.dialer = d
	dc, err = c.open(context.Background())
	return
}

func (c *Connector) open(ctx context.Context) (cn *conn, err error) {
	// 处理初始化连接过程中的异常
	// 因为errRecover()会将所有的连接错误转换成ErrBadConns而不是展示真正的错误信息
	// 所以处理过程中不使用errRecover()
	defer errRecoverNoErrBadConn(&err)

	o := c.opts
	numHosts := len(c.hosts)
	numPorts := len(c.ports)
	numMatch := false
	i := 0
	j := 0

	cn = &conn{
		opts:   o,
		dialer: c.dialer,
	}
	err = cn.handleDriverSettings(o)
	if nil != err {
		cn = nil
		return
	}
	cn.handleKbpass(o)

	//支持多主机地址
	var lastErr error
	var panicking bool = true
	if numHosts != numPorts {
		if numPorts != 1 {
			err = fmt.Errorf("the number of host and port dose not match, host: %d, port: %d", numHosts, numPorts)
			cn = nil
			return
		}
	} else {
		numMatch = true
	}
	for {
		for j = 0; j < numHosts; j++ {
			o["host"] = c.hosts[j]
			if numMatch {
				o["port"] = c.ports[j]
			} else {
				o["port"] = c.ports[0]
			}
			cn.c, err = dial(ctx, c.dialer, o)
			if nil != err {
				lastErr = err
				continue
			}

			err = cn.ssl(o)
			if nil != err {
				if nil != cn.c {
					cn.c.Close()
				}
				lastErr = err
				continue
			}
			// cn.startup报错时确保不会泄露cn.c
			defer func() {
				if panicking {
					if nil != cn.c {
						cn.c.Close()
					}
				}
			}()

			cn.buf = bufio.NewReader(cn.c)
			cn.startup(o)

			//判断是否需要找主
			if cn.requirePrimary {
				isPrimary, err := getPrimary(cn)
				if err != nil {
					if nil != cn.c {
						cn.c.Close()
					}
					lastErr = fmt.Errorf("getPrimary failed: %v", err)
					continue
				}
				if !isPrimary {
					//非主节点
					if nil != cn.c {
						cn.c.Close()
					}
					lastErr = fmt.Errorf("not a primary node")
					continue
				}
			}
			//已找到符合要求的节点，退出循环
			err = nil
			lastErr = nil
			break
		}
		if nil != err {
			i++
			if i > cn.retry {
				break
			} else {
				time.Sleep(time.Duration(cn.delay) * time.Second)
				continue
			}
		}
		break
	}
	if nil != err || nil != lastErr {
		err = fmt.Errorf("all connection attempts failed: %v", lastErr)
		return
	}
	cn.opts["host"] = o["host"]
	cn.opts["port"] = o["port"]

	// 重置deadline
	if timeout, ok := o["connect_timeout"]; "0" != timeout && ok {
		err = cn.c.SetDeadline(time.Time{})
	}

	// 获取数据库模式
	err = getDatabaseMode(cn)
	//忽略了因为数据库模式查询导致的出错，避免连接pg生态没有数据库模式导致无法连接
	if err != nil {
		return
	}
	panicking = false

	return
}

func getPrimary(cn *conn) (isPrimary bool, err error) {
	var dest interface{}
	var transaction_read_only string
	defer cn.errRecover(&err)

	rs, err := cn.simpleQuery("show transaction_read_only;")
	if nil != err {
		if nil != cn.c {
			cn.c.Close()
		}
		return false, err
	}
	for {
		t := cn.recv1Buf(&rs.rb)
		if t == 'Z' {
			break
		}
		switch t {
		case 'E':
			err = parseError(&rs.rb)
		case 'C', 'I', 'Z':
			if 'C' == t {
				rs.result, rs.tag = cn.parseComplete(rs.rb.string(), 0)
			}
			break
		case 'T':
			rs.rowsHeader = parsePortalRowDescribe(&rs.rb)
			continue
		case 'D':
			n := rs.rb.int16()
			if nil != err {
				errorf("unexpected DataRow after error %s", err)
			} else if n != 1 {
				errorf("unexpected Rows of transaction_read_only")
			}
			l := rs.rb.int32()
			if -1 == l || 0 == l {
				errorf("unexpected result length of transaction_read_only")
			}
			dest = decode(&cn.parameterStatus, rs.rb.next(l), rs.colTyps[0].OID, rs.colFmts[0], *cn)
			switch dest := dest.(type) {
			case []uint8:
				transaction_read_only = strings.ToLower(string(dest))
			case string:
				transaction_read_only = strings.ToLower(dest)
			default:
				errorf("unexpected type for transaction_read_only:%v", reflect.TypeOf(dest))
			}
			continue
		default:
			errorf("unexpected message after execute: %q", t)
		}
	}
	if transaction_read_only == "off" {
		isPrimary = true
	} else if transaction_read_only == "on" {
		isPrimary = false
	} else {
		errorf("unexpected value for transaction_read_only: %v", transaction_read_only)
	}
	return isPrimary, nil
}

func getDatabaseMode(cn *conn) (err error) {
	var dest interface{}
	defer cn.errRecover(&err)

	rs, err := cn.simpleQuery("show database_mode;")
	if nil != err {
		//忽略查询数据库模式的出错，该错误可能是因为连接pg生态没有数据库模式
		setDatabaseModeOid(cn)
		return nil
	}
	for {
		t := cn.recv1Buf(&rs.rb)
		if t == 'Z' {
			break
		}
		switch t {
		case 'E':
			//上述已忽略错误，此处不会执行
			err = parseError(&rs.rb)
		case 'C', 'I', 'Z':
			if 'C' == t {
				rs.result, rs.tag = cn.parseComplete(rs.rb.string(), 0)
			}
			break
		case 'T':
			rs.rowsHeader = parsePortalRowDescribe(&rs.rb)
			continue
		case 'D':
			n := rs.rb.int16()
			if nil != err {
				errorf("unexpected DataRow after error %s", err)
			} else if n != 1 {
				errorf("unexpected Rows of database_mode")
			}
			l := rs.rb.int32()
			if -1 == l || 0 == l {
				errorf("unexpected result length of database_mode")
			}
			dest = decode(&cn.parameterStatus, rs.rb.next(l), rs.colTyps[0].OID, rs.colFmts[0], *cn)
			switch dest := dest.(type) {
			case []uint8:
				cn.databaseMode = string(dest)
			case string:
				cn.databaseMode = dest
			default:
				errorf("unexpected type for database_mode:%v", reflect.TypeOf(dest))
			}
			continue
		default:
			errorf("unexpected message after execute: %q", t)
		}
	}
	cn.databaseMode = strings.ToLower(cn.databaseMode)
	setDatabaseModeOid(cn)
	return nil
}

func setDatabaseModeOid(cn *conn) {
	if cn.databaseMode == "sqlserver" {
		cn.allOid = sqlserverOid.SqlserverOid
		cn.TypeName = sqlserverOid.TypeName
		// oid.T_binary, oid.T__binary = sqlserverOid.T_binary, sqlserverOid.T__sqlserver_binary
		// oid.T_varbinary, oid.T__varbinary = sqlserverOid.T_varbinary, sqlserverOid.T__sqlserver_varbinary
		// oid.T_tinyint, oid.T__tinyint = sqlserverOid.T_tinyint, sqlserverOid.T__tinyint
		// oid.T_smallint, oid.T__smallint = sqlserverOid.T_smallint, sqlserverOid.T__smallint
		// oid.T_int, oid.T__int = sqlserverOid.T_int, sqlserverOid.T__int
		// oid.T_bigint, oid.T__bigint = sqlserverOid.T_bigint, sqlserverOid.T__bigint
		// oid.T_numeric, oid.T__numeric = sqlserverOid.T_numeric, sqlserverOid.T__numeric
		// oid.T_money, oid.T__money = sqlserverOid.T_money, sqlserverOid.T__money
		// oid.T_real, oid.T__real = sqlserverOid.T_real, sqlserverOid.T__real
		// oid.T_float, oid.T__float = sqlserverOid.T_float, sqlserverOid.T__float
		// oid.T_date, oid.T__date = sqlserverOid.T_date, sqlserverOid.T__sqlserverdate
		// oid.T_time, oid.T__time = sqlserverOid.T_time, sqlserverOid.T__time
		// oid.T_datetime, oid.T__datetime = sqlserverOid.T_datetime, sqlserverOid.T__datetime
		// oid.T_bit, oid.T__bit = sqlserverOid.T_bit, sqlserverOid.T__sqlserverbit
		// oid.TypeName[oid.T_binary], oid.TypeName[oid.T__binary] = "BINARY", "_BINARY"
		// oid.TypeName[oid.T_varbinary], oid.TypeName[oid.T__varbinary] = "VARBINARY", "_VARBINARY"
		// oid.TypeName[oid.T_tinyint], oid.TypeName[oid.T__tinyint] = "TINYINT", "_TINYINT"
		// oid.TypeName[oid.T_smallint], oid.TypeName[oid.T__smallint] = "SMALLINT", "_SMALLINT"
		// oid.TypeName[oid.T_int], oid.TypeName[oid.T__int] = "INT", "_INT"
		// oid.TypeName[oid.T_bigint], oid.TypeName[oid.T__bigint] = "BIGINT", "_BIGINT"
		// oid.TypeName[oid.T_numeric], oid.TypeName[oid.T__numeric] = "NUMERIC", "_NUMERIC"
		// oid.TypeName[oid.T_money], oid.TypeName[oid.T__money] = "MONEY", "_MONEY"
		// oid.TypeName[oid.T_real], oid.TypeName[oid.T__real] = "REAL", "_REAL"
		// oid.TypeName[oid.T_float], oid.TypeName[oid.T__float] = "FLOAT", "_FLOAT"
		// oid.TypeName[oid.T_date], oid.TypeName[oid.T__date] = "DATE", "_DATE"
		// oid.TypeName[oid.T_time], oid.TypeName[oid.T__time] = "TIME", "_TIME"
		// oid.TypeName[oid.T_datetime], oid.TypeName[oid.T__datetime] = "DATETIME", "_DATETIME"
		// oid.TypeName[oid.T_bit], oid.TypeName[oid.T__bit] = "BIT", "_BIT"
	} else if cn.databaseMode == "mysql" {
		cn.allOid = mysqlOid.MysqlOid
		cn.TypeName = mysqlOid.TypeName
		// oid.T_bit, oid.T__bit = mysqlOid.T_bit, mysqlOid.T__bit
		// oid.T_date, oid.T__date = mysqlOid.T_date, mysqlOid.T__date
		// oid.T_time, oid.T__time = mysqlOid.T_time, mysqlOid.T__time
		// oid.T_datetime, oid.T__datetime = mysqlOid.T_datetime, mysqlOid.T__datetime
		// oid.T_timestamp, oid.T__timestamp = mysqlOid.T_timestamp, mysqlOid.T__timestamp
		// oid.T_year, oid.T__year = mysqlOid.T_year, mysqlOid.T__year
		// oid.T_longtext, oid.T__longtext = mysqlOid.T_longtext, mysqlOid.T__longtext
		// oid.T_mediumtext, oid.T__mediumtext = mysqlOid.T_mediumtext, mysqlOid.T__mediumtext
		// oid.T_tinytext, oid.T__tinytext = mysqlOid.T_tinytext, mysqlOid.T__tinytext
		// oid.T_longblob, oid.T__longblob = mysqlOid.T_longblob, mysqlOid.T__longblob
		// oid.T_mediumblob, oid.T__mediumblob = mysqlOid.T_mediumblob, mysqlOid.T__mediumblob
		// oid.T_tinyblob, oid.T__tinyblob = mysqlOid.T_tinyblob, mysqlOid.T__tinyblob
		// oid.TypeName[oid.T_bit], oid.TypeName[oid.T__bit] = "BIT", "_BIT"
		// oid.TypeName[oid.T_date], oid.TypeName[oid.T__date] = "DATE", "_DATE"
		// oid.TypeName[oid.T_time], oid.TypeName[oid.T__time] = "TIME", "_TIME"
		// oid.TypeName[oid.T_datetime], oid.TypeName[oid.T__datetime] = "DATETIME", "_DATETIME"
		// oid.TypeName[oid.T_timestamp], oid.TypeName[oid.T__timestamp] = "TIMESTAMP", "_TIMESTAMP"
		// oid.TypeName[oid.T_year], oid.TypeName[oid.T__year] = "YEAR", "_YEAR"
		// oid.TypeName[oid.T_longtext], oid.TypeName[oid.T__longtext] = "LONGTEXT", "_LONGTEXT"
		// oid.TypeName[oid.T_mediumtext], oid.TypeName[oid.T__mediumtext] = "MEDIUMTEXT", "_MEDIUMTEXT"
		// oid.TypeName[oid.T_tinytext], oid.TypeName[oid.T__tinytext] = "TINYTEXT", "_TINYTEXT"
		// oid.TypeName[oid.T_longblob], oid.TypeName[oid.T__longblob] = "LONGBLOB", "_LONGBLOB"
		// oid.TypeName[oid.T_mediumblob], oid.TypeName[oid.T__mediumblob] = "MEDIUMBLOB", "_MEDIUMBLOB"
		// oid.TypeName[oid.T_tinyblob], oid.TypeName[oid.T__tinyblob] = "TINYBLOB", "_TINYBLOB"
	} else if cn.databaseMode == "oracle" || cn.databaseMode == "pg" || cn.databaseMode == "" {
		cn.allOid = oracleOid.OracleOid
		cn.TypeName = oracleOid.TypeName
		// oid.T_date, oid.T__date = oracleOid.T_date, oracleOid.T__date
		// oid.TypeName[oid.T_date], oid.TypeName[oid.T__date] = "DATE", "_DATE"
	}
}

func dial(ctx context.Context, d Dialer, o values) (nc net.Conn, err error) {
	network, address := network(o)
	// SSL在UNIX domain套接字上既不支持也不需要
	if "unix" == network {
		o["sslmode"] = "disable"
	}
	// 0或者不设置表示无限期等待
	if timeout, ok := o["connect_timeout"]; "0" != timeout && ok {
		seconds, err := strconv.ParseInt(timeout, 10, 0)
		if nil != err {
			nc = nil
			err = fmt.Errorf("invalid value for parameter connect_timeout: %s", err)
			return nc, err
		}
		duration := time.Duration(seconds) * time.Second

		// connect_timeout应该被应用于整个连接建立的过程中
		// 所以我们既为TCP连接建立过程设置超时时间也设置握手初始化完成的最晚时间
		// 当startup()完成时最晚时间也将被重置
		deadline := time.Now().Add(duration)
		var conn net.Conn
		if dctx, ok := d.(DialerContext); ok {
			ctx, cancel := context.WithTimeout(ctx, duration)
			defer cancel()
			conn, err = dctx.DialContext(ctx, network, address)
		} else {
			conn, err = d.DialTimeout(network, address, duration)
		}
		if nil != err {
			nc = nil
			return nc, err
		}
		err = conn.SetDeadline(deadline)
		nc = conn
		return conn, err
	}
	if dctx, ok := d.(DialerContext); ok {
		nc, err = dctx.DialContext(ctx, network, address)
		return
	}
	nc, err = d.Dial(network, address)
	return
}

func network(o values) (protocalString string, pathString string) {
	host := o["host"]

	if strings.HasPrefix(host, "/") {
		sockPath := path.Join(host, ".s.KINGBASE."+o["port"])
		protocalString = "unix"
		pathString = sockPath
		return
	}
	protocalString = "tcp"
	pathString = net.JoinHostPort(host, o["port"])
	return
}

// newScanner返回一个通过选项字符串s初始化的新的scanner
func newScanner(s string) (scan *scanner) {
	scan = &scanner{[]rune(s), 0}
	return
}

// Next返回下一个rune字符
// 当文本到达末尾时则返回0
func (s *scanner) Next() (rString rune, state bool) {
	if len(s.s) <= s.i {
		rString = 0
		state = false
		return
	}
	r := s.s[s.i]
	s.i++
	rString = r
	state = true
	return
}

// SkipSpaces返回下一个非空格的rune字符
// 当文本到达末尾时则返回0
func (s *scanner) SkipSpaces() (rString rune, state bool) {
	r, ok := s.Next()
	for unicode.IsSpace(r) && ok {
		r, ok = s.Next()
	}
	rString = r
	state = ok
	return
}

// parseOpts解析name中的选项并将其添加到values中
func parseOpts(name string, o values) (hosts []string, ports []string, err error) {
	s := newScanner(name)

	for {
		var (
			keyRunes, valRunes []rune
			r                  rune
			ok                 bool
		)

		//跳过连接串的前导空格
		if r, ok = s.SkipSpaces(); !ok {
			break
		}

		// 扫描
		for !unicode.IsSpace(r) && '=' != r {
			keyRunes = append(keyRunes, r)
			if r, ok = s.Next(); !ok {
				break
			}
		}

		// 跳过=前的所有空格
		if '=' != r {
			r, ok = s.SkipSpaces()
		}

		// 当前字符应该为=
		if ('=' != r && ',' != r) || !ok {
			err = fmt.Errorf(`missing "=" after %q in connection info string`, string(keyRunes))
			return
		}

		// 跳过=后的所有字符
		if r, ok = s.SkipSpaces(); !ok {
			// 如果到达字符串末尾，那最后一个值应为空字符串
			o[string(keyRunes)] = ""
			break
		}

		if '\'' != r {
			for !unicode.IsSpace(r) {
				if '\\' == r {
					if r, ok = s.Next(); !ok {
						err = fmt.Errorf(`missing character after backslash`)
						return
					}
				}
				valRunes = append(valRunes, r)

				if r, ok = s.Next(); !ok {
					break
				}
			}
		} else {
		quote:
			for {
				if r, ok = s.Next(); !ok {
					err = fmt.Errorf(`unterminated quoted string literal in connection string`)
					return
				}
				switch r {
				case '\'':
					break quote
				case '\\':
					r, _ = s.Next()
					fallthrough
				default:
					valRunes = append(valRunes, r)
				}
			}
		}
		o[string(keyRunes)] = string(valRunes)
	}
	//解析多主机地址和端口
	hosts = strings.Split(o["host"], ",")
	for i, h := range hosts {
		hosts[i] = strings.TrimSpace(h)
	}
	ports = strings.Split(o["port"], ",")
	for i, p := range ports {
		ports[i] = strings.TrimSpace(p)
	}
	err = nil
	return
}

func (cn *conn) isInTransaction() (state bool) {
	state = cn.txnStatus == txnStatusIdleInTransaction || cn.txnStatus == txnStatusInFailedTransaction
	return
}

func (cn *conn) checkIsInTransaction(intxn bool) {
	if intxn != cn.isInTransaction() {
		cn.bad = true
		errorf("unexpected transaction status %v", cn.txnStatus)
	}
	return
}

func (cn *conn) Begin() (dt driver.Tx, err error) {
	dt, err = cn.begin("")
	return
}

func (cn *conn) begin(mode string) (dt driver.Tx, err error) {
	if cn.bad {
		dt = nil
		err = driver.ErrBadConn
		return
	}
	defer cn.errRecover(&err)

	cn.checkIsInTransaction(false)
	_, commandTag, err := cn.simpleExec("BEGIN" + mode)
	if nil != err {
		dt = nil
		return
	}
	if "BEGIN" != commandTag {
		cn.bad = true
		dt = nil
		err = fmt.Errorf("unexpected command tag %s", commandTag)
		return
	}
	if txnStatusIdleInTransaction != cn.txnStatus {
		cn.bad = true
		dt = nil
		err = fmt.Errorf("unexpected transaction status %v", cn.txnStatus)
		return
	}
	dt = cn
	err = nil
	return
}

func (cn *conn) closeTxn() {
	if finish := cn.txnFinish; nil != finish {
		finish()
	}
	return
}

func (cn *conn) Commit() (err error) {
	defer cn.closeTxn()
	if cn.bad {
		err = driver.ErrBadConn
		return
	}
	defer cn.errRecover(&err)

	cn.checkIsInTransaction(true)
	// 我们不想让客户端在尝试提交一个失败的事务时认为没有错误发生
	// 但无论返回什么，database/sql都会将此连接释放到空闲连接池，所以必须在此终止当前事务
	// 注意，如果在一个失败的事务中发送COMMIT命令也会进行相同的处理
	if txnStatusInFailedTransaction == cn.txnStatus {
		if err = cn.rollback(); nil != err {
			return
		}
		err = ErrInFailedTransaction
		return
	}

	_, commandTag, err := cn.simpleExec("COMMIT")
	if nil != err {
		if cn.isInTransaction() {
			cn.bad = true
		}
		return
	}
	if "COMMIT" != commandTag {
		cn.bad = true
		err = fmt.Errorf("unexpected command tag %s", commandTag)
		return
	}
	cn.checkIsInTransaction(false)
	err = nil
	return
}

func (cn *conn) Rollback() (err error) {
	defer cn.closeTxn()
	if cn.bad {
		err = driver.ErrBadConn
		return
	}
	defer cn.errRecover(&err)
	err = cn.rollback()
	return
}

func (cn *conn) rollback() (err error) {
	cn.checkIsInTransaction(true)
	_, commandTag, err := cn.simpleExec("ROLLBACK")
	if nil != err {
		if cn.isInTransaction() {
			cn.bad = true
		}
		return
	}
	if "ROLLBACK" != commandTag {
		err = fmt.Errorf("unexpected command tag %s", commandTag)
		return
	}
	cn.checkIsInTransaction(false)
	err = nil
	return
}

func (cn *conn) gname() (result string) {
	cn.namei++
	result = strconv.FormatInt(int64(cn.namei), 10)
	return
}

func (cn *conn) addReturning(q string) string {
	//去除首尾空白字符
	trimmedSql := strings.TrimSpace(q)
	//转为小写以忽略大小写
	lowerSql := strings.ToLower(trimmedSql)
	if strings.HasPrefix(lowerSql, "insert") {
		//数据库不支持insert ignore+returning的用法
		cn.getLastInsertId.isInsert = true
		if strings.HasSuffix(lowerSql, ";") {
			trimmedSql = strings.TrimSuffix(trimmedSql, ";")
		}
		return trimmedSql + " RETURNING *"
	}
	//非insert语句，返回原sql
	cn.getLastInsertId.isInsert = false
	return q
}

func getID(cn *conn) (id int64) {
	var dest interface{}
	rs, err := cn.simpleQuery("select last_insert_id();")
	if nil != err {
		errorf("unexpected err for last_insert_id():%s", err)
	}
	for {
		t := cn.recv1Buf(&rs.rb)
		if t == 'Z' {
			break
		}
		switch t {
		case 'E':
			err = parseError(&rs.rb)
		case 'C', 'I', 'Z':
			if 'C' == t {
				rs.result, rs.tag = cn.parseComplete(rs.rb.string(), 0)
			}
			break
		case 'T':
			rs.rowsHeader = parsePortalRowDescribe(&rs.rb)
			continue
		case 'D':
			n := rs.rb.int16()
			if nil != err {
				cn.bad = true
				errorf("unexpected DataRow after error %s", err)
			} else if n != 1 {
				errorf("unexpected returning num of columns:%d", n)
			}
			l := rs.rb.int32()
			if -1 == l {
				dest = 0
				continue
			}
			dest = decode(&cn.parameterStatus, rs.rb.next(l), rs.colTyps[0].OID, rs.colFmts[0], *cn)
			//数据库返回的last_insert_id将会被解析为uint64
			id = int64(dest.(uint64))
			continue
		default:
			errorf("unexpected message after execute: %q", t)
		}
	}
	return id
}

func (cn *conn) simpleExec(q string) (res driver.Result, commandTag string, err error) {
	//判断是否需要拼接RETURNING *以获取自增列id
	var row *rows
	var dest interface{}
	var lastID int64 = 0
	var alreadyGet bool = false
	//var tag string
	if cn.getLastInsertId.enable {
		q = cn.addReturning(q)
	}

	b := cn.writeBuf('Q')
	b.string(q)
	cn.send(b)

	for {
		t, r := cn.recv1()
		switch t {
		case 'C':
			res, commandTag = cn.parseComplete(r.string(), lastID)
		case 'Z':
			cn.processReadyForQuery(r)
			if nil == res && nil == err {
				err = errUnexpectedReady
			}
			return
		case 'T': //可能的自增列id的T
			if cn.getLastInsertId.enable && cn.getLastInsertId.isInsert {
				row = &rows{cn: cn}
				row.rowsHeader = parsePortalRowDescribe(r)
			}
		case 'D': //可能的自增列id的D
			if cn.getLastInsertId.enable && cn.getLastInsertId.isInsert {
				if nil == row {
					cn.bad = true
					errorf("unexpected DataRow in simple query execution")
				}
				//获取结果集的第一列结果，即为自增列id
				if n := r.int16(); n < 1 {
					cn.bad = true
					errorf("unexpected returning num of columns:%d", n)
				}
				if nil != err {
					cn.bad = true
					errorf("unexpected DataRow after error %s", err)
				}
				//第一列必须为oid.T_int4
				switch row.colTyps[0].OID {
				case cn.allOid.T_int4:
					//获取第一列自增列id
					l := r.int32()
					if -1 == l {
						dest = 0
						continue
					}
					dest = decode(&cn.parameterStatus, r.next(l), row.colTyps[0].OID, row.colFmts[0], *cn)
					if !alreadyGet {
						lastID = dest.(int64)
						alreadyGet = true
					}
				default:
					//errorf("the first column(oid:%d) is not auto_increment id", row.colTyps[0].OID)
				}
				continue
			}
		case 'I':
			res = emptyRows
		case 'E':
			err = parseError(r)
		default:
			cn.bad = true
			errorf("unknown response for simple query: %q", t)
		}
	}
}

func (cn *conn) simpleQuery(q string) (res *rows, err error) {
	defer cn.errRecover(&err)

	b := cn.writeBuf('Q')
	b.string(q)
	cn.send(b)

	for {
		t, r := cn.recv1()
		switch t {
		case 'Z':
			cn.processReadyForQuery(r)
			// 完成
			return
		case 'C', 'I':
			//允许Query和Exec进行不会返回任何结果的查询
			// 但为了防止连接泄露，仍需要向database/sql提供一个对象以供用户可以关闭
			if nil != err {
				cn.bad = true
				errorf("unexpected message %q in simple query execution", t)
			}
			if nil == res {
				res = &rows{cn: cn}
			}
			if 'C' == t && nil == res.colNames {
				res.result, res.tag = cn.parseComplete(r.string(), 0)
			}
			res.done = true
		case 'T':
			// 如果收到了之前的命令完成，res可能为非空，但也可直接覆盖
			res = &rows{cn: cn}
			res.rowsHeader = parsePortalRowDescribe(r)

			// 为处理在Go1.2或更早版本中的bug，此处需要等待直到第一个DataRow接收完成
		case 'D':
			if nil == res {
				cn.bad = true
				errorf("unexpected DataRow in simple query execution")
			}
			// 查询没有失败，继续下一个
			cn.saveMessage(t, r)
			return
		case 'E':
			res = nil
			err = parseError(r)
		default:
			cn.bad = true
			errorf("unknown response for simple query: %q", t)
		}
	}
}

func (noRows) LastInsertId() (result int64, err error) {
	result = 0
	err = errNoLastInsertID
	return
}

func (noRows) RowsAffected() (result int64, err error) {
	result = 0
	err = errNoRowsAffected
	return
}

// 判断预备语句的列格式，输入为类型oid的数组，每个元素对应一个结果列
func decideColumnFormats(cn *conn, colTyps []fieldDesc, forceText bool) (colFmts []format, colFmtData []byte) {
	if 0 == len(colTyps) {
		colFmts = nil
		colFmtData = colFmtDataAllText
		return nil, colFmtDataAllText
	}

	colFmts = make([]format, len(colTyps))
	if true == forceText {
		colFmtData = colFmtDataAllText
		return
	}

	allBinary, allText := true, true
	for i, t := range colTyps {
		switch t.OID {
		// 当从预备语句接收到以下类型时需要使用二进制格式
		// 在encode.go的binaryDecode函数中有以下类型的格式转换
		case cn.allOid.T_bytea:
			colFmts[i] = formatBinary
			allText = false
		case cn.allOid.T_binary:
			colFmts[i] = formatBinary
			allText = false
		case cn.allOid.T_varbinary:
			colFmts[i] = formatBinary
			allText = false
		case cn.allOid.T_int8:
			colFmts[i] = formatBinary
			allText = false
		case cn.allOid.T_bigint:
			colFmts[i] = formatBinary
			allText = false
		case cn.allOid.T_int4:
			colFmts[i] = formatBinary
			allText = false
		case cn.allOid.T_int:
			colFmts[i] = formatBinary
			allText = false
		case cn.allOid.T_int2:
			colFmts[i] = formatBinary
			allText = false
		case cn.allOid.T_smallint:
			colFmts[i] = formatBinary
			allText = false
		case cn.allOid.T_tinyint:
			colFmts[i] = formatBinary
			allText = false
		case cn.allOid.T_uuid:
			colFmts[i] = formatBinary
			allText = false
		default:
			allBinary = false
		}
	}

	if true == allBinary {
		colFmtData = colFmtDataAllBinary
		return
	} else if true == allText {
		colFmtData = colFmtDataAllText
		return
	} else {
		colFmtData = make([]byte, 2+len(colFmts)*2)
		binary.BigEndian.PutUint16(colFmtData, uint16(len(colFmts)))
		for i, v := range colFmts {
			binary.BigEndian.PutUint16(colFmtData[2+i*2:], uint16(v))
		}
		return
	}
}

func GetPlatformBit() uint {
	byteNum := unsafe.Sizeof(uint(0))
	if byteNum == 8 {
		return 64
	}
	return 32
}

func (cn *conn) sendPmessage(q, stmtName string, v []driver.Value) (hasRet bool) {
	n := len(v)
	sBind := make([]bindStruct, n)

	for i, t := range v {
		sBind[i].out, sBind[i].isOut = t.(sql.Out)
		sBind[i].isBoth = sBind[i].out.In
		sBind[i].ret, sBind[i].isRet = t.(*ReturnStatus)
		if sBind[i].isRet {
			hasRet = true
		}

		if sBind[i].isOut {
			switch sBind[i].out.Dest.(type) {
			case *int:
				//sBind[i].typ = cn.allOid.T_int
				if GetPlatformBit() == 64 {
					//sBind[i].typ = cn.allOid.T_int8
				} else {
					//sBind[i].typ = cn.allOid.T_int4
				}
			case *int64, *sql.NullInt64:
				//sBind[i].typ = cn.allOid.T_bigint
			case *int32:
				//sBind[i].typ = cn.allOid.T_int
			case *int16:
				//sBind[i].typ = cn.allOid.T_smallint
			case *int8:
				//sBind[i].typ = cn.allOid.T_tinyint
			case *uint:
				if GetPlatformBit() == 64 {
					//sBind[i].typ = cn.allOid.T_uint8
				} else {
					//sBind[i].typ = cn.allOid.T_uint4
				}
			case *uint64:
				//sBind[i].typ = cn.allOid.T_uint8
			case *uint32:
				//sBind[i].typ = cn.allOid.T_uint4
			case *byte:
				if cn.databaseMode == "sqlserver" {
					sBind[i].typ = cn.allOid.T_tinyint
				}
			case *[]byte:
				if cn.databaseMode == "sqlserver" {
					sBind[i].typ = cn.allOid.T_binary
				} else {
					if len(*((sBind[i].out.Dest).(*[]byte))) <= 32767 {
						//sBind[i].typ = cn.allOid.T_bytea
					} else {
						//sBind[i].typ = cn.allOid.T_blob
					}
				}
			case *float64, *sql.NullFloat64:
				//sBind[i].typ = cn.allOid.T_float8
			case *float32:
				//sBind[i].typ = cn.allOid.T_float4
			case *decimal.Decimal:
				//numeric/decimal
				//sBind[i].typ = cn.allOid.T_numeric
			case *bool:
				sBind[i].typ = cn.allOid.T_bool
			case *sql.NullBool:
				sBind[i].typ = cn.allOid.T_bit
			case *string, *sql.NullString:
				//sBind[i].typ = cn.allOid.T_bpcharbyte
				//if len(*((sBind[i].out.Dest).(*string))) > 32767 {
				//sBind[i].typ = cn.allOid.T_clob
				//}
			case *VarChar, *VarCharMax:
				sBind[i].typ = cn.allOid.T_varcharbyte
			case *NVarCharMax:
				sBind[i].typ = cn.allOid.T_nvarchar
			case *NChar:
				sBind[i].typ = cn.allOid.T_nchar
			case *time.Time:
				//time timestamp date
			case *DateTime1:
				sBind[i].typ = cn.allOid.T_datetime
			case *civil.Date:
				sBind[i].typ = cn.allOid.T_date
			case *civil.Time:
				sBind[i].typ = cn.allOid.T_time
			case *CursorString:
				sBind[i].typ = cn.allOid.T_refcursor
			}
		} else if (sBind[i].isBoth || !sBind[i].isOut) && !sBind[i].isRet {
			switch t.(type) {
			case int:
				//sBind[i].typ = cn.allOid.T_int
				if GetPlatformBit() == 64 {
					//sBind[i].typ = cn.allOid.T_int8
				} else {
					//sBind[i].typ = cn.allOid.T_int4
				}
			case int64, sql.NullInt64:
				//sBind[i].typ = cn.allOid.T_bigint
			case int32:
				//sBind[i].typ = cn.allOid.T_int
			case int16:
				//sBind[i].typ = cn.allOid.T_smallint
			case int8:
				//sBind[i].typ = cn.allOid.T_tinyint
			case uint64:
				//sBind[i].typ = cn.allOid.T_uint8
				//sBind[i].typ = cn.allOid.T_numeric
			case uint32:
				//sBind[i].typ = cn.allOid.T_uint4
				//sBind[i].typ = cn.allOid.T_numeric
			case byte:
				if cn.databaseMode == "sqlserver" {
					sBind[i].typ = cn.allOid.T_tinyint
				}
			case []byte:
				if cn.databaseMode == "sqlserver" {
					sBind[i].typ = cn.allOid.T_binary
				} else {
					if len(t.([]byte)) <= 32767 {
						//sBind[i].typ = cn.allOid.T_bytea
					} else {
						//sBind[i].typ = cn.allOid.T_blob
					}
				}
			case float64, sql.NullFloat64:
				//sBind[i].typ = cn.allOid.T_float8
			case float32:
				//sBind[i].typ = cn.allOid.T_float4
			case decimal.Decimal:
				//numeric/decimal
				//sBind[i].typ = cn.allOid.T_numeric
			case bool:
				sBind[i].typ = cn.allOid.T_bool
			case *sql.NullBool:
				sBind[i].typ = cn.allOid.T_bit
			case string, sql.NullString:
				//sBind[i].typ = cn.allOid.T_bpcharbyte
				//if len(t.(string)) > 32767 {
				//sBind[i].typ = cn.allOid.T_clob
				//}
				//char varchar text
			case VarChar, VarCharMax:
				sBind[i].typ = cn.allOid.T_varcharbyte
			case NVarCharMax:
				sBind[i].typ = cn.allOid.T_nvarchar
			case NChar:
				sBind[i].typ = cn.allOid.T_nchar
			case time.Time:
				//time timestamp date
			case DateTime1:
				sBind[i].typ = cn.allOid.T_datetime
			case *civil.Date:
				sBind[i].typ = cn.allOid.T_date
			case *civil.Time:
				sBind[i].typ = cn.allOid.T_time
			case CursorString:
				sBind[i].typ = cn.allOid.T_refcursor
			}
		} else if sBind[i].isRet {
			continue
		}
	}

	for i, _ := range sBind {
		if sBind[i].isOut {
			if sBind[i].isBoth {
				//INOUT
				sBind[i].typ |= 3 << 29
			} else {
				//OUT
				sBind[i].typ |= 1 << 30
			}
		} else if !sBind[i].isOut { //&& !sBind[i].isRet {
			//IN
			sBind[i].typ |= 1 << 29
		} else if sBind[i].isRet {
			continue
		}
	}

	if cn.getLastInsertId.enable {
		q = cn.addReturning(q)
	}

	b := cn.writeBuf('P')
	b.string(stmtName)
	b.string(q)
	if hasRet {
		b.int16(n - 1)
	} else {
		b.int16(n)
	}
	for i := 0; i < n; i++ {
		if sBind[i].isRet {
			continue
		} else {
			b.int32((int)(sBind[i].typ))
		}
	}

	b.next('D')
	b.byte('S')
	b.string(stmtName)

	b.next('S')
	cn.send(b)
	return
}

func (cn *conn) readPDmessageResponse(st *stmt) (err error) {
	err = cn.readParseResponse()
	if err != nil {
		return err
	}

	st.paramTyps, st.colNames, st.colTyps, err = cn.readStatementDescribeResponse()
	if err != nil {
		return err
	}
	st.colFmts, st.colFmtData = decideColumnFormats(cn, st.colTyps, cn.disablePreparedBinaryResult)

	//此处获取到的T报文，可能是描述有out参数的存储过程，也可能是描述DQL
	//先保留此T报文以及绑定的参数，后续解析D报文时根据参数是否为out类型进行区分
	st.TMessage.colNames, st.TMessage.colTyps, st.TMessage.colFmts = st.colNames, st.colTyps, st.colFmts

	cn.readReadyForQuery()
	return nil
}

func (cn *conn) prepareTo(q, stmtName string, v []driver.Value) (st *stmt, err error) {
	st = &stmt{cn: cn, name: stmtName, queryName: q, pSended: true}
	st.hasRet = cn.sendPmessage(q, stmtName, v)
	err = cn.readPDmessageResponse(st)
	if err != nil {
		return nil, err
	}
	return st, nil
}

func (cn *conn) Prepare(q string) (st driver.Stmt, err error) {
	if cn.bad {
		st = nil
		err = driver.ErrBadConn
		return
	}
	defer cn.errRecover(&err)

	if 4 <= len(q) && strings.EqualFold(q[:4], "COPY") {
		st, err = cn.prepareCopyIn(q)
		if nil == err {
			cn.inCopy = true
		}
		return
	}
	st = &stmt{cn: cn, name: cn.gname(), queryName: q, pSended: false}
	err = nil
	return
}

func (cn *conn) Close() (err error) {
	// 准备关闭连接，所以可以跳过此处返回的cn.bad
	defer cn.errRecover(&err)

	// 确保cn.c.Close可以运行，因为错误处理是用panics和cn.errRecover来完成的
	// Close必须通过defer调用
	defer func() {
		cerr := cn.c.Close()
		if err == nil {
			err = cerr
		}
	}()

	return cn.sendSimpleMessage('X')
}

// 实现"Queryer"接口
func (cn *conn) Query(query string, args []driver.Value) (rs driver.Rows, err error) {
	rs, err = cn.query(query, args)
	return
}

func (cn *conn) query(query string, args []driver.Value) (rs *rows, err error) {
	if cn.bad {
		rs = nil
		err = driver.ErrBadConn
		return
	}
	if cn.inCopy {
		rs = nil
		err = errCopyInProgress
		return
	}
	defer cn.errRecover(&err)

	// 判断是否可使用simpleQuery接口
	// simpleQuery要比prepare/exec性能更高
	if 0 == len(args) {
		rs, err = cn.simpleQuery(query)
		return
	}

	st, err := cn.prepareTo(query, "", args)
	if err != nil {
		return nil, err
	}

	err = st.exec(args)
	if err != nil {
		return nil, err
	}
	rs = &rows{
		cn:         cn,
		rowsHeader: st.rowsHeader,
	}
	err = nil
	return
}

// 实现"Execer"接口
func (cn *conn) Exec(query string, args []driver.Value) (res driver.Result, err error) {
	if cn.bad {
		res = nil
		err = driver.ErrBadConn
		return
	}
	defer cn.errRecover(&err)

	// 判断是否可用simpleExec接口
	// simpleExec要比prepare/exec性能更高
	if 0 == len(args) {
		r, _, err := cn.simpleExec(query)
		res = r
		return res, err
	}

	// 使用匿名语句
	st, err := cn.prepareTo(query, "", args)
	if err != nil {
		return nil, err
	}

	r, err := st.Exec(args)
	if err != nil {
		return nil, err
	}

	res = r
	return
}

func (cn *conn) send(wb *writeBuf) {
	_, err := cn.c.Write(wb.wrap())
	if nil != err {
		panic(err)
	}
	return
}

func (cn *conn) sendStartupPacket(m *writeBuf) (err error) {
	_, err = cn.c.Write((m.wrap())[1:])
	return
}

// 发送一个标识为typ的报文到后端
func (cn *conn) sendSimpleMessage(typ byte) (err error) {
	_, err = cn.c.Write([]byte{typ, '\x00', '\x00', '\x00', '\x04'})
	return
}

// saveMessage将报文和缓冲区保存到conn中
// recvMessage将在下一次调用时返回这些值
func (cn *conn) saveMessage(typ byte, buf *readBuf) {
	if 0 != cn.saveMessageType {
		cn.bad = true
		errorf("unexpected saveMessageType %d", cn.saveMessageType)
	}
	cn.saveMessageType, cn.saveMessageBuffer = typ, *buf
	return
}

// recvMessage接收来自后端的所有消息，或当读取消息出错时返回一个错误
func (cn *conn) recvMessage(rb *readBuf) (result byte, err error) {
	// 处理QueryRow相关的bug,见exec
	if 0 != cn.saveMessageType {
		t := cn.saveMessageType
		*rb = cn.saveMessageBuffer
		cn.saveMessageType, cn.saveMessageBuffer = 0, nil
		result = t
		err = nil
		return
	}

	x := cn.scratch[:5]
	_, err = io.ReadFull(cn.buf, x)
	if nil != err {
		result = 0
		return
	}

	// 读取报文的类型和长度
	t, n := x[0], int(binary.BigEndian.Uint32(x[1:]))-4
	var y []byte
	if len(cn.scratch) >= n {
		y = cn.scratch[:n]
	} else {
		y = make([]byte, n)
	}
	_, err = io.ReadFull(cn.buf, y)
	if nil != err {
		result = 0
		return
	}
	*rb = y
	result = t
	err = nil
	return t, nil
}

// recv接收来自后端的报文，当读取报文出错或接收的报文为ErrorResponse时将会报错
// NoticeResponses会被忽略，此接口只应在startup队列中使用
func (cn *conn) recv() (t byte, rb *readBuf) {
	for {
		var err error
		rb = &readBuf{}
		t, err = cn.recvMessage(rb)
		if nil != err {
			panic(err)
		}
		switch t {
		case 'E':
			panic(parseError(rb))
		case 'N':
			if n := cn.noticeHandler; nil != n {
				n(parseError(rb))
			}
		default:
			return
		}
	}
}

// recv1Buf等价于recv1，但recv1Buf使用调用者传入的缓冲区以避免自身进行内存的分配和释放
func (cn *conn) recv1Buf(r *readBuf) (result byte) {
	for {
		t, err := cn.recvMessage(r)
		if nil != err {
			panic(err)
		}

		switch t {
		case 'A':
		case 'N':
			if n := cn.noticeHandler; nil != n {
				n(parseError(r))
			}
		case 'S':
			cn.processParameterStatus(r)
		default:
			result = t
			return
		}
	}
}

// recv1接收来自后端的报文，当读取出错时将会报错
// 除了ErrorResponse外，所有的异步报文将被忽略
func (cn *conn) recv1() (t byte, rb *readBuf) {
	rb = &readBuf{}
	t = cn.recv1Buf(rb)
	return
}

func (cn *conn) ssl(o values) (err error) {
	upgrade, err := ssl(o)
	if nil != err {
		return
	}

	if nil == upgrade {
		err = nil
		return
	}

	w := cn.writeBuf(0)
	w.int32(80877103)
	if err = cn.sendStartupPacket(w); nil != err {
		return
	}

	b := cn.scratch[:1]
	_, err = io.ReadFull(cn.c, b)
	if nil != err {
		return
	}

	if 'S' != b[0] {
		err = ErrSSLNotSupported
		return
	}

	cn.c, err = upgrade(cn.c)
	return
}

// isDriverSetting在key仅为驱动规定的连接选项时返回true
// 其它连接选项不会被发送到启动报文中
func isDriverSetting(key string) (state bool) {
	switch key {
	case "host", "port":
		fallthrough
	case "password":
		fallthrough
	case "sslmode", "sslcert", "sslkey", "sslrootcert":
		fallthrough
	case "fallback_application_name":
		fallthrough
	case "connect_timeout":
		fallthrough
	//case "keepalive_idle": fallthrough//go中idle和interval为相同值
	case "keepalive_interval":
		fallthrough
	case "keepalive_count":
		fallthrough
	case "tcp_user_timeout":
		fallthrough
	case "get_last_insert_id":
		fallthrough
	case "disable_prepared_binary_result":
		fallthrough
	case "binary_parameters":
		fallthrough
	case "target_session_attrs":
		fallthrough
	case "retry":
		fallthrough
	case "delay":
		state = true
	default:
		state = false
	}
	return
}

func (cn *conn) startup(value values) {
	w := cn.writeBuf(0)
	w.int32(196608)
	// 发送连接参数到后端
	for k, v := range value {
		// 跳过非规定的连接参数
		if isDriverSetting(k) {
			continue
		}
		// 协议规定数据库名为database而不是dbname
		if "dbname" == k {
			k = "database"
		}
		w.string(k)
		w.string(v)
	}
	w.string("")
	if err := cn.sendStartupPacket(w); nil != err {
		panic(err)
	}

	for {
		t, r := cn.recv()
		switch t {
		case 'K':
			cn.processBackendKeyData(r)
		case 'S':
			cn.processParameterStatus(r)
		case 'R':
			cn.auth(r, value)
		case 'Z':
			cn.processReadyForQuery(r)
			return
		default:
			errorf("unknown response for startup: %q", t)
		}
	}
}

func (cn *conn) auth(r *readBuf, o values) {
	switch code := r.int32(); code {
	case 0: // OK
	case 3:
		w := cn.writeBuf('p')
		w.string(o["password"])
		cn.send(w)

		t, r := cn.recv()
		if 'R' != t {
			errorf("unexpected password response: %q", t)
		}

		if 0 != r.int32() {
			errorf("unexpected authentication response: %q", t)
		}
	case 5:
		s, w := string(r.next(4)), cn.writeBuf('p')
		w.string("md5" + md5s(md5s(o["password"]+o["user"])+s))
		cn.send(w)

		t, r := cn.recv()
		if 'R' != t {
			errorf("unexpected password response: %q", t)
		}

		if 0 != r.int32() {
			errorf("unexpected authentication response: %q", t)
		}
	case 10:
		sc := scram.NewClient(sha256.New, o["user"], o["password"])
		sc.Step(nil)
		if nil != sc.Err() {
			errorf("SCRAM-SHA-256 error: %s", sc.Err().Error())
		}
		scOut := sc.Out()

		w := cn.writeBuf('p')
		w.string("SCRAM-SHA-256")
		w.int32(len(scOut))
		w.bytes(scOut)
		cn.send(w)

		t, r := cn.recv()
		if 'R' != t {
			errorf("unexpected password response: %q", t)
		}

		if 11 != r.int32() {
			errorf("unexpected authentication response: %q", t)
		}

		nextStep := r.next(len(*r))
		sc.Step(nextStep)
		if nil != sc.Err() {
			errorf("SCRAM-SHA-256 error: %s", sc.Err().Error())
		}

		scOut = sc.Out()
		w = cn.writeBuf('p')
		w.bytes(scOut)
		cn.send(w)

		t, r = cn.recv()
		if 'R' != t {
			errorf("unexpected password response: %q", t)
		}

		if 12 != r.int32() {
			errorf("unexpected authentication response: %q", t)
		}

		nextStep = r.next(len(*r))
		sc.Step(nextStep)
		if nil != sc.Err() {
			errorf("SCRAM-SHA-256 error: %s", sc.Err().Error())
		}
	case 13:
		sm, w := string(r.next(4)), cn.writeBuf('p')
		w.string("sm3" + sm3s(sm3s(o["password"], o["user"]), sm))
		cn.send(w)

		t, r := cn.recv()
		if 'R' != t {
			errorf("unexpected password response: %q", t)
		}

		if 0 != r.int32() {
			errorf("unexpected authentication response: %q", t)
		}

	default:
		errorf("unknown authentication response: %d", code)
	}
}

func (st *stmt) Close() (err error) {
	if st.closed {
		err = nil
		return
	}
	if st.cn.bad {
		err = driver.ErrBadConn
		return
	}
	defer st.cn.errRecover(&err)

	w := st.cn.writeBuf('C')
	w.byte('S')
	w.string(st.name)
	st.cn.send(w)
	st.cn.send(st.cn.writeBuf('S'))

	t, _ := st.cn.recv1()
	if '3' != t {
		st.cn.bad = true
		errorf("unexpected close response: %q", t)
	}
	st.closed = true

	t, r := st.cn.recv1()
	if 'Z' != t {
		st.cn.bad = true
		errorf("expected ready for query, but got: %q", t)
	}
	st.cn.processReadyForQuery(r)
	err = nil
	return
}

func (st *stmt) Query(v []driver.Value) (r driver.Rows, err error) {
	return st.query(v)
}

func (st *stmt) query(v []driver.Value) (r *rows, err error) {
	if st.cn.bad {
		r = nil
		err = driver.ErrBadConn
		return
	}
	defer st.cn.errRecover(&err)
	if !st.pSended {
		st.hasRet = st.cn.sendPmessage(st.queryName, st.name, v)
		st.pSended = true
		err = st.cn.readPDmessageResponse(st)
		if err != nil {
			return nil, err
		}
	}
	err = st.exec(v)
	if err != nil {
		return nil, err
	}
	r = &rows{
		cn:         st.cn,
		rowsHeader: st.rowsHeader,
		multiRes:   st.multiRes,
	}
	err = nil
	return
}

func (st *stmt) Exec(v []driver.Value) (res driver.Result, err error) {
	if st.cn.bad {
		res = nil
		err = driver.ErrBadConn
		return
	}
	defer st.cn.errRecover(&err)

	if !st.pSended {
		st.hasRet = st.cn.sendPmessage(st.queryName, st.name, v)
		st.pSended = true
		err = st.cn.readPDmessageResponse(st)
		if err != nil {
			return nil, err
		}
	}

	err = st.exec(v)
	if err != nil {
		return nil, err
	}
	res, _, err = st.cn.readExecuteResponse("simple query", v, st.colTyps, st.colFmts)
	return
}

func (st *stmt) exec(v []driver.Value) (err error) {
	//保存绑定的参数
	st.bindParams = v

	if 65536 <= len(v) {
		return fmt.Errorf("got %d parameters but Kingbase only supports 65535 parameters", len(v))
	}
	if len(st.paramTyps) != len(v) {
		if st.hasRet && len(st.paramTyps)+1 == len(v) {
			//绑定参数有返回值类型，调用语句中没有该返回值的占位符
			//不绑定参数中的返回值参数
			var val []driver.Value
			for _, t := range v {
				if _, ret := t.(*ReturnStatus); ret {
					continue
				}
				val = append(val, t)
			}
			v = val
		} else {
			return fmt.Errorf("got %d parameters but the statement requires %d", len(v), len(st.paramTyps))
		}
	}

	cn := st.cn
	w := cn.writeBuf('B')
	w.byte(0)
	w.string(st.name)

	if cn.binaryParameters {
		cn.sendBinaryParameters(w, v)
	} else {
		w.int16(len(v))
		for i := 0; i < len(v); i++ {
			if st.paramTyps[i] == cn.allOid.T_blob ||
				st.paramTyps[i] == cn.allOid.T_tinyblob ||
				st.paramTyps[i] == cn.allOid.T_mediumblob ||
				st.paramTyps[i] == cn.allOid.T_longblob {
				w.int16(1)
			} else {
				w.int16(0)
			}
		}
		w.int16(len(v))
		for i, x := range v {
			if nil == x {
				w.int32(-1)
			} else {
				b := encode(&cn.parameterStatus, x, st.paramTyps[i], cn)
				if b != nil {
					w.int32(len(b))
					w.bytes(b)
				} else {
					w.int32(-1)
				}
			}
		}
	}
	w.bytes(st.colFmtData)

	w.next('E')
	w.byte(0)
	w.int32(0)

	w.next('S')
	cn.send(w)

	cn.readBindResponse()
	st.colNames, st.colTyps, st.colFmts, err = cn.postExecuteWorkaround(st)
	return
}

func (st *stmt) NumInput() (count int) {
	s := st.queryName
	count = 0
	inQuote := false

	//兼容SQLSERVER对存储过程名字的调用以及存储过程含返回值的调用
	//存储过程调用时占位符数量和绑定参数的数量可能不一致
	//返回-1到标准接口以忽略该差异
	//在后续执行时会进一步判断参数数量和占位符数量是否匹配
	if st.cn.databaseMode == "sqlserver" {
		count = -1
		return
	}

	for i := 0; i < len(s); i++ {
		if (s[i] == '?' || s[i] == '$' || s[i] == '@') && !inQuote {
			count++
		} else if s[i] == ':' && !inQuote {
			if s[i+1] == ':' {
				i++
			} else {
				count++
			}
		} else if s[i] == '\'' {
			inQuote = !inQuote
		}
	}
	return count
}

func (r *driverResult) LastInsertId() (int64, error) {
	return r.lastInsertId, nil
}

func (r *driverResult) RowsAffected() (int64, error) {
	return r.rowsAffected, nil
}

// parseComplete从CommandComplete报文中解析命令标志
// 并返回影响行数和一个执行的命令字符串，比如"ALTER TABLE"
// 如果命令标志无法解析，parseComplete将报错
func (cn *conn) parseComplete(commandTag string, lastInsertId int64) (result driver.Result, s string) {
	// INSERT在后续处理
	commandsWithAffectedRows := []string{"SELECT ", "UPDATE ", "DELETE ", "FETCH ", "MOVE ", "COPY "}

	var affectedRows *string
	for _, tag := range commandsWithAffectedRows {
		if strings.HasPrefix(commandTag, tag) {
			t := commandTag[len(tag):]
			affectedRows, commandTag = &t, tag[:len(tag)-1]
			break
		}
	}
	// INSERT还包括其命令标志中插入行的oid
	// oid只在一行被插入时返回
	if nil == affectedRows && strings.HasPrefix(commandTag, "INSERT ") {
		parts := strings.Split(commandTag, " ")
		if 3 != len(parts) {
			cn.bad = true
			errorf("unexpected INSERT command tag %s", commandTag)
		}
		affectedRows, commandTag = &parts[len(parts)-1], "INSERT"
	}
	// 理应没有被影响的行，直接返回
	if nil == affectedRows {
		return driver.RowsAffected(0), commandTag
	}
	n, err := strconv.ParseInt(*affectedRows, 10, 64)
	if nil != err {
		cn.bad = true
		errorf("could not parse commandTag: %s", err)
	}
	return &driverResult{
		rowsAffected: n,
		lastInsertId: lastInsertId,
	}, commandTag
}

func (rs *rows) Close() (err error) {
	if finish := rs.finish; nil != finish {
		defer finish()
	}
	// 将会检查cn.bad，此处不需要处理
	for {
		err = rs.Next(nil)
		switch err {
		case nil:
		case io.EOF:
			// rs.Next会在Z报文(准备好查询)和T报文(行描述)上返回io.EOF
			// 需要读取报文直到读到Z报文
			if rs.done {
				err = nil
				return
			}
		default:
			return
		}
	}
}

func (rs *rows) Columns() (names []string) {
	names = rs.colNames
	return
}

func (rs *rows) Result() (result driver.Result) {
	if nil == rs.result {
		result = emptyRows
		return
	}
	result = rs.result
	return
}

func (rs *rows) Tag() (tag string) {
	tag = rs.tag
	return
}

// 解析D报文中的结果集，将解析后的结果集赋值给应用的OUT类型变量
func (cn conn) ParseOutValues(rb *readBuf, bindParams []driver.Value, colTyps []fieldDesc) (err error) {
	defer cn.errRecover(&err)

	var nOut = 0
	var nRet = 0
	var n = 0
	byteBool := []byte{116, 102}
	sBind := make([]bindStruct, len(bindParams))

	for i, t := range bindParams {
		sBind[i].out, sBind[i].isOut = t.(sql.Out)
		sBind[i].ret, sBind[i].isRet = t.(*ReturnStatus)
		if sBind[i].isOut {
			nOut++
		} else if sBind[i].isRet {
			nRet++
		}
	}
	n = nOut + nRet

	nParams := rb.int16()
	paramLength := make([]int, nParams)
	byteValues := make([][]byte, nParams)
	outNum := 0
	var retValue []byte

	for i := 0; i < nParams; i++ {
		paramLength[i] = rb.int32()
		if paramLength[i] != -1 {
			byteValues[i] = rb.next(paramLength[i])
		}
	}

	//SQLSERVER模式下存储过程固定有返回值
	//获取返回值并根据需要进行赋值
	if cn.databaseMode == "sqlserver" {
		retValue = byteValues[outNum]
		outNum++
		if n != nParams {
			//用户可能没绑定返回值
			if n != nParams-1 || nRet != 0 {
				err = fmt.Errorf("unexpected error: expect %v out parameters, got %v", n, nParams)
				return
			}
		}
	} else if n != nParams {
		err = fmt.Errorf("unexpected error: expect %v out parameters, got %v", n, nParams)
		return
	}

	for i, _ := range sBind {
		if sBind[i].isOut {
			if paramLength[outNum] == -1 {
				outNum++
				continue
			}
			dest := sBind[i].out.Dest
			switch colTyps[outNum].OID {
			case cn.allOid.T_int8, cn.allOid.T_bigint:
				//对于整型数值如果不开启disable_prepared_binary_result则均为二进制格式
				//默认不开启disable_prepared_binary_result
				switch dest.(type) {
				case *int:
					*dest.(*int) = int(binary.BigEndian.Uint64(byteValues[outNum]))
				case *int64:
					*dest.(*int64) = int64(binary.BigEndian.Uint64(byteValues[outNum]))
				case *sql.NullInt64:
					if paramLength[outNum] == -1 {
						(*dest.(*sql.NullInt64)).Valid = false
					} else {
						(*dest.(*sql.NullInt64)).Int64 = int64(binary.BigEndian.Uint64(byteValues[outNum]))
						(*dest.(*sql.NullInt64)).Valid = true
					}
				}
			case cn.allOid.T_int4, cn.allOid.T_int:
				switch dest.(type) {
				case *int:
					*dest.(*int) = int(binary.BigEndian.Uint32(byteValues[outNum]))
				case *int32:
					*dest.(*int32) = int32(binary.BigEndian.Uint32(byteValues[outNum]))
				}
			case cn.allOid.T_int2, cn.allOid.T_smallint:
				*dest.(*int16) = int16(binary.BigEndian.Uint16(byteValues[outNum]))
			case cn.allOid.T_tinyint:
				fillByte := fill64(byteValues[outNum])
				if cn.databaseMode == "sqlserver" {
					*dest.(*uint8) = uint8(binary.BigEndian.Uint64(fillByte))
				} else {
					*dest.(*int8) = int8(binary.BigEndian.Uint64(fillByte))
				}
			case cn.allOid.T_bit, cn.allOid.T_varbit:
				if cn.databaseMode == "sqlserver" {
					if paramLength[outNum] == -1 {
						(*dest.(*sql.NullBool)).Valid = false
					} else {
						var isBool bool
						if byteValues[outNum][0] == 49 {
							isBool = true
						} else if byteValues[outNum][0] == 48 {
							isBool = false
						}
						(*dest.(*sql.NullBool)).Bool = isBool
						(*dest.(*sql.NullBool)).Valid = true
					}
				} else if cn.databaseMode == "mysql" {
					*dest.(*[]byte) = byteValues[outNum]
				}
			case cn.allOid.T_uint8:
				s, _ := strconv.ParseUint(string(byteValues[outNum]), 10, 64)
				switch dest.(type) {
				case *uint:
					*dest.(*uint) = uint(s)
				case *uint64:
					*dest.(*uint64) = uint64(s)
				case *uint32:
					*dest.(*uint32) = uint32(s)
				case *uint16:
					*dest.(*uint16) = uint16(s)
				case *uint8:
					*dest.(*uint8) = uint8(s)
				}
			case cn.allOid.T_uint4:
				s, _ := strconv.ParseUint(string(byteValues[outNum]), 10, 64)
				switch dest.(type) {
				case *uint:
					*dest.(*uint) = uint(s)
				case *uint32:
					*dest.(*uint32) = uint32(s)
				case *uint16:
					*dest.(*uint16) = uint16(s)
				case *uint8:
					*dest.(*uint8) = uint8(s)
				}
			case cn.allOid.T_float8, cn.allOid.T_float:
				f, _ := strconv.ParseFloat(string(byteValues[outNum]), 64)
				switch dest.(type) {
				case *float64:
					*dest.(*float64) = f
				case *sql.NullFloat64:
					if paramLength[outNum] == -1 {
						(*dest.(*sql.NullFloat64)).Valid = false
					} else {
						(*dest.(*sql.NullFloat64)).Float64 = f
						(*dest.(*sql.NullFloat64)).Valid = true
					}
				}
			case cn.allOid.T_float4, cn.allOid.T_real:
				f, _ := strconv.ParseFloat(string(byteValues[outNum]), 64)
				*dest.(*float32) = float32(f)
			case cn.allOid.T_bytea, cn.allOid.T_blob, cn.allOid.T_longblob, cn.allOid.T_mediumblob, cn.allOid.T_tinyblob, cn.allOid.T_json:
				*dest.(*[]byte) = byteValues[outNum]
			case cn.allOid.T_binary, cn.allOid.T_varbinary:
				*dest.(*[]byte) = byteValues[outNum]
			case cn.allOid.T_bool:
				var isBool bool
				if byteValues[outNum][0] == byteBool[0] {
					isBool = true
				} else if byteValues[outNum][0] == byteBool[1] {
					isBool = false
				}
				switch dest.(type) {
				case *bool:
					*dest.(*bool) = isBool
				case *sql.NullBool:
					if paramLength[outNum] == -1 {
						(*dest.(*sql.NullBool)).Valid = false
					} else {
						(*dest.(*sql.NullBool)).Bool = isBool
						(*dest.(*sql.NullBool)).Valid = true
					}
				}
			case cn.allOid.T_varchar, cn.allOid.T_char, cn.allOid.T_bpchar, cn.allOid.T_text, cn.allOid.T_clob, cn.allOid.T_longtext, cn.allOid.T_mediumtext, cn.allOid.T_tinytext:
				switch dest.(type) {
				case *string:
					*dest.(*string) = string(byteValues[outNum])
				case *sql.NullString:
					if paramLength[outNum] == -1 {
						(*dest.(*sql.NullString)).Valid = false
					} else {
						(*dest.(*sql.NullString)).String = string(byteValues[outNum])
						(*dest.(*sql.NullString)).Valid = true
					}
				case *CursorString:
					(*dest.(*CursorString)).CursorName = string(byteValues[outNum])
				}
			case cn.allOid.T_varcharbyte:
				switch dest.(type) {
				case *string:
					*dest.(*string) = string(byteValues[outNum])
				case *VarChar:
					*dest.(*VarChar) = VarChar(string(byteValues[outNum]))
				case *VarCharMax:
					*dest.(*VarCharMax) = VarCharMax(string(byteValues[outNum]))
				}
			case cn.allOid.T_nvarchar:
				switch dest.(type) {
				case *string:
					*dest.(*string) = string(byteValues[outNum])
				case *NVarCharMax:
					*dest.(*NVarCharMax) = NVarCharMax(string(byteValues[outNum]))
				case sql.NullString:
					if paramLength[outNum] == -1 {
						(*dest.(*sql.NullString)).Valid = false
					} else {
						(*dest.(*sql.NullString)).String = string(byteValues[outNum])
						(*dest.(*sql.NullString)).Valid = true
					}
				}
			case cn.allOid.T_bpcharbyte:
				*dest.(*string) = string(byteValues[outNum])
			case cn.allOid.T_nchar:
				switch dest.(type) {
				case *string:
					*dest.(*string) = string(byteValues[outNum])
				case *NChar:
					*dest.(*NChar) = NChar(string(byteValues[outNum]))
				}
			case cn.allOid.T_timestamp, cn.allOid.T_timestamptz, cn.allOid.T_datetime:
				switch dest.(type) {
				case *time.Time:
					*dest.(*time.Time) = (parseTs(nil, string(byteValues[outNum]))).(time.Time)
				case *DateTime1:
					*dest.(*DateTime1) = DateTime1((parseTs(nil, string(byteValues[outNum]))).(time.Time))
				}
			case cn.allOid.T_time, cn.allOid.T_timetz:
				switch dest.(type) {
				case *time.Time:
					*dest.(*time.Time) = (parseTime(nil, string(byteValues[outNum]))).(time.Time)
				case *civil.Time:
					*dest.(*civil.Time) = (parseCivilTime(string(byteValues[outNum]))).(civil.Time)
				}
			case cn.allOid.T_date:
				switch dest.(type) {
				case *time.Time:
					*dest.(*time.Time) = (parseDate(nil, string(byteValues[outNum]))).(time.Time)
				case *civil.Date:
					*dest.(*civil.Date) = (parseCivilDate(string(byteValues[outNum]))).(civil.Date)
				}
			case cn.allOid.T_refcursor:
				(*dest.(*CursorString)).CursorName = string(byteValues[outNum])
			case cn.allOid.T_numeric, cn.allOid.T_money: //decimal
				newString := strings.ReplaceAll(string(byteValues[outNum]), ",", "")
				s, _ := strconv.ParseUint(newString, 10, 64)
				f, _ := strconv.ParseFloat(newString, 64)
				switch dest.(type) {
				case *uint:
					*dest.(*uint) = uint(s)
				case *uint64:
					*dest.(*uint64) = uint64(s)
				case *uint32:
					*dest.(*uint32) = uint32(s)
				case *uint16:
					*dest.(*uint16) = uint16(s)
				case *uint8:
					*dest.(*uint8) = uint8(s)
				case *int:
					if GetPlatformBit() == 64 {
						*dest.(*int) = int(binary.BigEndian.Uint64(byteValues[outNum]))
					} else {
						*dest.(*int) = int(binary.BigEndian.Uint32(byteValues[outNum]))
					}
				case *int64:
					*dest.(*int64) = int64(binary.BigEndian.Uint64(byteValues[outNum]))
				case *int32:
					*dest.(*int32) = int32(binary.BigEndian.Uint32(byteValues[outNum]))
				case *int16:
					*dest.(*int16) = int16(binary.BigEndian.Uint16(byteValues[outNum]))
				case *int8:
					*dest.(*uint8) = uint8(s)
				case *float64:
					*dest.(*float64) = f
				case *float32:
					*dest.(*float32) = float32(f)
				case *decimal.Decimal:
					//numeric/decimal
					num, _ := decimal.NewFromString(string(byteValues[outNum]))
					*dest.(*decimal.Decimal) = num
				case *[]byte:
					*dest.(*[]byte) = byteValues[outNum]
				}
			default:
				err = fmt.Errorf("unsupported out parameter type: %v", colTyps[outNum].OID)
				return
			}
			outNum++
		} else if sBind[i].isRet {
			*sBind[i].ret = ReturnStatus(int32(binary.BigEndian.Uint32(retValue)))
		}
	}
	err = nil
	return
}

func (rs *rows) Next(dest []driver.Value) (err error) {
	if rs.done {
		err = io.EOF
		return
	}

	conn := rs.cn
	if conn.bad {
		err = driver.ErrBadConn
		return
	}
	defer conn.errRecover(&err)

	for {
		t := conn.recv1Buf(&rs.rb)

		switch t {
		case 'E':
			err = parseError(&rs.rb)
		case 'C', 'I':
			if 'C' == t {
				rs.result, rs.tag = conn.parseComplete(rs.rb.string(), 0)
			}
			rs.outParamInMultiRes = true
			continue
		case 'T':
			next := parsePortalRowDescribe(&rs.rb)
			rs.next = &next
			err = io.EOF
			if rs.outParamInMultiRes {
				rs.outParamInMultiRes = false
			}
			return
		case 'D':
			if rs.outParamInMultiRes {
				//OUT参数的结果集
				//SQLSERVER模式下存储过程固定有返回值并在此结果集中
				err = rs.cn.ParseOutValues(&rs.rb, rs.bindParams, rs.TMessage.colTyps)
				if err != nil {
					panic(err)
				}
				rs.next = nil
				err = io.EOF
				return
			} else {
				//DQL的结果集
				n := rs.rb.int16()
				if nil != err {
					conn.bad = true
					errorf("unexpected DataRow after error %s", err)
				}
				if len(dest) > n {
					dest = dest[:n]
				}
				for i := range dest {
					l := rs.rb.int32()
					if -1 == l {
						// 空对象
						dest[i] = nil
						continue
					} else if 0 == l {
						// 空字符串
						typs := rs.colTyps[i].OID
						switch typs {
						case conn.allOid.T_varchar, conn.allOid.T_char, conn.allOid.T_bpchar, conn.allOid.T_text, conn.allOid.T_varcharbyte, conn.allOid.T_nvarchar, conn.allOid.T_bpcharbyte, conn.allOid.T_nchar:
							dest[i] = ""
						default:
							dest[i] = nil
						}
						continue
					}
					dest[i] = decode(&conn.parameterStatus, rs.rb.next(l), rs.colTyps[i].OID, rs.colFmts[i], *rs.cn)
				}
			}
			return
		case 'Z':
			conn.processReadyForQuery(&rs.rb)
			rs.done = true
			if nil != err {
				return
			}
			err = io.EOF
			return
		case 'n':
			continue
		default:
			errorf("unexpected message after execute: %q", t)
		}
	}
}

func (rs *rows) HasNextResultSet() (hasNext bool) {
	return rs.next != nil && !rs.done
}

func (rs *rows) NextResultSet() (err error) {
	if nil == rs.next {
		err = io.EOF
		return
	}
	rs.rowsHeader = *rs.next
	rs.next = nil
	err = nil
	return
}

// QuoteIdentifier将标识引用起来作为SQL语句的一部分(比如一个表或一个列名):
//
//	tblname := "my_table"
//	data := "my_data"
//	quoted := kb.QuoteIdentifier(tblname)
//	err := db.Exec(fmt.Sprintf("INSERT INTO %s VALUES ($1)", quoted), data)
//
// name中的所有双引号都将被转义，被引用的标识都是大小写敏感的
// 果输入的字符串包含0字节，结果将在此处截断
func QuoteIdentifier(name string) (s string) {
	end := strings.IndexRune(name, 0)
	if -1 < end {
		name = name[:end]
	}
	s = `"` + strings.Replace(name, `"`, `""`, -1) + `"`
	return
}

// QuoteLiteral将字符引起来作为SQL的一部分(经常被用来向DDL和其它不接收参数的语句传字符的参数):
//
//	exp_date := kb.QuoteLiteral("2023-01-05 15:00:00Z")
//	err := db.Exec(fmt.Sprintf("CREATE ROLE my_user VALID UNTIL %s", exp_date))
//
// 引号都将被转义
// 反斜杠(\)都将被替换为双反斜杠(\\)
// C风格的转义符都将被认为是字符串
func QuoteLiteral(literal string) (s string) {
	literal = strings.Replace(literal, `'`, `''`, -1)
	if strings.Contains(literal, `\`) {
		literal = strings.Replace(literal, `\`, `\\`, -1)
		literal = ` E'` + literal + `'`
	} else {
		literal = `'` + literal + `'`
	}
	s = literal
	return
}

func md5s(s string) (md5String string) {
	h := md5.New()
	h.Write([]byte(s))
	md5String = fmt.Sprintf("%x", h.Sum(nil))
	return
}

func sm3s(s string, k string) (sm3String string) {
	h := hmac.New(sm3.New, []byte(k))
	h.Write([]byte(s))
	sm3String = fmt.Sprintf("%x", h.Sum(nil))
	return
}

func (cn *conn) sendBinaryParameters(b *writeBuf, args []driver.Value) {
	// 如果需要将参数以二进制格式传递，在传参同时也需要创建一个参数格式数组
	var paramFormats []int
	for i, x := range args {
		_, ok := x.([]byte)
		if ok {
			if nil == paramFormats {
				paramFormats = make([]int, len(args))
			}
			paramFormats[i] = 1
		}
	}
	if nil == paramFormats {
		b.int16(0)
	} else {
		b.int16(len(paramFormats))
		for _, x := range paramFormats {
			b.int16(x)
		}
	}

	b.int16(len(args))
	for _, x := range args {
		if nil == x {
			b.int32(-1)
		} else {
			datum := binaryEncode(&cn.parameterStatus, x, cn)
			b.int32(len(datum))
			b.bytes(datum)
		}
	}
}

func (cn *conn) processParameterStatus(rb *readBuf) {
	var err error

	param := rb.string()
	switch param {
	case "server_version":
		var major1, major2, minor int
		_, err = fmt.Sscanf(rb.string(), "%d.%d.%d", &major1, &major2, &minor)
		if nil == err {
			cn.parameterStatus.serverVersion = major1*10000 + major2*100 + minor
		}
	case "TimeZone":
		cn.parameterStatus.currentLocation, err = time.LoadLocation(rb.string())
		if nil != err {
			cn.parameterStatus.currentLocation = nil
		}
	default:
		// ignore
	}
	return
}

func (cn *conn) processReadyForQuery(rb *readBuf) {
	cn.txnStatus = transactionStatus(rb.byte())
	return
}

func (cn *conn) sendBinaryModeQuery(query string, args []driver.Value) {
	if 65536 <= len(args) {
		errorf("got %d parameters but Kingbase only supports 65535 parameters", len(args))
	}

	b := cn.writeBuf('P')
	b.byte(0) // 未命名语句
	b.string(query)
	b.int16(0)

	b.next('B')
	b.int16(0) // 未命名入口/语句
	cn.sendBinaryParameters(b, args)
	b.bytes(colFmtDataAllText)

	b.next('D')
	b.byte('P')
	b.byte(0) // 未命名入口

	b.next('E')
	b.byte(0)
	b.int32(0)

	b.next('S')
	cn.send(b)
	return
}

func (cn *conn) readReadyForQuery() {
	t, r := cn.recv1()
	switch t {
	case 'Z':
		cn.processReadyForQuery(r)
		return
	default:
		cn.bad = true
		errorf("unexpected message %q; expected ReadyForQuery", t)
	}
	return
}

func (cn *conn) processBackendKeyData(rb *readBuf) {
	cn.processID = rb.int32()
	cn.secretKey = rb.int32()
	return
}

func (cn *conn) readParseResponse() (err error) {
	defer cn.errRecover(&err)

	t, r := cn.recv1()
	switch t {
	case 'E':
		err = parseError(r)
		cn.readReadyForQuery()
		panic(err)
	case '1':
		return
	default:
		cn.bad = true
		errorf("unexpected Parse response %q", t)
	}
	return
}

func (cn *conn) readStatementDescribeResponse() (paramTyps []oid.Oid, colNames []string, colTyps []fieldDesc, err error) {
	defer cn.errRecover(&err)

	for {
		t, r := cn.recv1()
		switch t {
		case 't':
			nparams := r.int16()
			paramTyps = make([]oid.Oid, nparams)
			for i := range paramTyps {
				paramTyps[i] = r.oid()
			}
		case 'T':
			colNames, colTyps = parseStatementRowDescribe(r)
			return
		case 'E':
			err = parseError(r)
			cn.readReadyForQuery()
			panic(err)
		case 'n':
			colNames, colTyps = nil, nil
			return
		default:
			cn.bad = true
			errorf("unexpected Describe statement response %q", t)
		}
	}
}

func (cn *conn) readPortalDescribeResponse() (header rowsHeader) {
	t, r := cn.recv1()
	switch t {
	case 'T':
		header = parsePortalRowDescribe(r)
		return
	case 'n':
		header = rowsHeader{}
		return
	case 'E':
		err := parseError(r)
		cn.readReadyForQuery()
		panic(err)
	default:
		cn.bad = true
		errorf("unexpected Describe response %q", t)
	}
	panic("not reached")
}

func (cn *conn) readBindResponse() {
	t, r := cn.recv1()
	switch t {
	case '2':
		return
	case 'E':
		err := parseError(r)
		cn.readReadyForQuery()
		panic(err)
	default:
		cn.bad = true
		errorf("unexpected Bind response %q", t)
	}
	return
}

func (cn *conn) postExecuteWorkaround(st *stmt) (colNames []string, colTyps []fieldDesc, colFmts []format, err error) {
	// 处理sql.DB.QueryRow中的bug:在Go1.2及更早版本中，go会忽略rows.Next中的所有错误，包括可能在查询执行中出现的错误
	// 为了避免可能出现的问题，在此处多接收一个后端返回的报文。如果不为错误则查询执行成功
	// 此处将接收到的报文存储在conn结构体中，recv1会在调用rows.Next或rows.Close时将存储的报文返回
	// 如果报文为错误信息，等待直到ReadyForQuery并将错误返回到调用者
	TMessage := false
	for {
		t, r := cn.recv1()
		switch t {
		case 'E':
			err = parseError(r)
			cn.readReadyForQuery()
			return
		case 'T':
			//case1:发送P/D后，D没有返回T(无out参数的存储过程调用DQL)，发送E后返回T报文，更新结果集信息
			//case2:发送P/D后，返回了T报文，但执行后又返回T报文，则为多结果集
			//		P/D返回的T报文描述OUT参数且已保存，但OUT参数的结果集在最后一个，所以此处仍用最新的T使标准接口能正确分配泛型数组接收数据
			newRows := parsePortalRowDescribe(r)
			colNames, colTyps, colFmts = newRows.colNames, newRows.colTyps, newRows.colFmts
			TMessage = true
		case 'D':
			if !TMessage { //执行完成后，返回报文中没有T时再尝试判断该结果集是否为OUT参数的结果集
				//该结果集可能为OUT参数的结果集(返回值也在此结果集中)，需要对绑定的参数进行判断
				//若为OUT参数则进行赋值处理，否则保存报文并在Next中进行处理
				sBind := make([]bindStruct, len(st.bindParams))
				resultSet := false
				for i, t := range st.bindParams {
					sBind[i].out, sBind[i].isOut = t.(sql.Out)
					sBind[i].ret, sBind[i].isRet = t.(*ReturnStatus)
					if sBind[i].isOut || sBind[i].isRet {
						resultSet = true
						break
					}
				}
				if resultSet {
					err = cn.ParseOutValues(r, st.bindParams, st.TMessage.colTyps)
					if err != nil {
						return
					}
					//已经处理完D报文，保存剩下的其它报文
					continue
				} else {
					cn.saveMessage(t, r)
					//仍使用之前的结果集信息
					colNames, colTyps, colFmts = st.colNames, st.colTyps, st.colFmts
					return
				}
			} else { //执行完成后，返回报文中包含T报文，则该T报文为多结果集中的第一个T或者是无out参数的存储过程调用DQL
				cn.saveMessage(t, r)
				return
			}
		case 'C':
			fallthrough
		case 'I':
			cn.saveMessage(t, r)
			if !TMessage { //执行完并没有得到T报文，则可能在P/D报文之后返回了或无T报文，仍使用之前的结果集信息
				colNames, colTyps, colFmts = st.colNames, st.colTyps, st.colFmts
			}
			return
		case 'n':
			continue
		default:
			cn.bad = true
			err = fmt.Errorf("unexpected message during extended query execution: %q", t)
			return
		}
	}
}

func fill64(v []byte) []byte {
	fillByte := []byte{0, 0, 0, 0, 0, 0, 0, 0}
	vLen := len(v)
	nFill := 8 - vLen
	for i := 0; i < vLen; i++ {
		fillByte[nFill+i] = v[i]
	}
	return fillByte
}

func (cn *conn) readExecuteResponse(protocolState string, v []driver.Value, colTyps []fieldDesc, colFmts []format) (res driver.Result, commandTag string, err error) {
	var dest interface{}
	var lastID int64 = 0
	var alreadyGet bool = false
	//var tag string
	for {
		t, r := cn.recv1()
		switch t {
		case 'I':
			if nil != err {
				cn.bad = true
				errorf("unexpected %q after error %s", t, err)
			}
			if 'I' == t {
				res = emptyRows
			}
		case 'T':
			//如果为存储过程out参数的结果集则在postExecuteWorkaround中处理
			//此时的结果集理论上仅有为了获取自增列id而添加RETURNING *返回的结果集
			//且结果集元信息已存储在语句句柄中，通过参数传入
			if nil != err {
				cn.bad = true
				errorf("unexpected %q after error %s", t, err)
			}
		case 'D':
			if nil != err {
				cn.bad = true
				errorf("unexpected %q after error %s", t, err)
			}
			//在开启getLastInsertId后且对于insert语句尝试获取第一列自增列id
			if cn.getLastInsertId.enable && cn.getLastInsertId.isInsert {
				if n := r.int16(); n < 1 {
					cn.bad = true
					errorf("unexpected returning num of columns:%d", n)
				}
				if nil != err {
					cn.bad = true
					errorf("unexpected DataRow after error %s", err)
				}
				//第一列必须为oid.T_int4
				switch colTyps[0].OID {
				case cn.allOid.T_int4:
					//获取第一列自增列id
					l := r.int32()
					if -1 == l {
						dest = 0
						continue
					}
					dest = decode(&cn.parameterStatus, r.next(l), colTyps[0].OID, colFmts[0], *cn)
					if !alreadyGet {
						lastID = dest.(int64)
						alreadyGet = true
					}
				default:
					//errorf("the first column(oid:%d) is not auto_increment id", colTyps[0].OID)
				}
				continue
			}
		case 'E':
			err = parseError(r)
		case 'Z':
			cn.processReadyForQuery(r)
			if nil == res && nil == err {
				err = errUnexpectedReady
			}
			return res, commandTag, err
		case 'C':
			if nil != err {
				cn.bad = true
				errorf("unexpected CommandComplete after error %s", err)
			}
			res, commandTag = cn.parseComplete(r.string(), lastID)
		case 'n':
			continue
		default:
			cn.bad = true
			errorf("unknown %s response: %q", protocolState, t)
		}
	}
}

func parseStatementRowDescribe(rb *readBuf) (colNames []string, colTyps []fieldDesc) {
	n := rb.int16()
	colNames, colTyps = make([]string, n), make([]fieldDesc, n)
	for i := range colNames {
		colNames[i] = rb.string()
		rb.next(6)
		colTyps[i].OID, colTyps[i].Len, colTyps[i].Mod = rb.oid(), rb.int16(), rb.int32()
		// 在描述语句时格式代码未知，设置为0
		rb.next(2)
	}
	return
}

func parsePortalRowDescribe(rb *readBuf) (rh rowsHeader) {
	n := rb.int16()
	colNames := make([]string, n)
	colFmts := make([]format, n)
	colTyps := make([]fieldDesc, n)
	for i := range colNames {
		colNames[i] = rb.string()
		rb.next(6)
		colTyps[i].OID, colTyps[i].Len, colTyps[i].Mod, colFmts[i] = rb.oid(), rb.int16(), rb.int32(), format(rb.int16())
	}
	rh = rowsHeader{
		colNames: colNames,
		colFmts:  colFmts,
		colTyps:  colTyps,
	}
	return
}

// parseEnviron进行一些环境变量处理
//
// 环境变量设置的连接信息比默认值优先级高
// 比显示传递的连接参数优先级低
func parseEnviron(env []string) (out map[string]string) {
	out = make(map[string]string)

	for _, v := range env {
		parts := strings.SplitN(v, "=", 2)

		accrue := func(keyname string) { out[keyname] = parts[1] }
		unsupported := func() { panic(fmt.Sprintf("setting %v not supported", parts[0])) }

		switch parts[0] {
		case "KINGBASE_HOST":
			accrue("host")
		case "KINGBASE_PORT":
			accrue("port")
		case "KINGBASE_DATABASE":
			accrue("dbname")
		case "KINGBASE_USER":
			accrue("user")
		case "KINGBASE_PASSWORD":
			accrue("password")
		case "KINGBASE_OPTIONS":
			accrue("options")
		case "KINGBASE_APPNAME":
			accrue("application_name")
		case "KINGBASE_SSLMODE":
			accrue("sslmode")
		case "KINGBASE_SSLCERT":
			accrue("sslcert")
		case "KINGBASE_SSLKEY":
			accrue("sslkey")
		case "KINGBASE_SSLROOTCERT":
			accrue("sslrootcert")
		case "KINGBASE_CONNECT_TIMEOUT":
			accrue("connect_timeout")
		//case "KINGBASE_KEEPALIVE_IDLE": accrue("keepalive_idle")//go中idle和interval为相同值
		case "KINGBASE_KEEPALIVE_INTERVAL":
			accrue("keepalive_interval")
		case "KINGBASE_KEEPALIVE_COUNT":
			accrue("keepalive_count")
		case "KINGBASE_TCP_USER_TIMEOUT":
			accrue("tcp_user_timeout")
		case "KINGBASE_GET_AUTO_INCREMENT_ID":
			accrue("get_auto_increment_id")
		case "KINGBASE_CLIENTENCODING":
			accrue("client_encoding")
		case "KINGBASE_DATESTYLE":
			accrue("datestyle")
		case "KCITZ":
			accrue("timezone")
		case "KCIGEQO":
			accrue("geqo")
		case "KINGBASE_HOSTADDR":
			fallthrough
		case "KINGBASE_SERVICE", "KINGBASE_SERVICEFILE", "KINGBASE_REALM":
			fallthrough
		case "KINGBASE_REQUIRESSL", "KINGBASE_SSLCRL":
			fallthrough
		case "KINGBASE_REQUIREPEER":
			fallthrough
		case "KINGBASE_KRBSRVNAME", "KINGBASE_GSSLIB":
			fallthrough
		case "KINGBASE_SYSCONFDIR", "KINGBASE_LOCALEDIR":
			unsupported()
		}
	}
	return
}

// isUTF8返回name是否为UTF-8或UNICODE格式
func isUTF8(name string) (state bool) {
	s := strings.Map(alnumLowerASCII, name)
	state = (s == "utf8" || s == "unicode")
	return
}

func alnumLowerASCII(ch rune) (result rune) {
	if ch >= 'A' && 'Z' >= ch {
		result = ch + ('a' - 'A')
		return
	}
	if ch >= 'a' && 'z' >= ch || ch >= '0' && '9' >= ch {
		result = ch
		return
	}
	result = -1
	return
}
