package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

const (
	defaultHost        = "127.0.0.1"
	defaultPort        = 8080
	defaultOneBotPath  = "/onebot/v11/ws"
	defaultEnvironment = EnvironmentDevelopment
)

type Environment string

const (
	EnvironmentDevelopment Environment = "development"
	EnvironmentProduction  Environment = "production"
)

type Config struct {
	Environment Environment
	Server      Server
	OneBot      OneBot
	Group       Group
}

type Group struct {
	ControlUserIDs []string
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
	environment := Environment(strings.ToLower(envOrDefault("ECHOCORE_ENV", string(defaultEnvironment))))
	if environment != EnvironmentDevelopment && environment != EnvironmentProduction {
		return Config{}, fmt.Errorf("ECHOCORE_ENV must be development or production")
	}

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

	controlUserIDs, err := parseControlUserIDs(os.Getenv("ECHOCORE_GROUP_CONTROL_USER_IDS"))
	if err != nil {
		return Config{}, err
	}

	return Config{
		Environment: environment,
		Server:      Server{Host: host, Port: port},
		OneBot: OneBot{
			Path:        oneBotPath,
			AccessToken: os.Getenv("ECHOCORE_ONEBOT_ACCESS_TOKEN"),
		},
		Group: Group{ControlUserIDs: controlUserIDs},
	}, nil
}

// parseControlUserIDs 校验并去重逗号分隔的 QQ 号；未配置时默认拒绝所有启停操作。
func parseControlUserIDs(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var ids []string
	seen := make(map[string]struct{})
	for _, item := range strings.Split(value, ",") {
		id := strings.TrimSpace(item)
		number, err := strconv.ParseInt(id, 10, 64)
		if err != nil || number <= 0 || strconv.FormatInt(number, 10) != id {
			return nil, fmt.Errorf("ECHOCORE_GROUP_CONTROL_USER_IDS must contain comma-separated positive QQ IDs without leading zeros")
		}
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	return ids, nil
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
