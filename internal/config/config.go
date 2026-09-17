package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
)

const (
	defaultHost = "127.0.0.1"
	defaultPort = 8080
)

type Config struct {
	Server Server
}

type Server struct {
	Host string
	Port int
}

func Load() (Config, error) {
	host := envOrDefault("ECHOCORE_SERVER_HOST", defaultHost)
	portValue := envOrDefault("ECHOCORE_SERVER_PORT", strconv.Itoa(defaultPort))

	port, err := strconv.Atoi(portValue)
	if err != nil || port < 1 || port > 65535 {
		return Config{}, fmt.Errorf("ECHOCORE_SERVER_PORT must be an integer between 1 and 65535")
	}

	return Config{Server: Server{Host: host, Port: port}}, nil
}

func (s Server) Address() string {
	return net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
}

func envOrDefault(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}
