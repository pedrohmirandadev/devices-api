// Package httpapi is the HTTP adapter (REST + WebSocket) of the service.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/pedrohmirandadev/devices-api/api"
)

// Pinger reports whether a dependency (the database) is reachable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Dependencies wires the router to the rest of the application.
type Dependencies struct {
	Service        DeviceService
	Subscriber     Subscriber
	DB             Pinger
	Logger         *slog.Logger
	Registry       *prometheus.Registry
	RequestTimeout time.Duration
	// PingInterval is the WebSocket keep-alive interval (default 30s).
	PingInterval time.Duration
}

const (
	defaultPingInterval = 30 * time.Second
	readinessTimeout    = 2 * time.Second
	swaggerUIVersion    = "5.17.14"
)

// NewRouter builds the HTTP handler of the service.
func NewRouter(d Dependencies) http.Handler {
	if d.PingInterval == 0 {
		d.PingInterval = defaultPingInterval
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID, requestLogger(d.Logger), newHTTPMetrics(d.Registry).middleware, middleware.Recoverer)

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, http.StatusNotFound, "route not found")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, http.StatusMethodNotAllowed, "method not allowed")
	})

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/readyz", readiness(d.DB))
	r.Handle("/metrics", promhttp.HandlerFor(d.Registry, promhttp.HandlerOpts{}))
	r.Get("/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(api.OpenAPISpec)
	})
	r.Get("/docs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(swaggerUIPage))
	})

	// Long-lived connection: deliberately outside the request timeout.
	r.Method(http.MethodGet, "/ws/devices/state-changes", &stateChangesHandler{
		sub:          d.Subscriber,
		logger:       d.Logger,
		pingInterval: d.PingInterval,
		writeTimeout: d.RequestTimeout,
	})

	devices := &deviceHandler{svc: d.Service, logger: d.Logger}
	r.Route("/api/v1/devices", func(r chi.Router) {
		r.Use(middleware.Timeout(d.RequestTimeout))
		devices.routes(r)
	})

	return r
}

func readiness(db Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
		defer cancel()

		if err := db.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})

			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

const swaggerUIPage = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Devices API</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@` + swaggerUIVersion + `/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@` + swaggerUIVersion + `/swagger-ui-bundle.js" crossorigin></script>
  <script>
    window.onload = () => { window.ui = SwaggerUIBundle({ url: '/openapi.yaml', dom_id: '#swagger-ui' }); };
  </script>
</body>
</html>`
