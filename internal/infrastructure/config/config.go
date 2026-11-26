// Package config loads the application configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"
)

// Config holds every runtime setting of the service.
type Config struct {
	HTTPAddr        string
	DatabaseURL     string
	DBMaxConns      int32
	AutoMigrate     bool
	LogLevel        slog.Level
	RequestTimeout  time.Duration
	ShutdownTimeout time.Duration
	EventBuffer     int
}

// Load reads the configuration through getenv (usually os.Getenv), applying
// defaults for unset variables. All invalid values are reported at once.
func Load(getenv func(string) string) (Config, error) {
	p := parser{getenv: getenv}

	cfg := Config{
		HTTPAddr:        p.str("HTTP_ADDR", ":8080"),
		DatabaseURL:     p.str("DATABASE_URL", ""),
		DBMaxConns:      int32(p.positiveInt("DB_MAX_CONNS", 10)), //nolint:gosec // bounded by positiveInt
		AutoMigrate:     p.boolean("DB_AUTO_MIGRATE", true),
		LogLevel:        p.level("LOG_LEVEL", slog.LevelInfo),
		RequestTimeout:  p.duration("REQUEST_TIMEOUT", 10*time.Second),
		ShutdownTimeout: p.duration("SHUTDOWN_TIMEOUT", 15*time.Second),
		EventBuffer:     p.positiveInt("EVENT_BUFFER", 64),
	}

	if cfg.DatabaseURL == "" {
		p.errs = append(p.errs, errors.New("DATABASE_URL is required"))
	}

	return cfg, errors.Join(p.errs...)
}

const maxInt = 1 << 20

type parser struct {
	getenv func(string) string
	errs   []error
}

func (p *parser) str(key, def string) string {
	if v := p.getenv(key); v != "" {
		return v
	}

	return def
}

func (p *parser) positiveInt(key string, def int) int {
	v := p.getenv(key)
	if v == "" {
		return def
	}

	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > maxInt {
		p.fail(key, v, fmt.Sprintf("an integer between 1 and %d", maxInt))

		return def
	}

	return n
}

func (p *parser) boolean(key string, def bool) bool {
	v := p.getenv(key)
	if v == "" {
		return def
	}

	b, err := strconv.ParseBool(v)
	if err != nil {
		p.fail(key, v, "a boolean")

		return def
	}

	return b
}

func (p *parser) duration(key string, def time.Duration) time.Duration {
	v := p.getenv(key)
	if v == "" {
		return def
	}

	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		p.fail(key, v, "a positive duration such as 10s")

		return def
	}

	return d
}

func (p *parser) level(key string, def slog.Level) slog.Level {
	v := p.getenv(key)
	if v == "" {
		return def
	}

	var l slog.Level
	if err := l.UnmarshalText([]byte(v)); err != nil {
		p.fail(key, v, "one of debug, info, warn, error")

		return def
	}

	return l
}

func (p *parser) fail(key, value, want string) {
	p.errs = append(p.errs, fmt.Errorf("%s=%q is invalid: must be %s", key, value, want))
}
