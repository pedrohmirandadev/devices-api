package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	appdevice "github.com/pedrohmirandadev/devices-api/internal/application/device"
	"github.com/pedrohmirandadev/devices-api/internal/domain/device"
)

const maxBodyBytes = 1 << 20 // 1 MiB

// DeviceService is the application port consumed by the HTTP adapter.
type DeviceService interface {
	Create(ctx context.Context, in appdevice.CreateInput) (*device.Device, error)
	Get(ctx context.Context, id uuid.UUID) (*device.Device, error)
	List(ctx context.Context, in appdevice.ListInput) (appdevice.ListOutput, error)
	Update(ctx context.Context, id uuid.UUID, in appdevice.UpdateInput) (*device.Device, error)
	Delete(ctx context.Context, id uuid.UUID, expectedVersion *int64) error
}

type deviceHandler struct {
	svc    DeviceService
	logger *slog.Logger
}

func (h *deviceHandler) routes(r chi.Router) {
	r.Post("/", h.create)
	r.Get("/", h.list)
	r.Get("/{id}", h.get)
	r.Put("/{id}", h.replace)
	r.Patch("/{id}", h.update)
	r.Delete("/{id}", h.delete)
}

func (h *deviceHandler) create(w http.ResponseWriter, r *http.Request) {
	var req createDeviceRequest
	if err := decodeJSON(w, r, &req); err != nil {
		h.fail(w, r, err)

		return
	}

	d, err := h.svc.Create(r.Context(), appdevice.CreateInput(req))
	if err != nil {
		h.fail(w, r, err)

		return
	}

	w.Header().Set("Location", "/api/v1/devices/"+d.ID().String())
	writeDevice(w, http.StatusCreated, d)
}

func (h *deviceHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	d, err := h.svc.Get(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeDevice(w, http.StatusOK, d)
}

func (h *deviceHandler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	limit, err := queryInt(q.Get("limit"), "limit")
	if err != nil {
		h.fail(w, r, err)

		return
	}

	offset, err := queryInt(q.Get("offset"), "offset")
	if err != nil {
		h.fail(w, r, err)

		return
	}

	out, err := h.svc.List(r.Context(), appdevice.ListInput{
		Brand: strings.TrimSpace(q.Get("brand")), State: q.Get("state"), Limit: limit, Offset: offset,
	})
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeJSON(w, http.StatusOK, toListResponse(out))
}

func (h *deviceHandler) replace(w http.ResponseWriter, r *http.Request) { h.applyUpdate(w, r, true) }

func (h *deviceHandler) update(w http.ResponseWriter, r *http.Request) { h.applyUpdate(w, r, false) }

func (h *deviceHandler) applyUpdate(w http.ResponseWriter, r *http.Request, full bool) {
	id, version, err := targetOf(r)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	var req updateDeviceRequest
	if err := decodeJSON(w, r, &req); err != nil {
		h.fail(w, r, err)

		return
	}

	if full && !req.complete() {
		h.fail(w, r, errFullUpdateIncomplete)

		return
	}

	d, err := h.svc.Update(r.Context(), id, req.toInput(version))
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeDevice(w, http.StatusOK, d)
}

func (h *deviceHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, version, err := targetOf(r)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	if err := h.svc.Delete(r.Context(), id, version); err != nil {
		h.fail(w, r, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *deviceHandler) fail(w http.ResponseWriter, r *http.Request, err error) {
	writeError(w, r, h.logger, err)
}

func writeDevice(w http.ResponseWriter, status int, d *device.Device) {
	w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(d.Version(), 10)))
	writeJSON(w, status, toDeviceResponse(d))
}

// targetOf extracts the device id and the optional If-Match version.
func targetOf(r *http.Request) (uuid.UUID, *int64, error) {
	id, err := pathID(r)
	if err != nil {
		return uuid.Nil, nil, err
	}

	version, err := ifMatchVersion(r.Header.Get("If-Match"))

	return id, version, err
}

func pathID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, badRequest("id must be a valid UUID")
	}

	return id, nil
}

// ifMatchVersion parses an If-Match header holding a device version. It accepts
// "3", 3 and W/"3"; an empty header or "*" means no precondition.
func ifMatchVersion(header string) (*int64, error) {
	header = strings.TrimSpace(header)
	if header == "" || header == "*" {
		return nil, nil
	}

	raw := strings.Trim(strings.TrimPrefix(header, "W/"), `"`)

	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, badRequest("If-Match must hold a device version, e.g. \"3\"")
	}

	return &v, nil
}

func queryInt(raw, name string) (int, error) {
	if raw == "" {
		return 0, nil
	}

	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, badRequest(name + " must be an integer")
	}

	return n, nil
}

// decodeJSON strictly decodes a single JSON object from the request body.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return badRequest("request body must not be empty")
		}

		return badRequest(fmt.Sprintf("invalid JSON body: %v", err))
	}

	if dec.More() {
		return badRequest("request body must contain a single JSON object")
	}

	return nil
}
