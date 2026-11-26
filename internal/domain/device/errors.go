package device

import "errors"

// Validation errors: the input violates an invariant of the Device aggregate.
var (
	ErrInvalidName  = errors.New("name is required and must be at most 255 characters")
	ErrInvalidBrand = errors.New("brand is required and must be at most 255 characters")
	ErrInvalidState = errors.New("state must be one of: available, in-use, inactive")
)

// Business rule violations: the input is valid but the operation is not allowed
// given the current state of the device.
var (
	ErrCreationTimeImmutable = errors.New("creation time cannot be updated")
	ErrLockedWhileInUse      = errors.New("name and brand cannot be updated while the device is in use")
	ErrDeleteInUse           = errors.New("in-use devices cannot be deleted")
)

// Persistence/concurrency errors.
var (
	ErrNotFound = errors.New("device not found")
	// ErrVersionMismatch is returned when the caller-supplied version (If-Match)
	// does not match the current version of the device.
	ErrVersionMismatch = errors.New("device version does not match the expected version")
	// ErrConcurrentModification is returned when the device changed between being
	// read and being written (optimistic locking failure).
	ErrConcurrentModification = errors.New("device was modified concurrently, retry the operation")
)
