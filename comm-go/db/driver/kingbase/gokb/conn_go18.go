/******************************************************************************
* 版权信息：中电科金仓（北京）科技股份有限公司

* 作者：KingbaseES

* 文件名：conn_go18.go

* 功能描述：对database/sql相关接口的实现

* 其它说明：

* 修改记录：
  1.修改时间：

  2.修改人：

  3.修改内容：

******************************************************************************/

package gokb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"io/ioutil"
	"reflect"
	"time"

	"github.com/golang-sql/civil"
	"github.com/shopspring/decimal"
)

type converter struct{}

func IsAlias(v interface{}) bool {
	k := (reflect.TypeOf(v)).Kind()
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	case reflect.Float32, reflect.Float64:
		return true
	case reflect.Bool:
		return true
	case reflect.String:
		return true
	default:
		return false
	}
}

func IsValue(v interface{}) bool {
	if v == nil {
		return true
	}
	switch v.(type) {
	case int, int64, int32, int16, int8, sql.NullInt64: //有符号整型
		return true
	case uint, uint64, uint32, uint16, uint8: //无符号整型
		return true
	case []uint8: //bytea
		return true
	case float64, float32, sql.NullFloat64: //浮点型
		return true
	case bool, sql.NullBool: //布尔类型
		return true
	case string: //字符串类型
		return true
	case time.Time: //时间类型
		return true
	case CursorString: //KB自定义游标类型
		return true
	case DateTime1, civil.Date, civil.Time: //兼容mssql的DateTime1, civil.Date, civil.Time
		return true
	case VarChar, VarCharMax, NVarCharMax, NChar, sql.NullString: //兼容mssql的VarChar, VarCharMax, NVarCharMax, NChar, sql.NullString
		return true
	case decimal.Decimal:
		return true
	default:
		return IsAlias(v)
	}
}

// callValuerValue返回vr.Value()，来自go的database/sql包
var valuerReflectType = reflect.TypeOf((*driver.Valuer)(nil)).Elem()

func callValuerValue(vr driver.Valuer) (v driver.Value, err error) {
	if rv := reflect.ValueOf(vr); rv.Kind() == reflect.Pointer &&
		rv.IsNil() &&
		rv.Type().Elem().Implements(valuerReflectType) {
		return nil, nil
	}
	return vr.Value()
}

// 实现"CheckNamedValue"接口中的"ConvertValue"函数
func (c converter) ConvertValue(v interface{}) (driver.Value, error) {
	//对gokb.Array等类型的支持
	switch vr := v.(type) {
	case driver.Valuer:
		sv, err := callValuerValue(vr)
		if err != nil {
			return nil, err
		}
		if !IsValue(sv) {
			return nil, fmt.Errorf("non-Value type %T returned from Value", sv)
		}
		return sv, nil
	}

	//判断是否为支持的in类型参数
	if IsValue(v) {
		return v, nil
	}

	//判断是否为out类型参数
	var sBind bindStruct
	sBind.out, sBind.isOut = v.(sql.Out)
	sBind.ret, sBind.isRet = v.(*ReturnStatus)
	if sBind.isRet { //SQLSERVER模式下存储过程返回值类型
		return v, nil
	}
	switch sBind.out.Dest.(type) {
	case *int, *int64, *int32, *int16, *int8, *sql.NullInt64:
		return v, nil
	case *uint, *uint64, *uint32, *uint16, *uint8:
		return v, nil
	case *[]uint8:
		return v, nil
	case *float64, *float32, *sql.NullFloat64:
		return v, nil
	case *bool, *sql.NullBool:
		return v, nil
	case *string:
		return v, nil
	case *time.Time:
		return v, nil
	case *CursorString:
		return v, nil
	case *DateTime1, *civil.Date, *civil.Time:
		return v, nil
	case *VarChar, *VarCharMax, *NVarCharMax, *NChar, *sql.NullString:
		return v, nil
	case *decimal.Decimal:
		return v, nil
	}
	//其它in类型参数
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer:
		// indirect pointers
		if rv.IsNil() {
			return nil, nil
		} else {
			return c.ConvertValue(rv.Elem().Interface())
		}
	case reflect.Slice:
		ek := rv.Type().Elem().Kind()
		if ek == reflect.Uint8 {
			return rv.Bytes(), nil
		}
		return nil, fmt.Errorf("unsupported type %T, a slice of %s", v, ek)
	}
	return nil, fmt.Errorf("kb unsupported type %T, a %s", v, rv.Kind())
}

// 实现"CheckNamedValue"接口
func (cn *conn) CheckNamedValue(nv *driver.NamedValue) (err error) {
	nv.Value, err = converter{}.ConvertValue(nv.Value)
	return
}

// 实现"QueryerContext"接口
func (cn *conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	var newArgs []driver.Value
	var err error

	if len(args) != 0 {
		if cn.databaseMode == "sqlserver" {
			query, newArgs, _, err = replaceProcName(query, args, nil)
		} else {
			query, newArgs, _, err = replaceHolderMarkers(query, args, nil)
		}
		if err != nil {
			return nil, err
		}
	} else {
		for _, nv := range args {
			newArgs = append(newArgs, nv.Value)
		}
	}

	finish := cn.watchCancel(ctx)
	r, err := cn.query(query, newArgs)
	if err != nil {
		if finish != nil {
			finish()
		}
		return nil, err
	}
	r.finish = finish
	return r, nil
}

