package device_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	app "github.com/pedrohmirandadev/devices-api/internal/application/device"
	"github.com/pedrohmirandadev/devices-api/internal/domain/device"
)

var (
	errBoom = errors.New("boom")
	fixedT  = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
)

func ptr[T any](v T) *T { return &v }

// fakeRepo is a minimal in-memory Repository with error injection.
type fakeRepo struct {
	devices    map[uuid.UUID]*device.Device
	lastFilter device.Filter
	createErr  error
	listErr    error
	updateErr  error
	deleteErr  error
	updates    int
	deleted    []uuid.UUID
}

func newFakeRepo() *fakeRepo { return &fakeRepo{devices: map[uuid.UUID]*device.Device{}} }

func (r *fakeRepo) Create(_ context.Context, d *device.Device) error {
	if r.createErr != nil {
		return r.createErr
	}
	r.devices[d.ID()] = d

	return nil
}

func (r *fakeRepo) Get(_ context.Context, id uuid.UUID) (*device.Device, error) {
	d, ok := r.devices[id]
	if !ok {
		return nil, device.ErrNotFound
	}

	return device.Restore(d.ID(), d.Name(), d.Brand(), d.State(), d.CreatedAt(), d.UpdatedAt(), d.Version()), nil
}

func (r *fakeRepo) List(_ context.Context, f device.Filter) ([]*device.Device, int, error) {
	r.lastFilter = f
	if r.listErr != nil {
		return nil, 0, r.listErr
	}
	out := make([]*device.Device, 0, len(r.devices))
	for _, d := range r.devices {
		out = append(out, d)
	}

	return out, len(out), nil
}

func (r *fakeRepo) Update(_ context.Context, d *device.Device) (*device.Device, error) {
	r.updates++
	if r.updateErr != nil {
		return nil, r.updateErr
	}
	saved := device.Restore(d.ID(), d.Name(), d.Brand(), d.State(), d.CreatedAt(), d.UpdatedAt(), d.Version()+1)
	r.devices[d.ID()] = saved

	return saved, nil
}

func (r *fakeRepo) Delete(_ context.Context, id uuid.UUID, _ int64) error {
	if r.deleteErr != nil {
		return r.deleteErr
	}
	r.deleted = append(r.deleted, id)
	delete(r.devices, id)

	return nil
}

type fakePublisher struct{ events []device.StateChanged }

func (p *fakePublisher) Publish(_ context.Context, e device.StateChanged) {
	p.events = append(p.events, e)
}

type fixture struct {
	repo *fakeRepo
	pub  *fakePublisher
	svc  *app.Service
}

func newFixture() fixture {
	repo, pub := newFakeRepo(), &fakePublisher{}

	return fixture{repo: repo, pub: pub, svc: app.NewService(repo, pub, func() time.Time { return fixedT })}
}

func (f fixture) seed(t *testing.T, state device.State) *device.Device {
	t.Helper()
	d, err := device.New("iPhone", "Apple", state, fixedT.Add(-time.Hour))
	require.NoError(t, err)
	f.repo.devices[d.ID()] = d

	return d
}

func TestNewServiceDefaultsClock(t *testing.T) {
	svc := app.NewService(newFakeRepo(), &fakePublisher{}, nil)

	d, err := svc.Create(context.Background(), app.CreateInput{Name: "n", Brand: "b"})

	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), d.CreatedAt(), time.Minute)
}

func TestCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("persists a valid device", func(t *testing.T) {
		f := newFixture()

		d, err := f.svc.Create(ctx, app.CreateInput{Name: "Pixel", Brand: "Google", State: "in-use"})

		require.NoError(t, err)
		assert.Equal(t, device.StateInUse, d.State())
		assert.Equal(t, fixedT, d.CreatedAt())
		assert.Contains(t, f.repo.devices, d.ID())
	})

	t.Run("rejects invalid input", func(t *testing.T) {
		_, err := newFixture().svc.Create(ctx, app.CreateInput{Brand: "Google"})
		assert.ErrorIs(t, err, device.ErrInvalidName)
	})

	t.Run("propagates repository errors", func(t *testing.T) {
		f := newFixture()
		f.repo.createErr = errBoom

		_, err := f.svc.Create(ctx, app.CreateInput{Name: "n", Brand: "b"})
		assert.ErrorIs(t, err, errBoom)
	})
}

func TestGet(t *testing.T) {
	f := newFixture()
	d := f.seed(t, device.StateAvailable)

	got, err := f.svc.Get(context.Background(), d.ID())
	require.NoError(t, err)
	assert.Equal(t, d.ID(), got.ID())

	_, err = f.svc.Get(context.Background(), uuid.New())
	assert.ErrorIs(t, err, device.ErrNotFound)
}

