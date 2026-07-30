package gokb

import (
	"bufio"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net"
	"reflect"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-comm-go/db/driver/kingbase/gokb/oid"
)

/*****************conn.go*****************/
const GOKB_Version_V009R030C011B0003PSI002 = iota
const GOKB_CompileTime_20250731 = iota
const GOKB_CompilerVersion_go1_19_1_linux_amd64 = iota

// 常见错误
var (
	ErrNotSupported              = errors.New("kb: Unsupported command")
	ErrInFailedTransaction       = errors.New("kb: Could not complete operation in a failed transaction")
	ErrSSLNotSupported           = errors.New("kb: SSL is not enabled on the server")
	ErrSSLKeyHasWorldPermissions = errors.New("kb: Private key file has group or world access. Permissions should be u=rw (0600) or less")
	ErrCouldNotDetectUsername    = errors.New("kb: Could not detect default username. Please provide one explicitly")

	errUnexpectedReady = errors.New("unexpected ReadyForQuery")
	errNoRowsAffected  = errors.New("no RowsAffected available after the empty statement")
	errNoLastInsertID  = errors.New("no LastInsertId available after the empty statement")
)

// Kingbase数据库驱动
type Driver struct{}

type parameterStatus struct {
	// 与server_version_num相同格式的服务端版本, 无效时为0
	serverVersion int

	// 基于当前对话时区的时区值
	currentLocation *time.Location
}

type transactionStatus byte

const (
	txnStatusIdle                transactionStatus = 'I'
	txnStatusIdleInTransaction   transactionStatus = 'T'
	txnStatusInFailedTransaction transactionStatus = 'E'
)

// Dialer为dialer接口，与创建网络连接相关
type Dialer interface {
	Dial(network, address string) (net.Conn, error)
	DialTimeout(network, address string, timeout time.Duration) (net.Conn, error)
}

// DialerContext为需要上下文的dialer接口
type DialerContext interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

type defaultDialer struct {
	d net.Dialer
}

type conn struct {
	c         net.Conn
	buf       *bufio.Reader
	namei     int
	scratch   [512]byte
	txnStatus transactionStatus
	txnFinish func()

	// 保存在CancelRequest时需要的连接参数
	dialer Dialer
	opts   values

	// CancelRequest报文需要的数据
	processID int
	secretKey int

	parameterStatus parameterStatus

	saveMessageType   byte
	saveMessageBuffer []byte

	// 当此连接出错时被设置为true
	bad bool

	// 设置后，此连接在从预备语句接收查询结果时不能再使用二进制格式
	disablePreparedBinaryResult bool

	// 设置后，[]byte类型参数将被以二进制格式发送
	binaryParameters bool

	// 如果此连接正在进行COPY操作则被置为true
	inCopy bool

	// 非空时notices将被同步发送
	noticeHandler func(*Error)

	//本连接的数据库模式
	databaseMode string
	//该数据库模式下oid和类型名的对应
	allOid   oid.AllOid
	TypeName map[oid.Oid]string

	// 设置后，对于insert语句将在末尾拼接returning *以获取自增列id
	getLastInsertId autoIncrementId

	// 多主机地址的连接重试次数和每次尝试的延迟
	retry int
	delay int
	// 配置多主机地址时是否要求必须连接主节点
	requirePrimary bool
}

type autoIncrementId struct {
	enable   bool
	isInsert bool
}

type timeoutParams struct {
	//单位s
	connect_timeout    int
	keepalive_interval int
	keepalive_count    int
	//单位ms
	tcp_user_timeout int
}

type values map[string]string

type scanner struct {
	s []rune
	i int
}

type driverResult struct {
	rowsAffected int64
	lastInsertId int64
}

type noRows struct{}

var emptyRows noRows

var _ driver.Result = noRows{}

const formatText format = 0
const formatBinary format = 1

// 结果列格式代码置为1(二进制格式).
var colFmtDataAllBinary = []byte{0, 1, 0, 1}

// 结果列格式代码置为0(文本)
var colFmtDataAllText = []byte{0, 0}

type format int

