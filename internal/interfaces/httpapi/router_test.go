package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	appdevice "github.com/pedrohmirandadev/devices-api/internal/application/device"
	"github.com/pedrohmirandadev/devices-api/internal/domain/device"
)

var (
	errBoom = errors.New("database exploded")
	created = time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)
)

func ptr[T any](v T) *T { return &v }

// fakeService records calls and returns canned results.
type fakeService struct {
	createFn func(appdevice.CreateInput) (*device.Device, error)
	getFn    func(uuid.UUID) (*device.Device, error)
	listFn   func(appdevice.ListInput) (appdevice.ListOutput, error)
	updateFn func(uuid.UUID, appdevice.UpdateInput) (*device.Device, error)
	deleteFn func(uuid.UUID, *int64) error
}

func (f *fakeService) Create(_ context.Context, in appdevice.CreateInput) (*device.Device, error) {
	return f.createFn(in)
}

func (f *fakeService) Get(_ context.Context, id uuid.UUID) (*device.Device, error) {
	return f.getFn(id)
}

func (f *fakeService) List(_ context.Context, in appdevice.ListInput) (appdevice.ListOutput, error) {
	return f.listFn(in)
}

func (f *fakeService) Update(_ context.Context, id uuid.UUID, in appdevice.UpdateInput) (*device.Device, error) {
	return f.updateFn(id, in)
}

func (f *fakeService) Delete(_ context.Context, id uuid.UUID, v *int64) error {
	return f.deleteFn(id, v)
}

type fakePinger struct{ err error }

func (p fakePinger) Ping(context.Context) error { return p.err }

type fakeSubscriber struct {
	ch       chan device.StateChanged
	canceled chan struct{}
}

func newFakeSubscriber() *fakeSubscriber {
	return &fakeSubscriber{ch: make(chan device.StateChanged, 1), canceled: make(chan struct{})}
}

func (s *fakeSubscriber) Subscribe() (<-chan device.StateChanged, func()) {
	return s.ch, func() { close(s.canceled) }
}

type env struct {
	svc     *fakeService
	sub     *fakeSubscriber
	logs    *bytes.Buffer
	handler http.Handler
}

func newEnv(t *testing.T, opts ...func(*Dependencies)) *env {
	t.Helper()
	e := &env{svc: &fakeService{}, sub: newFakeSubscriber(), logs: &bytes.Buffer{}}
	deps := Dependencies{
		Service:        e.svc,
		Subscriber:     e.sub,
		DB:             fakePinger{},
		Logger:         slog.New(slog.NewJSONHandler(e.logs, nil)),
		Registry:       prometheus.NewRegistry(),
		RequestTimeout: time.Second,
	}
	for _, o := range opts {
		o(&deps)
	}
	e.handler = NewRouter(deps)

	return e
}

func (e *env) do(t *testing.T, method, target, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)

	return rec
}

func sampleDevice(state device.State) *device.Device {
	return device.Restore(uuid.MustParse("0193a4b2-7c1e-7d4a-9f2b-1c2d3e4f5a6b"), "iPhone", "Apple", state, created, created, 3)
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v), rec.Body.String())

	return v
}

func assertProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, detail string) {
	t.Helper()
	require.Equal(t, status, rec.Code, rec.Body.String())
	assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
	p := decode[problem](t, rec)
	assert.Equal(t, status, p.Status)
	assert.Equal(t, http.StatusText(status), p.Title)
	assert.Contains(t, p.Detail, detail)
}

const deviceURL = "/api/v1/devices/0193a4b2-7c1e-7d4a-9f2b-1c2d3e4f5a6b"

