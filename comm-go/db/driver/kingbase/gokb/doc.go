/*
gokb包为基于database/sql包的Kingbase驱动

一般情况下需要使用database/sql而不是直接使用gokb中的接口
比如:

	import (
		"database/sql"

		_ "github.com/openbkn-ai/bkn-comm-go/db/driver/kingbase/gokb"
	)

	func main() {
		connStr := "user=kbgotest dbname=kbgotest sslmode=verify-full"
		db, err := sql.Open("kingbase", connStr)
		if err != nil {
			log.Fatal(err)
		}

		age := 21
		rows, err := db.Query("SELECT name FROM users WHERE age = $1", age)
		…
	}

也可以通过URL连接数据库
比如:

	connStr := "kingbase://kbgotest:password@localhost/kbgotest?sslmode=verify-full"
	db, err := sql.Open("kingbase", connStr)

与libkci类似，当建立与数据库的连接时，需要提供若干连接参数
libkci支持的连接参数gokb也均支持
此外，gokb也支持运行时参数(比如search_path等)，可直接在连接串中指定

支持以下的连接参数:

  - dbname - 数据库名
  - user - 用户名
  - password - 用户的密码
  - host - 主机(默认为localhost)
  - port -端口(默认为54321)
  - sslmode - 是否使用SSL(默认为require)
  - fallback_application_name - 默认的应用名，当没有提供应用名时则使用该应用名
  - connect_timeout - 连接的最大等待时间，单位为秒。指定为0或不指定表示无限期等待
  - sslcert - ssl证书的位置
  - sslkey - ssl秘钥的位置
  - sslrootcert - 根证书的位置

sslmode的有效值:

  - disable - 不使用SSL
  - require - 总是使用SSL(跳过验证)
  - verify-ca - 使用SSL(验证证书是否为信任的CA签名)
  - verify-full - 使用SSL(验证证书是否为信任的CA签名并且主机名与证书中的匹配)

当参数值中包含空格时需要使用单引号，比如:

	"user=kbgotest password='with spaces'"

使用反斜杠转义下一个字符:

	"user=space\ man password='it\'s valid'"

注意：
1.连接参数client_encoding(设置文本的编码格式)必须设置为"UTF-8"

2.当连接参数binary_parameters开启时，[]bytes数据将以二进制格式发送到后端

3.gokb可能返回Error类型的错误，可通过如下方式查看错误细节:

	if err, ok := err.(*kb.Error); ok {
	    fmt.Println("kb error:", err.Code.Name())
	}

4.CopyIn内部调用COPY FROM，不能在显示事务之外进行COPY
用法:

	txn, err := db.Begin()
	if err != nil {
		log.Fatal(err)
	}

	stmt, err := txn.Prepare(kb.CopyIn("users", "name", "age"))
	if err != nil {
		log.Fatal(err)
	}

	for _, user := range users {
		_, err = stmt.Exec(user.Name, int64(user.Age))
		if err != nil {
			log.Fatal(err)
		}
	}

	_, err = stmt.Exec()
	if err != nil {
		log.Fatal(err)
	}

	err = stmt.Close()
	if err != nil {
		log.Fatal(err)
	}

	err = txn.Commit()
	if err != nil {
		log.Fatal(err)
	}

5.要开始监听通知，首先要通过调用NewListener打开一个到数据库的连接。此链接不能用于LISTEN/NOTIFY以外的任何操作
调用Listen将打开一个通知通道，在该通道上生成的通知将影响Listen上的发送
通知通道在调用Unlisten之前将一直保持打开状态，但连接丢失可能会导致某些通知丢失
为了解决这个问题，每当连接丢失后重新建立连接时，Listener都会通过通知通道发送一个nil指针
应用程序可以通过在对NewListener调用中设置事件回调来获取底层连接状态的信息

单个监听器可以安全地用于并发程序，这意味着通常不需要再应用程序中创建多个监听器。
但是监听器总是连接到单个数据库，因此需要为希望接收通知的每个数据库创建一个新的Listener实例
*/
package gokb