type stmt struct {
	cn   *conn
	name string
	rowsHeader
	colFmtData []byte
	paramTyps  []oid.Oid
	closed     bool
	pSended    bool
	queryName  string
	nameList   []string
	multiRes
}

type rowsHeader struct {
	colNames []string
	colTyps  []fieldDesc
	colFmts  []format
}

type rows struct {
	cn     *conn
	finish func()
	rowsHeader
	done   bool
	rb     readBuf
	result driver.Result
	tag    string

	next *rowsHeader

	multiRes
	//判断下一个结果集是否为OUT参数的结果集
	outParamInMultiRes bool
}

/*****************array.go*****************/
var typeByteSlice = reflect.TypeOf([]byte{})
var typeDriverValuer = reflect.TypeOf((*driver.Valuer)(nil)).Elem()
var typeSQLScanner = reflect.TypeOf((*sql.Scanner)(nil)).Elem()

// ByteaArray表示一维的bytea类型的数组
type ByteaArray [][]byte

// Float64Array表示一维的双精度数组
type Float64Array []float64

// ArrayDelimiter可由driver.Valuer或sql.Scanner实现
// 通过GenericArray来重写数组分隔符
type ArrayDelimiter interface {
	// ArrayDelimiter返回当前元素类型的分隔符
	ArrayDelimiter() string
}

// BoolArray表示一维的布尔类型的数组
type BoolArray []bool

// GenericArray实现了driver.Valuer和sql.Scanner接口，作为任何维数的数组或切片
type GenericArray struct{ A interface{} }

// Int64Array表示一维的整型数组
type Int64Array []int64

// StringArray表示一维的字符串类型的数组
type StringArray []string

/*****************notify.go*****************/
// Notification表示来自数据库的一个notification
type Notification struct {
	// 后端通知的进程ID(PID)
	BePid int
	// 发送通知的通道名
	Channel string
	// 通知内容，未指定时为空字符串
	Extra string
}

const (
	connStateIdle int32 = iota
	connStateExpectResponse
	connStateExpectReadyForQuery
)

type message struct {
	typ byte
	err error
}

var errListenerConnClosed = errors.New("kb: ListenerConn has been closed")

type ListenerConn struct {
	connectionLock sync.Mutex
	cn             *conn
	err            error

	connState int32

	senderLock sync.Mutex

	notificationChan chan<- *Notification

	replyChan chan message
}

var errListenerClosed = errors.New("kb: Listener has been closed")

// ErrChannelAlreadyOpen在通道打开后从Listen返回
var ErrChannelAlreadyOpen = errors.New("kb: channel is already open")

// ErrChannelNotOpen在通道未打开时从Unlisten返回
var ErrChannelNotOpen = errors.New("kb: channel is not open")

// ListenerEventType为监听器时间类型的枚举
type ListenerEventType int

const (
	// ListenerEventConnected在数据库连接初始化时发出
	// 错误消息总是为空
	ListenerEventConnected ListenerEventType = iota

	// ListenerEventDisconnected在出错或调用Close导致数据库连接丢失时发出
	// 错误消息为丢失连接的原因
	ListenerEventDisconnected

	// ListenerEventReconnected在数据库连接丢失并重建后发出
	// 错误信息总是为空
	// 在此时间被发送后，一个空的kb.Notification将被发送到Listener.Notify通道
	ListenerEventReconnected

	// ListenerEventConnectionAttemptFailed在尝试连接数据库但失败后被发送
	// 错误信息为连接没有成功的原因
	ListenerEventConnectionAttemptFailed
)

// EventCallbackType为事件回调类型
type EventCallbackType func(event ListenerEventType, err error)

// Listener为监听数据库的notifications提供了一个接口
type Listener struct {
	// 接收来自数据库通知的通道
	Notify chan *Notification

	name                 string
	minReconnectInterval time.Duration
	maxReconnectInterval time.Duration
	dialer               Dialer
	eventCallback        EventCallbackType

	lock                 sync.Mutex
	isClosed             bool
	reconnectCond        *sync.Cond
	cn                   *ListenerConn
	connNotificationChan <-chan *Notification
	channels             map[string]struct{}
}