func TestList(t *testing.T) {
	ctx := context.Background()

	t.Run("applies defaults and filters", func(t *testing.T) {
		f := newFixture()
		f.seed(t, device.StateAvailable)

		out, err := f.svc.List(ctx, app.ListInput{Brand: "Apple", State: "available", Offset: 5})

		require.NoError(t, err)
		assert.Equal(t, 1, out.Total)
		assert.Len(t, out.Items, 1)
		assert.Equal(t, app.DefaultLimit, out.Limit)
		assert.Equal(t, 5, out.Offset)
		assert.Equal(t, device.Filter{Brand: ptr("Apple"), State: ptr(device.StateAvailable), Limit: 20, Offset: 5}, f.repo.lastFilter)
	})

	t.Run("no filters", func(t *testing.T) {
		f := newFixture()

		_, err := f.svc.List(ctx, app.ListInput{Limit: app.MaxLimit})

		require.NoError(t, err)
		assert.Nil(t, f.repo.lastFilter.Brand)
		assert.Nil(t, f.repo.lastFilter.State)
		assert.Equal(t, app.MaxLimit, f.repo.lastFilter.Limit)
	})

	t.Run("invalid pagination", func(t *testing.T) {
		for _, in := range []app.ListInput{{Limit: -1}, {Limit: app.MaxLimit + 1}, {Offset: -1}} {
			_, err := newFixture().svc.List(ctx, in)
			assert.ErrorIs(t, err, app.ErrInvalidPagination)
		}
	})

	t.Run("invalid state", func(t *testing.T) {
		_, err := newFixture().svc.List(ctx, app.ListInput{State: "lost"})
		assert.ErrorIs(t, err, device.ErrInvalidState)
	})

	t.Run("repository error", func(t *testing.T) {
		f := newFixture()
		f.repo.listErr = errBoom

		_, err := f.svc.List(ctx, app.ListInput{})
		assert.ErrorIs(t, err, errBoom)
	})
}

func TestUpdate(t *testing.T) {
	ctx := context.Background()

	t.Run("state change is persisted then published", func(t *testing.T) {
		f := newFixture()
		d := f.seed(t, device.StateAvailable)

		got, err := f.svc.Update(ctx, d.ID(), app.UpdateInput{State: ptr("in-use"), ExpectedVersion: ptr(int64(1))})

		require.NoError(t, err)
		assert.Equal(t, device.StateInUse, got.State())
		assert.Equal(t, int64(2), got.Version())
		require.Len(t, f.pub.events, 1)
		assert.Equal(t, device.StateChanged{
			DeviceID: d.ID(), PreviousState: device.StateAvailable, NewState: device.StateInUse, OccurredAt: fixedT,
		}, f.pub.events[0])
	})

	t.Run("change without state change publishes nothing", func(t *testing.T) {
		f := newFixture()
		d := f.seed(t, device.StateAvailable)

		got, err := f.svc.Update(ctx, d.ID(), app.UpdateInput{Name: ptr("New"), Brand: ptr("Brand")})

		require.NoError(t, err)
		assert.Equal(t, "New", got.Name())
		assert.Empty(t, f.pub.events)
	})

	t.Run("no-op update skips persistence", func(t *testing.T) {
		f := newFixture()
		d := f.seed(t, device.StateAvailable)

		got, err := f.svc.Update(ctx, d.ID(), app.UpdateInput{})

		require.NoError(t, err)
		assert.Equal(t, int64(1), got.Version())
		assert.Zero(t, f.repo.updates)
	})

	t.Run("domain rule violation", func(t *testing.T) {
		f := newFixture()
		d := f.seed(t, device.StateInUse)

		_, err := f.svc.Update(ctx, d.ID(), app.UpdateInput{Name: ptr("Other")})

		require.ErrorIs(t, err, device.ErrLockedWhileInUse)
		assert.Zero(t, f.repo.updates)
	})

	t.Run("creation time cannot change", func(t *testing.T) {
		f := newFixture()
		d := f.seed(t, device.StateAvailable)

		_, err := f.svc.Update(ctx, d.ID(), app.UpdateInput{CreatedAt: ptr(fixedT)})

		require.ErrorIs(t, err, device.ErrCreationTimeImmutable)
		assert.Zero(t, f.repo.updates)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := newFixture().svc.Update(ctx, uuid.New(), app.UpdateInput{})
		assert.ErrorIs(t, err, device.ErrNotFound)
	})

	t.Run("version mismatch", func(t *testing.T) {
		f := newFixture()
		d := f.seed(t, device.StateAvailable)

		_, err := f.svc.Update(ctx, d.ID(), app.UpdateInput{ExpectedVersion: ptr(int64(9))})
		assert.ErrorIs(t, err, device.ErrVersionMismatch)
	})

	t.Run("persistence failure publishes nothing", func(t *testing.T) {
		f := newFixture()
		f.repo.updateErr = device.ErrConcurrentModification
		d := f.seed(t, device.StateAvailable)

		_, err := f.svc.Update(ctx, d.ID(), app.UpdateInput{State: ptr("inactive")})

		require.ErrorIs(t, err, device.ErrConcurrentModification)
		assert.Empty(t, f.pub.events)
	})
}

func TestDelete(t *testing.T) {
	ctx := context.Background()

	t.Run("deletes an available device", func(t *testing.T) {
		f := newFixture()
		d := f.seed(t, device.StateAvailable)

		require.NoError(t, f.svc.Delete(ctx, d.ID(), ptr(int64(1))))
		assert.Equal(t, []uuid.UUID{d.ID()}, f.repo.deleted)
	})

	t.Run("refuses in-use device", func(t *testing.T) {
		f := newFixture()
		d := f.seed(t, device.StateInUse)

		require.ErrorIs(t, f.svc.Delete(ctx, d.ID(), nil), device.ErrDeleteInUse)
		assert.Empty(t, f.repo.deleted)
	})

	t.Run("not found and version mismatch", func(t *testing.T) {
		f := newFixture()
		d := f.seed(t, device.StateAvailable)

		assert.ErrorIs(t, f.svc.Delete(ctx, uuid.New(), nil), device.ErrNotFound)
		assert.ErrorIs(t, f.svc.Delete(ctx, d.ID(), ptr(int64(2))), device.ErrVersionMismatch)
	})

	t.Run("repository error", func(t *testing.T) {
		f := newFixture()
		f.repo.deleteErr = errBoom
		d := f.seed(t, device.StateInactive)

		assert.ErrorIs(t, f.svc.Delete(ctx, d.ID(), nil), errBoom)
	})
}
