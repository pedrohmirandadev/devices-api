package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pedrohmirandadev/devices-api/internal/domain/device"
)

func dialStateChanges(t *testing.T, e *env) (*websocket.Conn, context.Context) {
	t.Helper()
	srv := httptest.NewServer(e.handler)
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/devices/state-changes"
	conn, resp, err := websocket.Dial(ctx, url, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	t.Cleanup(func() { conn.CloseNow() })

	return conn, ctx
}

func TestStateChangesStreamsEvents(t *testing.T) {
	e := newEnv(t)
	conn, ctx := dialStateChanges(t, e)

	id := uuid.New()
	at := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)
	e.sub.ch <- device.StateChanged{DeviceID: id, PreviousState: device.StateAvailable, NewState: device.StateInUse, OccurredAt: at}

	var msg stateChangedMessage
	require.NoError(t, wsjson.Read(ctx, conn, &msg))
	assert.Equal(t, stateChangedMessage{
		Type: "device.state_changed", DeviceID: id, PreviousState: "available", NewState: "in-use", OccurredAt: at,
	}, msg)

	// Client disconnect unsubscribes.
	require.NoError(t, conn.Close(websocket.StatusNormalClosure, "bye"))
	select {
	case <-e.sub.canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("subscription was not canceled after the client disconnected")
	}
}

func TestStateChangesClosesOnShutdown(t *testing.T) {
	e := newEnv(t)
	conn, ctx := dialStateChanges(t, e)

	close(e.sub.ch) // broker closed

	_, _, err := conn.Read(ctx)
	assert.Equal(t, websocket.StatusGoingAway, websocket.CloseStatus(err))
}

func TestStateChangesKeepAlive(t *testing.T) {
	e := newEnv(t, func(d *Dependencies) {
		d.PingInterval = 10 * time.Millisecond
		d.RequestTimeout = 50 * time.Millisecond
	})
	conn, _ := dialStateChanges(t, e)

	// While the client reads, pings are answered and the connection stays open.
	readCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _, err := conn.Read(readCtx)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	// Once the client stops reading, pongs stop and the server drops the connection.
	select {
	case <-e.sub.canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("server did not drop an unresponsive client")
	}
}

func TestStateChangesRejectsPlainHTTP(t *testing.T) {
	rec := newEnv(t).do(t, http.MethodGet, "/ws/devices/state-changes", "")

	assert.Equal(t, http.StatusUpgradeRequired, rec.Code)
}