/*****************connector.go*****************/
// 结构体表示gokb驱动的固定配置，实现database/sql/driver的Connector接口
// 可通过database/sql的OpenDB函数创建任意数量的Conn
type Connector struct {
	opts   values
	dialer Dialer

	// 多主机地址和端口
	hosts []string
	ports []string
}

/*****************encode.go*****************/
// locationCache缓存客户端的时区
type locationCache struct {
	cache map[int]*time.Location
	lock  sync.Mutex
}

// 所有连接都有相同的时区列表
var globalLocationCache = newLocationCache()

var infinityTsNegative time.Time
var infinityTsPositive time.Time

const (
	infinityTsEnabledAlready        = "kb: infinity timestamp enabled already"
	infinityTsNegativeMustBeSmaller = "kb: infinity timestamp: negative value must be smaller (before) than positive"
)

var infinityTsEnabled = false

// NullTime表示一个可能为空的time.Time类型的值
// NullTime实现了sql.Scanner接口，所以可被用来作为扫描后的目标结构，与sql.NullString类似
type NullTime struct {
	Time  time.Time
	Valid bool // 不为空时Valid为true
}

/*****************copy.go*****************/
var (
	errCopyInClosed               = errors.New("kb: copyin statement has already been closed")
	errBinaryCopyNotSupported     = errors.New("kb: only text format supported for COPY")
	errCopyToNotSupported         = errors.New("kb: COPY TO is not supported")
	errCopyNotSupportedOutsideTxn = errors.New("kb: COPY is only allowed inside a transaction")
	errCopyInProgress             = errors.New("kb: COPY in progress")
)

type copyin struct {
	cn      *conn
	buffer  []byte
	rowData chan []byte
	done    chan bool

	closed bool

	sync.Mutex // guards err
	err        error
}

const ciBufferSize = 64 * 1024

// ciBufferFlushSize的大小需要满足在缓冲区填满并需要重新分配内存之前刷新缓冲区
const ciBufferFlushSize = 63 * 1024

/*****************error.go*****************/
// 错误等级
const (
	Efatal   = "FATAL"
	Epanic   = "PANIC"
	Ewarning = "WARNING"
	Enotice  = "NOTICE"
	Edebug   = "DEBUG"
	Einfo    = "INFO"
	Elog     = "LOG"
)

type Error struct {
	Severity         string
	Code             ErrorCode
	Message          string
	Detail           string
	Hint             string
	Position         string
	InternalPosition string
	InternalQuery    string
	Where            string
	Schema           string
	Table            string
	Column           string
	DataTypeName     string
	Constraint       string
	File             string
	Line             string
	Routine          string
}

// ErrorCode为5个字符长的错误码
type ErrorCode string

type ErrorClass string

// KBError为支持旧版本的接口，新版本直接使用Error即可
type KBError interface {
	Error() string
	Fatal() bool
	Get(k byte) (v string)
}

