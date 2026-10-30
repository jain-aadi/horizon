package api

import (
	"context"
	"net/http"
)

// APIHandler defines the HTTP API handler interface.
type APIHandler interface {
	RegisterRoutes(mux *http.ServeMux)
	Start(ctx context.Context) error
	Stop() error
}

// ServerConfig holds HTTP server configuration.
type ServerConfig struct {
	Host string
	Port int
}

// NewServerConfig creates a default server config.
func NewServerConfig() ServerConfig {
	return ServerConfig{
		Host: "127.0.0.1",
		Port: 8080,
	}
}

// Address returns the server listen address.
func (c ServerConfig) Address() string {
	return c.Host + ":" + string(rune(c.Port))
}