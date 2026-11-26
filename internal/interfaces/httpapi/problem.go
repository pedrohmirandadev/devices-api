package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	appdevice "github.com/pedrohmirandadev/devices-api/internal/application/device"
	"github.com/pedrohmirandadev/devices-api/internal/domain/device"
)

// problem is an RFC 7807 problem details body.
type problem struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
}

// badRequestError marks malformed requests (syntax rather than semantics).
type badRequestError struct{ msg string }

func (e badRequestError) Error() string { return e.msg }

func badRequest(msg string) error { return badRequestError{msg: msg} }

// errFullUpdateIncomplete is returned when a PUT body misses a required field.
var errFullUpdateIncomplete = errors.New("name, brand and state are required for a full update")

// statusFor maps application and domain errors to HTTP status codes.
func statusFor(err error) int {
	var br badRequestError

	switch {
	case errors.As(err, &br):
		return http.StatusBadRequest
	case errors.Is(err, device.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, device.ErrInvalidName),
		errors.Is(err, device.ErrInvalidBrand),
		errors.Is(err, device.ErrInvalidState),
		errors.Is(err, device.ErrCreationTimeImmutable),
		errors.Is(err, appdevice.ErrInvalidPagination),
		errors.Is(err, errFullUpdateIncomplete):
		return http.StatusUnprocessableEntity
	case errors.Is(err, device.ErrLockedWhileInUse),
		errors.Is(err, device.ErrDeleteInUse),
		errors.Is(err, device.ErrConcurrentModification):
		return http.StatusConflict
	case errors.Is(err, device.ErrVersionMismatch):
		return http.StatusPreconditionFailed
	default:
		return http.StatusInternalServerError
	}
}

// writeError writes err as a problem response. Unexpected errors are logged and
// their details hidden from the client.
func writeError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	status := statusFor(err)
	detail := err.Error()

	if status == http.StatusInternalServerError {
		logger.ErrorContext(r.Context(), "request failed", slog.String("error", err.Error()))
		detail = "an unexpected error occurred"
	}

	writeProblem(w, r, status, detail)
}

func writeProblem(w http.ResponseWriter, r *http.Request, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem{
		Type:     "about:blank",
		Title:    http.StatusText(status),
		Status:   status,
		Detail:   detail,
		Instance: r.URL.Path,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