func TestCreateDevice(t *testing.T) {
	t.Run("created", func(t *testing.T) {
		e := newEnv(t)
		var got appdevice.CreateInput
		e.svc.createFn = func(in appdevice.CreateInput) (*device.Device, error) {
			got = in

			return sampleDevice(device.StateAvailable), nil
		}

		rec := e.do(t, http.MethodPost, "/api/v1/devices", `{"name":"iPhone","brand":"Apple","state":"available"}`)

		require.Equal(t, http.StatusCreated, rec.Code)
		assert.Equal(t, appdevice.CreateInput{Name: "iPhone", Brand: "Apple", State: "available"}, got)
		assert.Equal(t, deviceURL, rec.Header().Get("Location"))
		assert.Equal(t, `"3"`, rec.Header().Get("ETag"))
		assert.Equal(t, deviceResponse{
			ID: sampleDevice("").ID(), Name: "iPhone", Brand: "Apple", State: "available",
			CreatedAt: created, UpdatedAt: created, Version: 3,
		}, decode[deviceResponse](t, rec))
	})

	malformed := map[string]struct{ body, detail string }{
		"empty body":     {"", "must not be empty"},
		"invalid json":   {"{", "invalid JSON body"},
		"unknown field":  {`{"name":"x","color":"red"}`, "unknown field"},
		"trailing data":  {`{"name":"x"} {}`, "single JSON object"},
		"created_at set": {`{"name":"x","brand":"y","created_at":"2026-01-01T00:00:00Z"}`, "unknown field"},
	}
	for name, tc := range malformed {
		t.Run(name, func(t *testing.T) {
			rec := newEnv(t).do(t, http.MethodPost, "/api/v1/devices", tc.body)
			assertProblem(t, rec, http.StatusBadRequest, tc.detail)
		})
	}

	t.Run("validation error", func(t *testing.T) {
		e := newEnv(t)
		e.svc.createFn = func(appdevice.CreateInput) (*device.Device, error) { return nil, device.ErrInvalidName }

		assertProblem(t, e.do(t, http.MethodPost, "/api/v1/devices", `{}`), http.StatusUnprocessableEntity, "name is required")
	})

	t.Run("unexpected error is hidden and logged", func(t *testing.T) {
		e := newEnv(t)
		e.svc.createFn = func(appdevice.CreateInput) (*device.Device, error) { return nil, errBoom }

		rec := e.do(t, http.MethodPost, "/api/v1/devices", `{}`)

		assertProblem(t, rec, http.StatusInternalServerError, "an unexpected error occurred")
		assert.NotContains(t, rec.Body.String(), errBoom.Error())
		assert.Contains(t, e.logs.String(), errBoom.Error())
	})
}

func TestGetDevice(t *testing.T) {
	e := newEnv(t)
	e.svc.getFn = func(id uuid.UUID) (*device.Device, error) {
		if id == sampleDevice("").ID() {
			return sampleDevice(device.StateInUse), nil
		}

		return nil, device.ErrNotFound
	}

	rec := e.do(t, http.MethodGet, deviceURL, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "in-use", decode[deviceResponse](t, rec).State)
	assert.Equal(t, `"3"`, rec.Header().Get("ETag"))

	assertProblem(t, e.do(t, http.MethodGet, "/api/v1/devices/"+uuid.NewString(), ""), http.StatusNotFound, "device not found")
	assertProblem(t, e.do(t, http.MethodGet, "/api/v1/devices/not-a-uuid", ""), http.StatusBadRequest, "valid UUID")
}

