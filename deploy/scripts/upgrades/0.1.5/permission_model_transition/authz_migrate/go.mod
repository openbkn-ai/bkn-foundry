module github.com/openbkn-ai/bkn-foundry/deploy/permission-transition-0.1.5/authz-migrate

go 1.25.0

require (
	github.com/glebarez/sqlite v1.11.0
	github.com/google/uuid v1.6.0
	github.com/openbkn-ai/bkn-comm-go v0.0.4
	github.com/openbkn-ai/bkn-foundry/bkn-safe/server v0.0.0
	gopkg.in/yaml.v3 v3.0.1
	gorm.io/driver/mysql v1.6.0
	gorm.io/gorm v1.31.2
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	gitee.com/chunanyong/dm v1.8.23 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/emirpasic/gods v1.18.1 // indirect
	github.com/glebarez/go-sqlite v1.22.0 // indirect
	github.com/go-sql-driver/mysql v1.10.1 // indirect
	github.com/golang-sql/civil v0.0.0-20220223132316-b832511892a9 // indirect
	github.com/golang/snappy v1.0.0 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	golang.org/x/exp v0.0.0-20251219203646-944ab1f22d93 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	modernc.org/libc v1.67.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	modernc.org/sqlite v1.42.2 // indirect
)

replace github.com/openbkn-ai/bkn-foundry/bkn-safe/server => ../../../../../../bkn-safe/server
