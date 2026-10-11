package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	BotToken             string
	Port                 int
	DBPath               string
	RedisURL             string
	CommandPrefix        string
	BotPresence          string
	BotPresenceType      int
	ExternalURL          string
	DiscordInvite        string
	BotIdempotencyEnvKey string
	LogLevel             string
	LogFormat            string
	MaxSockets           int
}

func loadDotEnv() {
	data, err := os.ReadFile(".env")
	if err != nil {
		return
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) == 0 || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.TrimSpace(parts[1])
			v = strings.Trim(v, `"'`)
			if os.Getenv(k) == "" {
				os.Setenv(k, v)
			}
		}
	}
}

func Load() (*Config, error) {
	loadDotEnv()
	token := os.Getenv("BOT_TOKEN")
	if token == "" {
		return nil, errors.New("BOT_TOKEN is required")
	}

	port := 4001
	if p := os.Getenv("PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			port = v
		}
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./data/lanyard.db"
	}

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = os.Getenv("REDIS_URI")
	}
	if redisURL == "" {
		redisURL = os.Getenv("REDIS_DSN")
	}
	if redisURL == "" {
		if host := os.Getenv("REDIS_HOST"); host != "" {
			redisURL = fmt.Sprintf("redis://%s:6379", host)
		}
	}

	prefix := os.Getenv("COMMAND_PREFIX")
	if prefix == "" {
		prefix = "."
	}

	botPresence := os.Getenv("BOT_PRESENCE")
	if botPresence == "" {
		botPresence = "you <3"
	}

	botPresenceType := 3 // Watching
	if pt := os.Getenv("BOT_PRESENCE_TYPE"); pt != "" {
		if v, err := strconv.Atoi(pt); err == nil {
			botPresenceType = v
		}
	}

	extURL := os.Getenv("EXTERNAL_URL")
	if extURL == "" {
		extURL = "https://api.lanyard.rest"
	}
	extURL = strings.TrimRight(extURL, "/")

	logLevel := strings.ToLower(os.Getenv("LOG_LEVEL"))
	if logLevel == "" {
		logLevel = "info"
	}

	logFormat := strings.ToLower(os.Getenv("LOG_FORMAT"))
	if logFormat == "" {
		logFormat = "text"
	}

	maxSockets := 5000
	if ms := os.Getenv("MAX_SOCKETS"); ms != "" {
		if v, err := strconv.Atoi(ms); err == nil && v > 0 {
			maxSockets = v
		}
	}

	return &Config{
		BotToken:             token,
		Port:                 port,
		DBPath:               dbPath,
		RedisURL:             redisURL,
		CommandPrefix:        prefix,
		BotPresence:          botPresence,
		BotPresenceType:      botPresenceType,
		ExternalURL:          extURL,
		DiscordInvite:        os.Getenv("DISCORD_INVITE"),
		BotIdempotencyEnvKey: os.Getenv("BOT_IDEMPOTENCY_ENV_KEY"),
		LogLevel:             logLevel,
		LogFormat:            logFormat,
		MaxSockets:           maxSockets,
	}, nil
}

// IsIdempotent checks BOT_IDEMPOTENCY_ENV_KEY formatting "ENVNAME=expected"
func (c *Config) IsIdempotent() bool {
	if c.BotIdempotencyEnvKey == "" {
		return true
	}
	parts := strings.SplitN(c.BotIdempotencyEnvKey, "=", 2)
	if len(parts) != 2 {
		return false
	}
	return os.Getenv(parts[0]) == parts[1]
}
