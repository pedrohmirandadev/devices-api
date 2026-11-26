// Package device implements the application use cases for devices. It
// orchestrates the domain aggregate, the repository and the event publisher.
package device

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/pedrohmirandadev/devices-api/internal/domain/device"
)

// Pagination limits for listings.
const (
	DefaultLimit = 20
	MaxLimit     = 100
)

// ErrInvalidPagination is returned when limit or offset are out of range.
var ErrInvalidPagination = errors.New("limit must be between 1 and 100 and offset must not be negative")

// CreateInput is the input of the Create use case.
type CreateInput struct {
	Name  string
	Brand string
	State string
}

// UpdateInput is the input of the Update use case. Nil fields are not changed,
// which lets a single use case serve both full (PUT) and partial (PATCH) updates.
type UpdateInput struct {
	Name  *string
	Brand *string
	State *string
	// ExpectedVersion, when set, must match the current version (If-Match).
	ExpectedVersion *int64
}

// ListInput is the input of the List use case. Empty filters are not applied.
type ListInput struct {
	Brand  string
	State  string
	Limit  int
	Offset int
}

// ListOutput is a page of devices.
type ListOutput struct {
	Items  []*device.Device
	Total  int
	Limit  int
	Offset int
}

// Service implements the device use cases.
type Service struct {
	repo      device.Repository
	publisher device.EventPublisher
	now       func() time.Time
}

// NewService builds a Service. now defaults to time.Now when nil.
func NewService(repo device.Repository, publisher device.EventPublisher, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}

	return &Service{repo: repo, publisher: publisher, now: now}
}

// Create registers a new device.
func (s *Service) Create(ctx context.Context, in CreateInput) (*device.Device, error) {
	d, err := device.New(in.Name, in.Brand, device.State(in.State), s.now())
	if err != nil {
		return nil, err
	}

	if err := s.repo.Create(ctx, d); err != nil {
		return nil, err
	}

	return d, nil
}

// Get returns a single device.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*device.Device, error) {
	return s.repo.Get(ctx, id)
}

// List returns a page of devices, optionally filtered by brand and/or state.
func (s *Service) List(ctx context.Context, in ListInput) (ListOutput, error) {
	if in.Limit == 0 {
		in.Limit = DefaultLimit
	}
	if in.Limit < 1 || in.Limit > MaxLimit || in.Offset < 0 {
		return ListOutput{}, ErrInvalidPagination
	}

	f := device.Filter{Limit: in.Limit, Offset: in.Offset}
	if in.Brand != "" {
		f.Brand = &in.Brand
	}
	if in.State != "" {
		st, err := device.ParseState(in.State)
		if err != nil {
			return ListOutput{}, err
		}
		f.State = &st
	}

	items, total, err := s.repo.List(ctx, f)
	if err != nil {
		return ListOutput{}, err
	}

	return ListOutput{Items: items, Total: total, Limit: in.Limit, Offset: in.Offset}, nil
}

// Update fully or partially updates a device. A StateChanged event is published
// only after the new state has been successfully persisted.
func (s *Service) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (*device.Device, error) {
	d, err := s.load(ctx, id, in.ExpectedVersion)
	if err != nil {
		return nil, err
	}

	changes := device.Changes{Name: in.Name, Brand: in.Brand}
	if in.State != nil {
		st := device.State(*in.State)
		changes.State = &st
	}

	changed, previous, err := d.Update(changes, s.now())
	if err != nil {
		return nil, err
	}
	if !changed {
		return d, nil
	}

	saved, err := s.repo.Update(ctx, d)
	if err != nil {
		return nil, err
	}

	if saved.State() != previous {
		s.publisher.Publish(ctx, device.StateChanged{
			DeviceID:      saved.ID(),
			PreviousState: previous,
			NewState:      saved.State(),
			OccurredAt:    saved.UpdatedAt(),
		})
	}

	return saved, nil
}

// Delete removes a device unless it is in use.
func (s *Service) Delete(ctx context.Context, id uuid.UUID, expectedVersion *int64) error {
	d, err := s.load(ctx, id, expectedVersion)
	if err != nil {
		return err
	}

	if err := d.EnsureDeletable(); err != nil {
		return err
	}

	// The version check makes the delete atomic with the in-use check above.
	return s.repo.Delete(ctx, id, d.Version())
}

func (s *Service) load(ctx context.Context, id uuid.UUID, expectedVersion *int64) (*device.Device, error) {
	d, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	if expectedVersion != nil && *expectedVersion != d.Version() {
		return nil, device.ErrVersionMismatch
	}

	return d, nil
}
