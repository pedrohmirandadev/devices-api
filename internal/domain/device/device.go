// Package device holds the Device aggregate, its invariants and the ports
// (interfaces) the domain needs from the outside world.
package device

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const maxTextLength = 255

// Device is the aggregate root. Its fields are unexported so that every
// mutation goes through methods that enforce the domain rules.
type Device struct {
	id        uuid.UUID
	name      string
	brand     string
	state     State
	createdAt time.Time
	updatedAt time.Time
	version   int64
}

// New creates a valid device. An empty state defaults to "available".
func New(name, brand string, state State, now time.Time) (*Device, error) {
	if state == "" {
		state = StateAvailable
	}

	name, brand = strings.TrimSpace(name), strings.TrimSpace(brand)
	if err := validate(name, brand, state); err != nil {
		return nil, err
	}

	now = normalizeTime(now)

	return &Device{
		id:        uuid.Must(uuid.NewV7()),
		name:      name,
		brand:     brand,
		state:     state,
		createdAt: now,
		updatedAt: now,
		version:   1,
	}, nil
}

// Restore rebuilds a device from persisted data without re-running validation.
// It must only be used by repositories.
func Restore(id uuid.UUID, name, brand string, state State, createdAt, updatedAt time.Time, version int64) *Device {
	return &Device{
		id:        id,
		name:      name,
		brand:     brand,
		state:     state,
		createdAt: createdAt.UTC(),
		updatedAt: updatedAt.UTC(),
		version:   version,
	}
}

// Changes describes a full or partial update. Nil fields are left unchanged.
type Changes struct {
	Name  *string
	Brand *string
	State *State
	// CreatedAt may be echoed back by clients (e.g. GET-modify-PUT), but it
	// must match the stored value: the creation time can never be updated.
	CreatedAt *time.Time
}

// Update applies the changes while enforcing the domain rules:
//   - the creation time cannot be updated;
//   - name and brand cannot change while the device is currently in use;
//   - all resulting values must be valid.
//
// It reports whether anything changed and, if the state changed, the previous
// state so the caller can emit a StateChanged event once the change is persisted.
func (d *Device) Update(c Changes, now time.Time) (changed bool, previous State, err error) {
	if c.CreatedAt != nil && !c.CreatedAt.Equal(d.createdAt) {
		return false, "", ErrCreationTimeImmutable
	}

	name, brand, state := d.name, d.brand, d.state
	if c.Name != nil {
		name = strings.TrimSpace(*c.Name)
	}
	if c.Brand != nil {
		brand = strings.TrimSpace(*c.Brand)
	}
	if c.State != nil {
		state = *c.State
	}

	if err := validate(name, brand, state); err != nil {
		return false, "", err
	}

	if d.state == StateInUse && (name != d.name || brand != d.brand) {
		return false, "", ErrLockedWhileInUse
	}

	if name == d.name && brand == d.brand && state == d.state {
		return false, d.state, nil
	}

	previous = d.state
	d.name, d.brand, d.state = name, brand, state
	d.updatedAt = normalizeTime(now)

	return true, previous, nil
}

// EnsureDeletable returns an error if the device cannot be deleted.
func (d *Device) EnsureDeletable() error {
	if d.state == StateInUse {
		return ErrDeleteInUse
	}

	return nil
}

// ID returns the device identifier.
func (d *Device) ID() uuid.UUID { return d.id }

// Name returns the device name.
func (d *Device) Name() string { return d.name }

// Brand returns the device brand.
func (d *Device) Brand() string { return d.brand }

// State returns the device state.
func (d *Device) State() State { return d.state }

// CreatedAt returns the (immutable) creation time.
func (d *Device) CreatedAt() time.Time { return d.createdAt }

// UpdatedAt returns the last modification time.
func (d *Device) UpdatedAt() time.Time { return d.updatedAt }

// Version returns the optimistic-locking version.
func (d *Device) Version() int64 { return d.version }

// normalizeTime stores times in UTC with microsecond precision, the precision of
// PostgreSQL timestamps, so values read back from storage compare equal.
func normalizeTime(t time.Time) time.Time {
	return t.UTC().Truncate(time.Microsecond)
}

func validate(name, brand string, state State) error {
	if !validText(name) {
		return ErrInvalidName
	}
	if !validText(brand) {
		return ErrInvalidBrand
	}
	if _, err := ParseState(string(state)); err != nil {
		return err
	}

	return nil
}

func validText(s string) bool {
	return s != "" && utf8.RuneCountInString(s) <= maxTextLength
}