func TestListDevices(t *testing.T) {
	t.Run("passes filters and pagination", func(t *testing.T) {
		e := newEnv(t)
		var got appdevice.ListInput
		e.svc.listFn = func(in appdevice.ListInput) (appdevice.ListOutput, error) {
			got = in

			return appdevice.ListOutput{Items: []*device.Device{sampleDevice(device.StateAvailable)}, Total: 7, Limit: 5, Offset: 2}, nil
		}

		rec := e.do(t, http.MethodGet, "/api/v1/devices?brand=%20Apple%20&state=available&limit=5&offset=2", "")

		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, appdevice.ListInput{Brand: "Apple", State: "available", Limit: 5, Offset: 2}, got)
		body := decode[listResponse](t, rec)
		assert.Len(t, body.Items, 1)
		assert.Equal(t, 7, body.Total)
		assert.Equal(t, 5, body.Limit)
		assert.Equal(t, 2, body.Offset)
	})

	t.Run("empty list is an empty array", func(t *testing.T) {
		e := newEnv(t)
		e.svc.listFn = func(appdevice.ListInput) (appdevice.ListOutput, error) { return appdevice.ListOutput{Limit: 20}, nil }

		rec := e.do(t, http.MethodGet, "/api/v1/devices", "")

		require.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, `{"items":[],"total":0,"limit":20,"offset":0}`, rec.Body.String())
	})

	t.Run("invalid query parameters", func(t *testing.T) {
		e := newEnv(t)
		assertProblem(t, e.do(t, http.MethodGet, "/api/v1/devices?limit=abc", ""), http.StatusBadRequest, "limit must be an integer")
		assertProblem(t, e.do(t, http.MethodGet, "/api/v1/devices?offset=x", ""), http.StatusBadRequest, "offset must be an integer")
	})

	t.Run("service validation error", func(t *testing.T) {
		e := newEnv(t)
		e.svc.listFn = func(appdevice.ListInput) (appdevice.ListOutput, error) {
			return appdevice.ListOutput{}, appdevice.ErrInvalidPagination
		}

		assertProblem(t, e.do(t, http.MethodGet, "/api/v1/devices?limit=500", ""), http.StatusUnprocessableEntity, "limit must be between")
	})
}

func TestUpdateDevice(t *testing.T) {
	t.Run("PUT replaces the device", func(t *testing.T) {
		e := newEnv(t)
		var got appdevice.UpdateInput
		e.svc.updateFn = func(id uuid.UUID, in appdevice.UpdateInput) (*device.Device, error) {
			assert.Equal(t, sampleDevice("").ID(), id)
			got = in

			return sampleDevice(device.StateInUse), nil
		}

		body := `{"id":"ignored","name":"iPhone","brand":"Apple","state":"in-use",
			"created_at":"2026-04-01T09:00:00Z","updated_at":"x","version":1}`
		rec := e.do(t, http.MethodPut, deviceURL, body, "If-Match", `"3"`)

		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, `"3"`, rec.Header().Get("ETag"))
		assert.Equal(t, appdevice.UpdateInput{
			Name: ptr("iPhone"), Brand: ptr("Apple"), State: ptr("in-use"),
			CreatedAt: ptr(created), ExpectedVersion: ptr(int64(3)),
		}, got)
	})

	t.Run("PUT requires every field", func(t *testing.T) {
		rec := newEnv(t).do(t, http.MethodPut, deviceURL, `{"name":"iPhone"}`)
		assertProblem(t, rec, http.StatusUnprocessableEntity, "required for a full update")
	})

	t.Run("PATCH changes only what is sent", func(t *testing.T) {
		e := newEnv(t)
		var got appdevice.UpdateInput
		e.svc.updateFn = func(_ uuid.UUID, in appdevice.UpdateInput) (*device.Device, error) {
			got = in

			return sampleDevice(device.StateInactive), nil
		}

		rec := e.do(t, http.MethodPatch, deviceURL, `{"state":"inactive"}`)

		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, appdevice.UpdateInput{State: ptr("inactive")}, got)
	})

	errorCases := map[string]struct {
		err    error
		status int
	}{
		"in use":           {device.ErrLockedWhileInUse, http.StatusConflict},
		"concurrent write": {device.ErrConcurrentModification, http.StatusConflict},
		"stale version":    {device.ErrVersionMismatch, http.StatusPreconditionFailed},
		"creation time":    {device.ErrCreationTimeImmutable, http.StatusUnprocessableEntity},
		"not found":        {device.ErrNotFound, http.StatusNotFound},
	}
	for name, tc := range errorCases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.svc.updateFn = func(uuid.UUID, appdevice.UpdateInput) (*device.Device, error) { return nil, tc.err }

			assertProblem(t, e.do(t, http.MethodPatch, deviceURL, `{"name":"x"}`), tc.status, tc.err.Error())
		})
	}

	t.Run("malformed requests", func(t *testing.T) {
		e := newEnv(t)
		assertProblem(t, e.do(t, http.MethodPatch, "/api/v1/devices/nope", `{}`), http.StatusBadRequest, "valid UUID")
		assertProblem(t, e.do(t, http.MethodPatch, deviceURL, `{}`, "If-Match", "abc"), http.StatusBadRequest, "If-Match")
		assertProblem(t, e.do(t, http.MethodPatch, deviceURL, `[]`), http.StatusBadRequest, "invalid JSON body")
	})
}

