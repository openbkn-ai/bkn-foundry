package conf

type HTTPServerConfig struct {
	Address         string
	InternalAddress string
}

func NewHTTPServerConfig() HTTPServerConfig {
	return HTTPServerConfig{
		Address:         ":8080",
		InternalAddress: ":8081",
	}
}
