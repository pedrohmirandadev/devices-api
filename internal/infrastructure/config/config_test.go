package config_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pedrohmirandadev/devices-api/internal/infrastructure/config"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{"DATABASE_URL": "postgres://x"}))

	require.NoError(t, err)
	assert.Equal(t, config.Config{
		HTTPAddr:        ":8080",
		DatabaseURL:     "postgres://x",
		DBMaxConns:      10,
		AutoMigrate:     true,
		LogLevel:        slog.LevelInfo,
		RequestTimeout:  10 * time.Second,
		ShutdownTimeout: 15 * time.Second,
		EventBuffer:     64,
	}, cfg)
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{
		"HTTP_ADDR":        ":9090",
		"DATABASE_URL":     "postgres://y",
		"DB_MAX_CONNS":     "25",
		"DB_AUTO_MIGRATE":  "false",
		"LOG_LEVEL":        "debug",
		"REQUEST_TIMEOUT":  "3s",
		"SHUTDOWN_TIMEOUT": "1m",
		"EVENT_BUFFER":     "8",
	}))

	require.NoError(t, err)
	assert.Equal(t, config.Config{
		HTTPAddr:        ":9090",
		DatabaseURL:     "postgres://y",
		DBMaxConns:      25,
		AutoMigrate:     false,
		LogLevel:        slog.LevelDebug,
		RequestTimeout:  3 * time.Second,
		ShutdownTimeout: time.Minute,
		EventBuffer:     8,
	}, cfg)
}

func TestLoadReportsAllErrors(t *testing.T) {
	_, err := config.Load(env(map[string]string{
		"DB_MAX_CONNS":     "0",
		"DB_AUTO_MIGRATE":  "maybe",
		"LOG_LEVEL":        "loud",
		"REQUEST_TIMEOUT":  "-1s",
		"SHUTDOWN_TIMEOUT": "soon",
		"EVENT_BUFFER":     "abc",
	}))

	require.Error(t, err)
	for _, want := range []string{
		"DATABASE_URL is required",
		`DB_MAX_CONNS="0"`,
		`DB_AUTO_MIGRATE="maybe"`,
		`LOG_LEVEL="loud"`,
		`REQUEST_TIMEOUT="-1s"`,
		`SHUTDOWN_TIMEOUT="soon"`,
		`EVENT_BUFFER="abc"`,
	} {
		assert.ErrorContains(t, err, want)
	}
}
