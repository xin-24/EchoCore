package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

const (
	defaultHost       = "127.0.0.1"
	defaultPort       = 8080
	defaultOneBotPath = "/onebot/v11/ws"
)

type Config struct {
	Server Server
	OneBot OneBot
}

type Server struct {
	Host string
	Port int
}

type OneBot struct {
	Path        string
	AccessToken string
}

func Load() (Config, error) {
	host := envOrDefault("ECHOCORE_SERVER_HOST", defaultHost)
	portValue := envOrDefault("ECHOCORE_SERVER_PORT", strconv.Itoa(defaultPort))

	port, err := strconv.Atoi(portValue)
	if err != nil || port < 1 || port > 65535 {
		return Config{}, fmt.Errorf("ECHOCORE_SERVER_PORT must be an integer between 1 and 65535")
	}

	oneBotPath := envOrDefault("ECHOCORE_ONEBOT_PATH", defaultOneBotPath)
	if !strings.HasPrefix(oneBotPath, "/") || strings.ContainsAny(oneBotPath, "?#") {
		return Config{}, fmt.Errorf("ECHOCORE_ONEBOT_PATH must be an absolute URL path without a query or fragment")
	}

	return Config{
		Server: Server{Host: host, Port: port},
		OneBot: OneBot{
			Path:        oneBotPath,
			AccessToken: os.Getenv("ECHOCORE_ONEBOT_ACCESS_TOKEN"),
		},
	}, nil
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
