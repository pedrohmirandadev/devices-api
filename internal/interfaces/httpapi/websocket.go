package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/pedrohmirandadev/devices-api/internal/domain/device"
)

// Subscriber gives access to the stream of state change events.
type Subscriber interface {
	Subscribe() (events <-chan device.StateChanged, cancel func())
}

// stateChangesHandler streams StateChanged events to WebSocket clients.
type stateChangesHandler struct {
	sub          Subscriber
	logger       *slog.Logger
	pingInterval time.Duration
	writeTimeout time.Duration
}

func (h *stateChangesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept has already written the HTTP error response.
	}
	defer conn.CloseNow()

	events, cancel := h.sub.Subscribe()
	defer cancel()

	// Client messages are ignored; CloseRead handles control frames and cancels
	// ctx when the client goes away.
	ctx := conn.CloseRead(r.Context())

	ping := time.NewTicker(h.pingInterval)
	defer ping.Stop()

	h.logger.DebugContext(ctx, "websocket subscriber connected")

	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			if err := h.withTimeout(ctx, conn.Ping); err != nil {
				return
			}
		case e, ok := <-events:
			if !ok {
				_ = conn.Close(websocket.StatusGoingAway, "server shutting down")

				return
			}

			err := h.withTimeout(ctx, func(ctx context.Context) error {
				return wsjson.Write(ctx, conn, toStateChangedMessage(e))
			})
			if err != nil {
				return
			}
		}
	}
}

func (h *stateChangesHandler) withTimeout(ctx context.Context, fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, h.writeTimeout)
	defer cancel()

	return fn(ctx)
}