func (st *stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	finish := st.cn.watchCancel(ctx)
	var newArgs []driver.Value
	var err error

	if len(args) != 0 {
		if st.cn.databaseMode == "sqlserver" {
			st.queryName, newArgs, st.nameList, err = replaceProcName(st.queryName, args, st.nameList)
		} else {
			st.queryName, newArgs, st.nameList, err = replaceHolderMarkers(st.queryName, args, st.nameList)
		}
		if err != nil {
			return nil, err
		}
	} else {
		for _, nv := range args {
			newArgs = append(newArgs, nv.Value)
		}
	}

	r, err := st.query(newArgs)
	if err != nil {
		if finish != nil {
			finish()
		}
		return nil, err
	}
	r.finish = finish
	return r, nil
}

// 实现"ExecerContext"接口
func (cn *conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	var newArgs []driver.Value
	var err error

	if len(args) != 0 {
		if cn.databaseMode == "sqlserver" {
			query, newArgs, _, err = replaceProcName(query, args, nil)
		} else {
			query, newArgs, _, err = replaceHolderMarkers(query, args, nil)
		}
		if err != nil {
			return nil, err
		}
	} else {
		for _, nv := range args {
			newArgs = append(newArgs, nv.Value)
		}
	}

	if finish := cn.watchCancel(ctx); finish != nil {
		defer finish()
	}

	return cn.Exec(query, newArgs)
}

func (st *stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	var newArgs []driver.Value
	var err error

	if len(args) != 0 {
		if st.cn.databaseMode == "sqlserver" {
			st.queryName, newArgs, st.nameList, err = replaceProcName(st.queryName, args, st.nameList)
		} else {
			st.queryName, newArgs, st.nameList, err = replaceHolderMarkers(st.queryName, args, st.nameList)
		}
		if err != nil {
			return nil, err
		}
	} else {
		for _, nv := range args {
			newArgs = append(newArgs, nv.Value)
		}
	}

	if finish := st.cn.watchCancel(ctx); finish != nil {
		defer finish()
	}

	return st.Exec(newArgs)
}

// 实现"ConnBeginTx"接口
func (cn *conn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	var mode string

	switch sql.IsolationLevel(opts.Isolation) {
	case sql.LevelDefault:
		// Don't touch mode: use the server's default
	case sql.LevelReadUncommitted:
		mode = " ISOLATION LEVEL READ UNCOMMITTED"
	case sql.LevelReadCommitted:
		mode = " ISOLATION LEVEL READ COMMITTED"
	case sql.LevelRepeatableRead:
		mode = " ISOLATION LEVEL REPEATABLE READ"
	case sql.LevelSerializable:
		mode = " ISOLATION LEVEL SERIALIZABLE"
	default:
		return nil, fmt.Errorf("kb: isolation level not supported: %d", opts.Isolation)
	}

	if opts.ReadOnly {
		mode += " READ ONLY"
	} else {
		mode += " READ WRITE"
	}

	tx, err := cn.begin(mode)
	if err != nil {
		return nil, err
	}
	cn.txnFinish = cn.watchCancel(ctx)
	return tx, nil
}

func (cn *conn) Ping(ctx context.Context) error {
	if finish := cn.watchCancel(ctx); finish != nil {
		defer finish()
	}
	rows, err := cn.simpleQuery(";")
	if err != nil {
		return driver.ErrBadConn
	}
	rows.Close()
	return nil
}

func (cn *conn) watchCancel(ctx context.Context) func() {
	if done := ctx.Done(); done != nil {
		finished := make(chan struct{})
		go func() {
			select {
			case <-done:
				// 在此处函数级的上下文被取消，因此它不能用于额外的网络请求来取消查询
				// 需要创建一个新的上下文并传给dial
				ctxCancel, cancel := context.WithTimeout(context.Background(), time.Second*10)
				defer cancel()

				_ = cn.cancel(ctxCancel)
				finished <- struct{}{}
			case <-finished:
			}
		}()
		return func() {
			select {
			case <-finished:
			case finished <- struct{}{}:
			}
		}
	}
	return nil
}

func (cn *conn) cancel(ctx context.Context) error {
	c, err := dial(ctx, cn.dialer, cn.opts)
	if err != nil {
		return err
	}
	defer c.Close()

	{
		can := conn{
			c: c,
		}
		err = can.ssl(cn.opts)
		if err != nil {
			return err
		}

		w := can.writeBuf(0)
		w.int32(80877102) //取消请求代码
		w.int32(cn.processID)
		w.int32(cn.secretKey)

		if err := can.sendStartupPacket(w); err != nil {
			return err
		}
	}

	// 读取数据直到读到EOF以确保服务端收到了cancel请求
	{
		_, err := io.Copy(ioutil.Discard, c)
		return err
	}
}
