package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pedrohmirandadev/devices-api/internal/domain/device"
	"github.com/pedrohmirandadev/devices-api/internal/testutil/pgtest"
)

var (
	testDSN  string
	testPool *pgxpool.Pool
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()

	pg, err := pgtest.Start(ctx, pgtest.DefaultImage)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}
	defer pg.Close()

	testDSN = pg.DSN
	if err := Migrate(testDSN); err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	testPool, err = NewPool(ctx, testDSN, 5)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}
	defer testPool.Close()

	return m.Run()
}

func newRepo(t *testing.T) *Repository {
	t.Helper()
	_, err := testPool.Exec(context.Background(), "TRUNCATE devices")
	require.NoError(t, err)

	return NewRepository(testPool)
}

func closedRepo(t *testing.T) *Repository {
	t.Helper()
	pool, err := NewPool(context.Background(), testDSN, 1)
	require.NoError(t, err)
	pool.Close()

	return NewRepository(pool)
}

func mustDevice(t *testing.T, name, brand string, state device.State, at time.Time) *device.Device {
	t.Helper()
	d, err := device.New(name, brand, state, at)
	require.NoError(t, err)

	return d
}

func ptr[T any](v T) *T { return &v }

var base = time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

func TestNewPoolErrors(t *testing.T) {
	ctx := context.Background()

	_, err := NewPool(ctx, "::not a url::", 1)
	require.ErrorContains(t, err, "parse database url")

	_, err = NewPool(ctx, "postgres://u:p@127.0.0.1:1/db?connect_timeout=1", 1)
	require.ErrorContains(t, err, "ping database")

	_, err = NewPool(ctx, "postgres://u:p@localhost/db", 0)
	require.ErrorContains(t, err, "create connection pool")
}

func TestMigrate(t *testing.T) {
	t.Run("is idempotent", func(t *testing.T) {
		require.NoError(t, Migrate(testDSN))
	})

	t.Run("invalid source directory", func(t *testing.T) {
		require.ErrorContains(t, migrateFS(testDSN, fstest.MapFS{}, "missing"), "open migrations")
	})

	t.Run("invalid database url", func(t *testing.T) {
		require.ErrorContains(t, Migrate("unknown://x"), "init migrations")
	})

	t.Run("failing migration", func(t *testing.T) {
		fsys := fstest.MapFS{"999_broken.up.sql": {Data: []byte("THIS IS NOT SQL")}}

		require.ErrorContains(t, migrateFS(testDSN, fsys, "."), "apply migrations")

		// Leave the schema_migrations table as it was for other tests.
		_, err := testPool.Exec(context.Background(), "UPDATE schema_migrations SET version = 1, dirty = false")
		require.NoError(t, err)
	})
}

func TestMigrateURL(t *testing.T) {
	assert.Equal(t, "pgx5://u@h/db", migrateURL("postgres://u@h/db"))
	assert.Equal(t, "pgx5://u@h/db", migrateURL("postgresql://u@h/db"))
	assert.Equal(t, "host=h", migrateURL("host=h"))
}

func TestCreateAndGet(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	d := mustDevice(t, "iPhone", "Apple", device.StateInUse, base)

	require.NoError(t, repo.Create(ctx, d))

	got, err := repo.Get(ctx, d.ID())
	require.NoError(t, err)
	assert.Equal(t, d, got)

	_, err = repo.Get(ctx, uuid.New())
	assert.ErrorIs(t, err, device.ErrNotFound)

	err = repo.Create(ctx, d)
	assert.ErrorContains(t, err, "insert device", "duplicate id")
}

