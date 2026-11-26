package httpapi

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	appdevice "github.com/pedrohmirandadev/devices-api/internal/application/device"
	"github.com/pedrohmirandadev/devices-api/internal/domain/device"
)

type createDeviceRequest struct {
	Name  string `json:"name"`
	Brand string `json:"brand"`
	State string `json:"state"`
}

// updateDeviceRequest is used by PUT and PATCH. Read-only fields are accepted
// (so clients can send back what they read) but ignored.
type updateDeviceRequest struct {
	Name      *string    `json:"name"`
	Brand     *string    `json:"brand"`
	State     *string    `json:"state"`
	CreatedAt *time.Time `json:"created_at"`

	ID        json.RawMessage `json:"id"`
	UpdatedAt json.RawMessage `json:"updated_at"`
	Version   json.RawMessage `json:"version"`
}

func (r updateDeviceRequest) complete() bool {
	return r.Name != nil && r.Brand != nil && r.State != nil
}

func (r updateDeviceRequest) toInput(expectedVersion *int64) appdevice.UpdateInput {
	return appdevice.UpdateInput{
		Name:            r.Name,
		Brand:           r.Brand,
		State:           r.State,
		CreatedAt:       r.CreatedAt,
		ExpectedVersion: expectedVersion,
	}
}

type deviceResponse struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Brand     string    `json:"brand"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Version   int64     `json:"version"`
}

func toDeviceResponse(d *device.Device) deviceResponse {
	return deviceResponse{
		ID:        d.ID(),
		Name:      d.Name(),
		Brand:     d.Brand(),
		State:     d.State().String(),
		CreatedAt: d.CreatedAt(),
		UpdatedAt: d.UpdatedAt(),
		Version:   d.Version(),
	}
}

type listResponse struct {
	Items  []deviceResponse `json:"items"`
	Total  int              `json:"total"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
}

func toListResponse(out appdevice.ListOutput) listResponse {
	items := make([]deviceResponse, len(out.Items))
	for i, d := range out.Items {
		items[i] = toDeviceResponse(d)
	}

	return listResponse{Items: items, Total: out.Total, Limit: out.Limit, Offset: out.Offset}
}

// stateChangedMessage is the WebSocket notification payload.
type stateChangedMessage struct {
	Type          string    `json:"type"`
	DeviceID      uuid.UUID `json:"device_id"`
	PreviousState string    `json:"previous_state"`
	NewState      string    `json:"new_state"`
	OccurredAt    time.Time `json:"occurred_at"`
}

func toStateChangedMessage(e device.StateChanged) stateChangedMessage {
	return stateChangedMessage{
		Type:          "device.state_changed",
		DeviceID:      e.DeviceID,
		PreviousState: e.PreviousState.String(),
		NewState:      e.NewState.String(),
		OccurredAt:    e.OccurredAt,
	}
}