// errorCodeNames为5位错误码和错误信息之间的映射
var errorCodeNames = map[ErrorCode]string{
	// Class 00 - 执行成功
	"00000": "successful_completion",
	// Class 01 - 警告
	"01000": "warning",
	"0100C": "dynamic_result_sets_returned",
	"01008": "implicit_zero_bit_padding",
	"01003": "null_value_eliminated_in_set_function",
	"01007": "privilege_not_granted",
	"01006": "privilege_not_revoked",
	"01004": "string_data_right_truncation",
	"01P01": "deprecated_feature",
	// Class 02 - 无数据
	"02000": "no_data",
	"02001": "no_additional_dynamic_result_sets_returned",
	// Class 03 - SQL语句暂未执行完成
	"03000": "sql_statement_not_yet_complete",
	// Class 08 - 连接异常
	"08000": "connection_exception",
	"08003": "connection_does_not_exist",
	"08006": "connection_failure",
	"08001": "sqlclient_unable_to_establish_sqlconnection",
	"08004": "sqlserver_rejected_establishment_of_sqlconnection",
	"08007": "transaction_resolution_unknown",
	"08P01": "protocol_violation",
	// Class 09 - 事务异常
	"09000": "triggered_action_exception",
	// Class 0A - 模式不支持
	"0A000": "feature_not_supported",
	// Class 0B - 无效的事务初始化
	"0B000": "invalid_transaction_initiation",
	// Class 0F - Locator异常
	"0F000": "locator_exception",
	"0F001": "invalid_locator_specification",
	// Class 0L - 无效的Grantor
	"0L000": "invalid_grantor",
	"0LP01": "invalid_grant_operation",
	// Class 0P - 无效的角色
	"0P000": "invalid_role_specification",
	// Class 0Z - 判断异常
	"0Z000": "diagnostics_exception",
	"0Z002": "stacked_diagnostics_accessed_without_active_handler",
	// Class 20 - 没有找到该情况
	"20000": "case_not_found",
	// Class 21 - 基数违规
	"21000": "cardinality_violation",
	// Class 22 - 数据异常
	"22000": "data_exception",
	"2202E": "array_subscript_error",
	"22021": "character_not_in_repertoire",
	"22008": "datetime_field_overflow",
	"22012": "division_by_zero",
	"22005": "error_in_assignment",
	"2200B": "escape_character_conflict",
	"22022": "indicator_overflow",
	"22015": "interval_field_overflow",
	"2201E": "invalid_argument_for_logarithm",
	"22014": "invalid_argument_for_ntile_function",
	"22016": "invalid_argument_for_nth_value_function",
	"2201F": "invalid_argument_for_power_function",
	"2201G": "invalid_argument_for_width_bucket_function",
	"22018": "invalid_character_value_for_cast",
	"22007": "invalid_datetime_format",
	"22019": "invalid_escape_character",
	"2200D": "invalid_escape_octet",
	"22025": "invalid_escape_sequence",
	"22P06": "nonstandard_use_of_escape_character",
	"22010": "invalid_indicator_parameter_value",
	"22023": "invalid_parameter_value",
	"2201B": "invalid_regular_expression",
	"2201W": "invalid_row_count_in_limit_clause",
	"2201X": "invalid_row_count_in_result_offset_clause",
	"22009": "invalid_time_zone_displacement_value",
	"2200C": "invalid_use_of_escape_character",
	"2200G": "most_specific_type_mismatch",
	"22004": "null_value_not_allowed",
	"22002": "null_value_no_indicator_parameter",
	"22003": "numeric_value_out_of_range",
	"2200H": "sequence_generator_limit_exceeded",
	"22026": "string_data_length_mismatch",
	"22001": "string_data_right_truncation",
	"22011": "substring_error",
	"22027": "trim_error",
	"22024": "unterminated_c_string",
	"2200F": "zero_length_character_string",
	"22P01": "floating_point_exception",
	"22P02": "invalid_text_representation",
	"22P03": "invalid_binary_representation",
	"22P04": "bad_copy_file_format",
	"22P05": "untranslatable_character",
	"2200L": "not_an_xml_document",
	"2200M": "invalid_xml_document",
	"2200N": "invalid_xml_content",
	"2200S": "invalid_xml_comment",
	"2200T": "invalid_xml_processing_instruction",
	// Class 23 - 违反完整性约束
	"23000": "integrity_constraint_violation",
	"23001": "restrict_violation",
	"23502": "not_null_violation",
	"23503": "foreign_key_violation",
	"23505": "unique_violation",
	"23514": "check_violation",
	"23P01": "exclusion_violation",
	// Class 24 - 无效的游标状态
	"24000": "invalid_cursor_state",
	// Class 25 - 无效的事务状态
	"25000": "invalid_transaction_state",
	"25001": "active_sql_transaction",
	"25002": "branch_transaction_already_active",
	"25008": "held_cursor_requires_same_isolation_level",
	"25003": "inappropriate_access_mode_for_branch_transaction",
	"25004": "inappropriate_isolation_level_for_branch_transaction",
	"25005": "no_active_sql_transaction_for_branch_transaction",
	"25006": "read_only_sql_transaction",
	"25007": "schema_and_data_statement_mixing_not_supported",
	"25P01": "no_active_sql_transaction",
	"25P02": "in_failed_sql_transaction",
	// Class 26 - 无效的SQL语句名
	"26000": "invalid_sql_statement_name",
	// Class 27 - 触发数据更改违规
	"27000": "triggered_data_change_violation",
	// Class 28 - 无效的授权规范
	"28000": "invalid_authorization_specification",
	"28P01": "invalid_password",
	// Class 2B - 依赖的特权描述符仍然存在
	"2B000": "dependent_privilege_descriptors_still_exist",
	"2BP01": "dependent_objects_still_exist",
	// Class 2D - 无效的事务终止
	"2D000": "invalid_transaction_termination",
	// Class 2F - SQL程序异常
	"2F000": "sql_routine_exception",
	"2F005": "function_executed_no_return_statement",
	"2F002": "modifying_sql_data_not_permitted",
	"2F003": "prohibited_sql_statement_attempted",
	"2F004": "reading_sql_data_not_permitted",
	// Class 34 - 无效的游标名
	"34000": "invalid_cursor_name",
	// Class 38 - 外部程序异常
	"38000": "external_routine_exception",
	"38001": "containing_sql_not_permitted",
	"38002": "modifying_sql_data_not_permitted",
	"38003": "prohibited_sql_statement_attempted",
	"38004": "reading_sql_data_not_permitted",
	// Class 39 - 外部常规调用异常
	"39000": "external_routine_invocation_exception",
	"39001": "invalid_sqlstate_returned",
	"39004": "null_value_not_allowed",
	"39P01": "trigger_protocol_violated",
	"39P02": "srf_protocol_violated",
	// Class 3B - 保存点异常
	"3B000": "savepoint_exception",
	"3B001": "invalid_savepoint_specification",
	// Class 3D - 无效的目录名
	"3D000": "invalid_catalog_name",
	// Class 3F - 无效的模式名
	"3F000": "invalid_schema_name",
	// Class 40 - 事务回滚
	"40000": "transaction_rollback",
	"40002": "transaction_integrity_constraint_violation",
	"40001": "serialization_failure",
	"40003": "statement_completion_unknown",
	"40P01": "deadlock_detected",
	// Class 42 - 语法错误或语法违规
	"42000": "syntax_error_or_access_rule_violation",
	"42601": "syntax_error",
	"42501": "insufficient_privilege",
	"42846": "cannot_coerce",
	"42803": "grouping_error",
	"42P20": "windowing_error",
	"42P19": "invalid_recursion",
	"42830": "invalid_foreign_key",
	"42602": "invalid_name",
	"42622": "name_too_long",
	"42939": "reserved_name",
	"42804": "datatype_mismatch",
	"42P18": "indeterminate_datatype",
	"42P21": "collation_mismatch",
	"42P22": "indeterminate_collation",
	"42809": "wrong_object_type",
	"42703": "undefined_column",
	"42883": "undefined_function",
	"42P01": "undefined_table",
	"42P02": "undefined_parameter",
	"42704": "undefined_object",
	"42701": "duplicate_column",
	"42P03": "duplicate_cursor",
	"42P04": "duplicate_database",
	"42723": "duplicate_function",
	"42P05": "duplicate_prepared_statement",
	"42P06": "duplicate_schema",
	"42P07": "duplicate_table",
	"42712": "duplicate_alias",
	"42710": "duplicate_object",
	"42702": "ambiguous_column",
	"42725": "ambiguous_function",
	"42P08": "ambiguous_parameter",
	"42P09": "ambiguous_alias",
	"42P10": "invalid_column_reference",
	"42611": "invalid_column_definition",
	"42P11": "invalid_cursor_definition",
	"42P12": "invalid_database_definition",
	"42P13": "invalid_function_definition",
	"42P14": "invalid_prepared_statement_definition",
	"42P15": "invalid_schema_definition",
	"42P16": "invalid_table_definition",
	"42P17": "invalid_object_definition",
	// Class 44 - 违反检查选项
	"44000": "with_check_option_violation",
	// Class 53 - 资源不足
	"53000": "insufficient_resources",
	"53100": "disk_full",
	"53200": "out_of_memory",
	"53300": "too_many_connections",
	"53400": "configuration_limit_exceeded",
	// Class 54 - 超出程序限制
	"54000": "program_limit_exceeded",
	"54001": "statement_too_complex",
	"54011": "too_many_columns",
	"54023": "too_many_arguments",
	// Class 55 - 对象不在预定状态
	"55000": "object_not_in_prerequisite_state",
	"55006": "object_in_use",
	"55P02": "cant_change_runtime_param",
	"55P03": "lock_not_available",
	// Class 57 - operator干预
	"57000": "operator_intervention",
	"57014": "query_canceled",
	"57P01": "admin_shutdown",
	"57P02": "crash_shutdown",
	"57P03": "cannot_connect_now",
	"57P04": "database_dropped",
	// Class 58 - 系统错误
	"58000": "system_error",
	"58030": "io_error",
	"58P01": "undefined_file",
	"58P02": "duplicate_file",
	// Class F0 - 配置文件错误
	"F0000": "config_file_error",
	"F0001": "lock_file_exists",
	// Class HV - 外部数据封装错误(SQL/MED)
	"HV000": "fdw_error",
	"HV005": "fdw_column_name_not_found",
	"HV002": "fdw_dynamic_parameter_value_needed",
	"HV010": "fdw_function_sequence_error",
	"HV021": "fdw_inconsistent_descriptor_information",
	"HV024": "fdw_invalid_attribute_value",
	"HV007": "fdw_invalid_column_name",
	"HV008": "fdw_invalid_column_number",
	"HV004": "fdw_invalid_data_type",
	"HV006": "fdw_invalid_data_type_descriptors",
	"HV091": "fdw_invalid_descriptor_field_identifier",
	"HV00B": "fdw_invalid_handle",
	"HV00C": "fdw_invalid_option_index",
	"HV00D": "fdw_invalid_option_name",
	"HV090": "fdw_invalid_string_length_or_buffer_length",
	"HV00A": "fdw_invalid_string_format",
	"HV009": "fdw_invalid_use_of_null_pointer",
	"HV014": "fdw_too_many_handles",
	"HV001": "fdw_out_of_memory",
	"HV00P": "fdw_no_schemas",
	"HV00J": "fdw_option_name_not_found",
	"HV00K": "fdw_reply_handle",
	"HV00Q": "fdw_schema_not_found",
	"HV00R": "fdw_table_not_found",
	"HV00L": "fdw_unable_to_create_execution",
	"HV00M": "fdw_unable_to_create_reply",
	"HV00N": "fdw_unable_to_establish_connection",
	// Class P0 - Kingbase数据库错误
	"P0000": "kingbase_error",
	"P0001": "raise_exception",
	"P0002": "no_data_found",
	"P0003": "too_many_rows",
	// Class XX - 内部错误
	"XX000": "internal_error",
	"XX001": "data_corrupted",
	"XX002": "index_corrupted",
}

/*****************rows.go*****************/
const headerSize = 4

type fieldDesc struct {
	// 数据类型的对象ID
	OID oid.Oid
	// 数据类型的长度(同sys_type.typlen)
	Len int
	// 数据类型的属性(同sys_attribute.atttypmod).
	Mod int
}

type bindStruct struct {
	//存储过程OUT参数
	out    sql.Out
	isOut  bool
	isBoth bool
	typ    oid.Oid

	//存储过程返回值
	ret   *ReturnStatus
	isRet bool
}

type CursorString struct {
	CursorName string
}

type multiRes struct {
	//多结果集 out参数的T报文
	TMessage rowsHeader
	//执行时绑定的参数
	bindParams []driver.Value
	//绑定的参数是否有返回值
	hasRet bool
}

/*****************sqlserver数据类型*****************/
//兼容mssql的ReturnStatus，用于接收存储过程的返回值
type ReturnStatus int32

type DateTime1 time.Time

type VarChar string

type VarCharMax string

type NVarCharMax string

type NChar string