func TestList(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)

	seed := []*device.Device{
		mustDevice(t, "iPhone", "Apple", device.StateAvailable, base),
		mustDevice(t, "MacBook", "APPLE", device.StateInUse, base.Add(time.Minute)),
		mustDevice(t, "Pixel", "Google", device.StateInUse, base.Add(2*time.Minute)),
		mustDevice(t, "Nokia", "Nokia", device.StateInactive, base.Add(3*time.Minute)),
	}
	for _, d := range seed {
		require.NoError(t, repo.Create(ctx, d))
	}

	ids := func(items []*device.Device) []uuid.UUID {
		out := make([]uuid.UUID, len(items))
		for i, d := range items {
			out[i] = d.ID()
		}

		return out
	}

	tests := map[string]struct {
		filter    device.Filter
		wantIDs   []uuid.UUID
		wantTotal int
	}{
		"all, newest first": {
			filter:    device.Filter{Limit: 10},
			wantIDs:   []uuid.UUID{seed[3].ID(), seed[2].ID(), seed[1].ID(), seed[0].ID()},
			wantTotal: 4,
		},
		"paginated": {
			filter:    device.Filter{Limit: 2, Offset: 1},
			wantIDs:   []uuid.UUID{seed[2].ID(), seed[1].ID()},
			wantTotal: 4,
		},
		"by brand (case-insensitive)": {
			filter:    device.Filter{Brand: ptr("apple"), Limit: 10},
			wantIDs:   []uuid.UUID{seed[1].ID(), seed[0].ID()},
			wantTotal: 2,
		},
		"by state": {
			filter:    device.Filter{State: ptr(device.StateInUse), Limit: 10},
			wantIDs:   []uuid.UUID{seed[2].ID(), seed[1].ID()},
			wantTotal: 2,
		},
		"by brand and state": {
			filter:    device.Filter{Brand: ptr("Apple"), State: ptr(device.StateInUse), Limit: 10},
			wantIDs:   []uuid.UUID{seed[1].ID()},
			wantTotal: 1,
		},
		"offset past the end still reports total": {
			filter:    device.Filter{State: ptr(device.StateInUse), Limit: 10, Offset: 50},
			wantIDs:   []uuid.UUID{},
			wantTotal: 2,
		},
		"no match": {
			filter:    device.Filter{Brand: ptr("Sony"), Limit: 10},
			wantIDs:   []uuid.UUID{},
			wantTotal: 0,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			items, total, err := repo.List(ctx, tc.filter)

			require.NoError(t, err)
			assert.Equal(t, tc.wantIDs, ids(items))
			assert.Equal(t, tc.wantTotal, total)
		})
	}
}

func TestUpdate(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	d := mustDevice(t, "iPhone", "Apple", device.StateAvailable, base)
	require.NoError(t, repo.Create(ctx, d))

	_, _, err := d.Update(device.Changes{Name: ptr("iPhone 16"), State: ptr(device.StateInUse)}, base.Add(time.Hour))
	require.NoError(t, err)

	saved, err := repo.Update(ctx, d)
	require.NoError(t, err)
	assert.Equal(t, "iPhone 16", saved.Name())
	assert.Equal(t, device.StateInUse, saved.State())
	assert.Equal(t, base, saved.CreatedAt())
	assert.Equal(t, base.Add(time.Hour), saved.UpdatedAt())
	assert.Equal(t, int64(2), saved.Version())

	t.Run("stale version is a conflict", func(t *testing.T) {
		_, err := repo.Update(ctx, d) // d still carries version 1
		assert.ErrorIs(t, err, device.ErrConcurrentModification)
	})

	t.Run("missing device", func(t *testing.T) {
		_, err := repo.Update(ctx, mustDevice(t, "x", "y", "", base))
		assert.ErrorIs(t, err, device.ErrNotFound)
	})

	t.Run("database protects created_at", func(t *testing.T) {
		_, err := testPool.Exec(ctx, "UPDATE devices SET created_at = now() WHERE id = $1", d.ID())
		assert.ErrorContains(t, err, "created_at is immutable")
	})
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	d := mustDevice(t, "iPhone", "Apple", device.StateAvailable, base)
	require.NoError(t, repo.Create(ctx, d))

	assert.ErrorIs(t, repo.Delete(ctx, d.ID(), 99), device.ErrConcurrentModification)
	require.NoError(t, repo.Delete(ctx, d.ID(), 1))
	assert.ErrorIs(t, repo.Delete(ctx, d.ID(), 1), device.ErrNotFound)
}

func TestDatabaseErrors(t *testing.T) {
	ctx := context.Background()
	repo := closedRepo(t)
	d := mustDevice(t, "iPhone", "Apple", "", base)

	assert.ErrorContains(t, repo.Create(ctx, d), "insert device")

	_, err := repo.Get(ctx, d.ID())
	assert.ErrorContains(t, err, "get device")

	_, _, err = repo.List(ctx, device.Filter{Limit: 1})
	assert.ErrorContains(t, err, "list devices")

	_, err = repo.Update(ctx, d)
	assert.ErrorContains(t, err, "update device")

	assert.ErrorContains(t, repo.Delete(ctx, d.ID(), 1), "delete device")
	assert.ErrorContains(t, repo.notFoundOrConflict(ctx, d.ID()), "check device existence")

	_, err = repo.count(ctx, "", nil)
	assert.ErrorContains(t, err, "count devices")
}
