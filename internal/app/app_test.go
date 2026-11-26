package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pedrohmirandadev/devices-api/internal/app"
	"github.com/pedrohmirandadev/devices-api/internal/testutil/pgtest"
)

var dsn string

func TestMain(m *testing.M) {
	pg, err := pgtest.Start(context.Background(), pgtest.DefaultImage)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	dsn = pg.DSN
	code := m.Run()
	_ = pg.Close()
	os.Exit(code)
}

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	return addr
}

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// startApp runs the service and returns its base URL and a stop function that
// returns Run's result.
func startApp(t *testing.T, logs io.Writer) (string, func() error) {
	t.Helper()
	addr := freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx, envOf(map[string]string{
			"DATABASE_URL": dsn, "HTTP_ADDR": addr, "LOG_LEVEL": "debug",
		}), logs)
	}()

	base := "http://" + addr
	require.Eventually(t, func() bool {
		resp, err := http.Get(base + "/readyz")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()

		return resp.StatusCode == http.StatusOK
	}, 10*time.Second, 50*time.Millisecond, "service did not become ready")

	return base, func() error {
		cancel()

		return <-done
	}
}

type apiClient struct {
	t    *testing.T
	base string
}

func (c apiClient) call(method, path, body string, headers ...string) (int, http.Header, map[string]any) {
	c.t.Helper()
	req, err := http.NewRequest(method, c.base+path, strings.NewReader(body))
	require.NoError(c.t, err)
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(c.t, err)
	defer resp.Body.Close()

	var out map[string]any
	if resp.StatusCode != http.StatusNoContent {
		require.NoError(c.t, json.NewDecoder(resp.Body).Decode(&out))
	}

	return resp.StatusCode, resp.Header, out
}

func TestEndToEnd(t *testing.T) {
	logs := &syncBuffer{}
	base, stop := startApp(t, logs)
	c := apiClient{t: t, base: base}

	// Subscribe to state change notifications.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/ws/devices/state-changes", nil)
	require.NoError(t, err)
	defer ws.CloseNow()

	// Metrics expose the connected subscriber.
	require.Eventually(t, func() bool {
		resp, err := http.Get(base + "/metrics")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)

		return strings.Contains(string(raw), "devices_state_change_subscribers 1")
	}, 5*time.Second, 20*time.Millisecond)

	// Unique brands keep the test independent of data left by other runs.
	apple, google := "Apple-"+uuid.NewString(), "Google-"+uuid.NewString()

	// Create.
	status, hdr, dev := c.call(http.MethodPost, "/api/v1/devices", fmt.Sprintf(`{"name":"iPhone 16","brand":%q}`, apple))
	require.Equal(t, http.StatusCreated, status, dev)
	id := dev["id"].(string)
	assert.Equal(t, "available", dev["state"])
	assert.Equal(t, "/api/v1/devices/"+id, hdr.Get("Location"))
	assert.Equal(t, `"1"`, hdr.Get("ETag"))

	status, _, _ = c.call(http.MethodPost, "/api/v1/devices", fmt.Sprintf(`{"name":"Pixel 9","brand":%q,"state":"inactive"}`, google))
	require.Equal(t, http.StatusCreated, status)

	// Fetch one, list, filter.
	status, _, got := c.call(http.MethodGet, "/api/v1/devices/"+id, "")
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, dev, got)

	_, _, list := c.call(http.MethodGet, "/api/v1/devices?limit=100", "")
	assert.GreaterOrEqual(t, list["total"], float64(2))

	_, _, list = c.call(http.MethodGet, "/api/v1/devices?brand="+strings.ToLower(apple), "")
	assert.EqualValues(t, 1, list["total"], "brand filter is case-insensitive")

	_, _, list = c.call(http.MethodGet, "/api/v1/devices?state=inactive&brand="+google, "")
	assert.EqualValues(t, 1, list["total"])

	_, _, list = c.call(http.MethodGet, "/api/v1/devices?state=in-use&brand="+google, "")
	assert.EqualValues(t, 0, list["total"])

	// State change is persisted, then notified.
	status, hdr, dev = c.call(http.MethodPatch, "/api/v1/devices/"+id, `{"state":"in-use"}`, "If-Match", `"1"`)
	require.Equal(t, http.StatusOK, status, dev)
	assert.Equal(t, `"2"`, hdr.Get("ETag"))

	expectEvent := func(previous, current string) {
		t.Helper()
		var event map[string]any
		require.NoError(t, wsjson.Read(ctx, ws, &event))
		assert.Equal(t, "device.state_changed", event["type"])
		assert.Equal(t, id, event["device_id"])
		assert.Equal(t, previous, event["previous_state"])
		assert.Equal(t, current, event["new_state"])
	}
	expectEvent("available", "in-use")

	// Domain rules.
	status, _, _ = c.call(http.MethodPatch, "/api/v1/devices/"+id, `{"name":"Other"}`)
	assert.Equal(t, http.StatusConflict, status, "name is locked while in use")

	status, _, _ = c.call(http.MethodDelete, "/api/v1/devices/"+id, "")
	assert.Equal(t, http.StatusConflict, status, "in-use devices cannot be deleted")

	status, _, _ = c.call(http.MethodPatch, "/api/v1/devices/"+id, `{"created_at":"2000-01-01T00:00:00Z"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, status, "creation time is immutable")

	status, _, _ = c.call(http.MethodPatch, "/api/v1/devices/"+id, `{"state":"available"}`, "If-Match", `"1"`)
	assert.Equal(t, http.StatusPreconditionFailed, status, "stale If-Match")

	// Full update (echoing created_at back is allowed), then delete.
	body := fmt.Sprintf(`{"name":"iPhone 16","brand":%q,"state":"available","created_at":%q}`, apple, dev["created_at"])
	status, _, dev = c.call(http.MethodPut, "/api/v1/devices/"+id, body)
	require.Equal(t, http.StatusOK, status, dev)
	expectEvent("in-use", "available")

	status, _, _ = c.call(http.MethodDelete, "/api/v1/devices/"+id, "")
	assert.Equal(t, http.StatusNoContent, status)

	status, _, _ = c.call(http.MethodGet, "/api/v1/devices/"+id, "")
	assert.Equal(t, http.StatusNotFound, status)

	// Graceful shutdown disconnects subscribers.
	require.NoError(t, stop())
	_, _, err = ws.Read(ctx)
	assert.Equal(t, websocket.StatusGoingAway, websocket.CloseStatus(err))
	assert.Contains(t, logs.String(), "shutting down")
}

func TestRunErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("invalid configuration", func(t *testing.T) {
		err := app.Run(ctx, envOf(map[string]string{}), io.Discard)
		require.ErrorContains(t, err, "load config")
	})

	t.Run("migration failure", func(t *testing.T) {
		err := app.Run(ctx, envOf(map[string]string{"DATABASE_URL": "postgres://u:p@127.0.0.1:1/db?connect_timeout=1"}), io.Discard)
		require.ErrorContains(t, err, "migrations")
	})

	t.Run("database unreachable", func(t *testing.T) {
		err := app.Run(ctx, envOf(map[string]string{
			"DATABASE_URL": "postgres://u:p@127.0.0.1:1/db?connect_timeout=1", "DB_AUTO_MIGRATE": "false",
		}), io.Discard)
		require.ErrorContains(t, err, "ping database")
	})

	t.Run("address in use", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer ln.Close()

		err = app.Run(ctx, envOf(map[string]string{"DATABASE_URL": dsn, "HTTP_ADDR": ln.Addr().String()}), io.Discard)
		require.ErrorContains(t, err, "listen on")
	})
}
