package device_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pedrohmirandadev/devices-api/internal/domain/device"
)

var now = time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("X", 3600))

func ptr[T any](v T) *T { return &v }

func TestParseState(t *testing.T) {
	for _, s := range []device.State{device.StateAvailable, device.StateInUse, device.StateInactive} {
		got, err := device.ParseState(s.String())
		require.NoError(t, err)
		assert.Equal(t, s, got)
	}

	_, err := device.ParseState("broken")
	assert.ErrorIs(t, err, device.ErrInvalidState)
}

func TestNew(t *testing.T) {
	t.Run("defaults state to available and trims input", func(t *testing.T) {
		d, err := device.New("  iPhone ", " Apple ", "", now)
		require.NoError(t, err)

		assert.NotEqual(t, uuid.Nil, d.ID())
		assert.Equal(t, "iPhone", d.Name())
		assert.Equal(t, "Apple", d.Brand())
		assert.Equal(t, device.StateAvailable, d.State())
		assert.Equal(t, now.UTC(), d.CreatedAt())
		assert.Equal(t, now.UTC(), d.UpdatedAt())
		assert.Equal(t, int64(1), d.Version())
	})

	t.Run("keeps explicit state", func(t *testing.T) {
		d, err := device.New("Pixel", "Google", device.StateInUse, now)
		require.NoError(t, err)
		assert.Equal(t, device.StateInUse, d.State())
	})

	tests := map[string]struct {
		name, brand string
		state       device.State
		want        error
	}{
		"empty name":    {"  ", "Apple", "", device.ErrInvalidName},
		"long name":     {strings.Repeat("a", 256), "Apple", "", device.ErrInvalidName},
		"empty brand":   {"iPhone", "", "", device.ErrInvalidBrand},
		"long brand":    {"iPhone", strings.Repeat("b", 256), "", device.ErrInvalidBrand},
		"invalid state": {"iPhone", "Apple", "lost", device.ErrInvalidState},
		"max length ok": {strings.Repeat("é", 255), "Apple", "", nil},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := device.New(tc.name, tc.brand, tc.state, now)
			if tc.want == nil {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, tc.want)
		})
	}
}

func TestRestore(t *testing.T) {
	id := uuid.New()
	later := now.Add(time.Hour)

	d := device.Restore(id, "n", "b", device.StateInactive, now, later, 7)

	assert.Equal(t, id, d.ID())
	assert.Equal(t, "n", d.Name())
	assert.Equal(t, "b", d.Brand())
	assert.Equal(t, device.StateInactive, d.State())
	assert.Equal(t, now.UTC(), d.CreatedAt())
	assert.Equal(t, later.UTC(), d.UpdatedAt())
	assert.Equal(t, int64(7), d.Version())
}

func newDevice(t *testing.T, state device.State) *device.Device {
	t.Helper()
	d, err := device.New("iPhone", "Apple", state, now)
	require.NoError(t, err)

	return d
}

func TestUpdate(t *testing.T) {
	later := now.Add(time.Minute)

	t.Run("full update of an available device", func(t *testing.T) {
		d := newDevice(t, device.StateAvailable)

		changed, prev, err := d.Update(device.Changes{
			Name: ptr("Galaxy"), Brand: ptr("Samsung"), State: ptr(device.StateInUse),
		}, later)

		require.NoError(t, err)
		assert.True(t, changed)
		assert.Equal(t, device.StateAvailable, prev)
		assert.Equal(t, "Galaxy", d.Name())
		assert.Equal(t, "Samsung", d.Brand())
		assert.Equal(t, device.StateInUse, d.State())
		assert.Equal(t, later.UTC(), d.UpdatedAt())
		assert.Equal(t, now.UTC(), d.CreatedAt(), "creation time never changes")
	})

	t.Run("partial update keeps other fields", func(t *testing.T) {
		d := newDevice(t, device.StateAvailable)

		changed, _, err := d.Update(device.Changes{Name: ptr("iPhone 16")}, later)

		require.NoError(t, err)
		assert.True(t, changed)
		assert.Equal(t, "iPhone 16", d.Name())
		assert.Equal(t, "Apple", d.Brand())
		assert.Equal(t, device.StateAvailable, d.State())
	})

	t.Run("in-use device can change state only", func(t *testing.T) {
		d := newDevice(t, device.StateInUse)

		changed, prev, err := d.Update(device.Changes{State: ptr(device.StateAvailable)}, later)

		require.NoError(t, err)
		assert.True(t, changed)
		assert.Equal(t, device.StateInUse, prev)
		assert.Equal(t, device.StateAvailable, d.State())
	})

	t.Run("in-use device accepts unchanged name and brand", func(t *testing.T) {
		d := newDevice(t, device.StateInUse)

		changed, _, err := d.Update(device.Changes{
			Name: ptr("iPhone"), Brand: ptr("Apple"), State: ptr(device.StateInactive),
		}, later)

		require.NoError(t, err)
		assert.True(t, changed)
	})

	t.Run("in-use device rejects name or brand change", func(t *testing.T) {
		for _, c := range []device.Changes{{Name: ptr("Other")}, {Brand: ptr("Other")}} {
			d := newDevice(t, device.StateInUse)

			changed, _, err := d.Update(c, later)

			require.ErrorIs(t, err, device.ErrLockedWhileInUse)
			assert.False(t, changed)
			assert.Equal(t, "iPhone", d.Name())
			assert.Equal(t, "Apple", d.Brand())
		}
	})

	t.Run("no-op update", func(t *testing.T) {
		d := newDevice(t, device.StateInactive)

		changed, prev, err := d.Update(device.Changes{Name: ptr(" iPhone ")}, later)

		require.NoError(t, err)
		assert.False(t, changed)
		assert.Equal(t, device.StateInactive, prev)
		assert.Equal(t, now.UTC(), d.UpdatedAt())
	})

	t.Run("invalid values", func(t *testing.T) {
		d := newDevice(t, device.StateAvailable)

		_, _, err := d.Update(device.Changes{Name: ptr("")}, later)
		require.ErrorIs(t, err, device.ErrInvalidName)

		_, _, err = d.Update(device.Changes{State: ptr(device.State("x"))}, later)
		require.ErrorIs(t, err, device.ErrInvalidState)
	})
}

func TestUpdateCreationTime(t *testing.T) {
	d := newDevice(t, device.StateAvailable)

	_, _, err := d.Update(device.Changes{CreatedAt: ptr(now.Add(time.Second))}, now)
	require.ErrorIs(t, err, device.ErrCreationTimeImmutable)

	changed, _, err := d.Update(device.Changes{CreatedAt: ptr(now), Name: ptr("New")}, now)
	require.NoError(t, err, "echoing the same creation time is allowed")
	assert.True(t, changed)
	assert.Equal(t, now.UTC(), d.CreatedAt())
}

func TestEnsureDeletable(t *testing.T) {
	assert.NoError(t, newDevice(t, device.StateAvailable).EnsureDeletable())
	assert.NoError(t, newDevice(t, device.StateInactive).EnsureDeletable())
	assert.ErrorIs(t, newDevice(t, device.StateInUse).EnsureDeletable(), device.ErrDeleteInUse)
}
