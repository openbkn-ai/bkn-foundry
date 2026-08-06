package httpserver

import (
	"context"
	"fmt"
	"net"
	"net/http"
)

type Server struct {
	httpServer *http.Server
}

func New(address string, handler http.Handler) *Server {
	return &Server{
		httpServer: &http.Server{
			Addr:    address,
			Handler: handler,
		},
	}
}

func (s *Server) Start() error {
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("start http server: %w", err)
	}

	return nil
}

// StartAsync binds the listener before returning so callers can fail startup
// atomically when a required companion listener is unavailable.
func (s *Server) StartAsync() (<-chan error, error) {
	listener, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", s.httpServer.Addr, err)
	}
	result := make(chan error, 1)
	go func() {
		err := s.httpServer.Serve(listener)
		if err == http.ErrServerClosed {
			err = nil
		}
		result <- err
	}()
	return result, nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
