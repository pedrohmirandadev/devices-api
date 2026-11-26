package device

// State is the lifecycle state of a device.
type State string

// Supported device states.
const (
	StateAvailable State = "available"
	StateInUse     State = "in-use"
	StateInactive  State = "inactive"
)

// ParseState converts a raw string into a State, rejecting unknown values.
func ParseState(raw string) (State, error) {
	switch s := State(raw); s {
	case StateAvailable, StateInUse, StateInactive:
		return s, nil
	default:
		return "", ErrInvalidState
	}
}

// String implements fmt.Stringer.
func (s State) String() string { return string(s) }
