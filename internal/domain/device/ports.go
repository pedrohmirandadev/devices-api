package device

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Filter narrows down a device listing. Nil fields are not applied.
type Filter struct {
	Brand  *string
	State  *State
	Limit  int
	Offset int
}

// Repository is the persistence port for the Device aggregate.
type Repository interface {
	// Create persists a new device.
	Create(ctx context.Context, d *Device) error
	// Get returns the device with the given id or ErrNotFound.
	Get(ctx context.Context, id uuid.UUID) (*Device, error)
	// List returns a page of devices matching the filter and the total number of matches.
	List(ctx context.Context, f Filter) ([]*Device, int, error)
	// Update persists the device if its stored version still equals d.Version().
	// It returns the stored device (with the incremented version), ErrNotFound or
	// ErrConcurrentModification.
	Update(ctx context.Context, d *Device) (*Device, error)
	// Delete removes the device if its stored version still equals version.
	// It returns ErrNotFound or ErrConcurrentModification.
	Delete(ctx context.Context, id uuid.UUID, version int64) error
}

// StateChanged is the domain event raised after a state change is persisted.
type StateChanged struct {
	DeviceID      uuid.UUID
	PreviousState State
	NewState      State
	OccurredAt    time.Time
}

// EventPublisher delivers domain events to interested subscribers.
type EventPublisher interface {
	Publish(ctx context.Context, e StateChanged)
}
