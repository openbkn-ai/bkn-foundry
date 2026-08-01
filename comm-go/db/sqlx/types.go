package sqlx

// DBConfig 数据库配置信息
type DBConfig struct {
	User             string `yaml:"user_name"`
	Password         string `yaml:"user_pwd"`
	Host             string `yaml:"db_host"`
	Port             int    `yaml:"db_port"`
	HostRead         string `yaml:"db_host_read"`
	PortRead         int    `yaml:"db_port_read"`
	Database         string `yaml:"db_name"`
	Charset          string `yaml:"db_charset"`
	Timeout          int    `yaml:"timeout"`
	ReadTimeout      int    `yaml:"read_timeout"`
	WriteTimeout     int    `yaml:"write_timeout"`
	MaxOpenConns     int    `yaml:"max_open_conns"`
	MaxOpenReadConns int    `yaml:"max_open_read_conns"`
	ConnMaxLifeTime  int    `yaml:"conn_max_life_time_s"`
	CustomDriver     string `yaml:"custom_driver"`
	ParseTime        string `yaml:"parseTime"`
	Loc              string `yaml:"loc"`
}