func TestDeleteDevice(t *testing.T) {
	e := newEnv(t)
	var gotVersion *int64
	e.svc.deleteFn = func(_ uuid.UUID, v *int64) error {
		gotVersion = v

		return nil
	}

	rec := e.do(t, http.MethodDelete, deviceURL, "", "If-Match", `W/"4"`)
	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, ptr(int64(4)), gotVersion)

	e.svc.deleteFn = func(uuid.UUID, *int64) error { return device.ErrDeleteInUse }
	assertProblem(t, e.do(t, http.MethodDelete, deviceURL, ""), http.StatusConflict, "cannot be deleted")
	assertProblem(t, e.do(t, http.MethodDelete, "/api/v1/devices/1", ""), http.StatusBadRequest, "valid UUID")
}

func TestIfMatchVersion(t *testing.T) {
	for header, want := range map[string]*int64{"": nil, "*": nil, " 5 ": ptr(int64(5)), `"6"`: ptr(int64(6)), `W/"7"`: ptr(int64(7))} {
		got, err := ifMatchVersion(header)
		require.NoError(t, err, header)
		assert.Equal(t, want, got, header)
	}

	_, err := ifMatchVersion(`"v1"`)
	assert.Error(t, err)
}

func TestOperationalEndpoints(t *testing.T) {
	e := newEnv(t)

	rec := e.do(t, http.MethodGet, "/healthz", "")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"status":"ok"}`, rec.Body.String())

	rec = e.do(t, http.MethodGet, "/readyz", "")
	assert.Equal(t, http.StatusOK, rec.Code)

	rec = e.do(t, http.MethodGet, "/openapi.yaml", "")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/yaml", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Body.String(), "openapi: 3.0.3")

	rec = e.do(t, http.MethodGet, "/docs", "")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "swagger-ui")

	rec = e.do(t, http.MethodGet, "/metrics", "")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `http_requests_total{method="GET",route="/healthz",status="200"} 1`)

	assertProblem(t, e.do(t, http.MethodGet, "/nowhere", ""), http.StatusNotFound, "route not found")
	assertProblem(t, e.do(t, http.MethodPost, "/healthz", ""), http.StatusMethodNotAllowed, "method not allowed")

	rec = e.do(t, http.MethodGet, "/metrics", "")
	assert.Contains(t, rec.Body.String(), `route="unmatched",status="404"`)
}

func TestReadinessFailure(t *testing.T) {
	e := newEnv(t, func(d *Dependencies) { d.DB = fakePinger{err: errBoom} })

	rec := e.do(t, http.MethodGet, "/readyz", "")

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.JSONEq(t, `{"status":"unavailable"}`, rec.Body.String())
}

func TestPanicIsRecovered(t *testing.T) {
	e := newEnv(t)
	e.svc.getFn = func(uuid.UUID) (*device.Device, error) { panic("boom") }

	rec := e.do(t, http.MethodGet, deviceURL, "")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestRequestLogging(t *testing.T) {
	e := newEnv(t)

	e.do(t, http.MethodGet, "/healthz", "")

	var entry map[string]any
	line, _, _ := strings.Cut(e.logs.String(), "\n")
	require.NoError(t, json.Unmarshal([]byte(line), &entry))
	assert.Equal(t, "http request", entry["msg"])
	assert.Equal(t, "/healthz", entry["path"])
	assert.EqualValues(t, 200, entry["status"])
	assert.NotEmpty(t, entry["request_id"])
}

func TestStatusForUnknownError(t *testing.T) {
	assert.Equal(t, http.StatusInternalServerError, statusFor(fmt.Errorf("wrapped: %w", io.ErrUnexpectedEOF)))
	assert.Equal(t, http.StatusNotFound, statusFor(fmt.Errorf("wrapped: %w", device.ErrNotFound)))
}
