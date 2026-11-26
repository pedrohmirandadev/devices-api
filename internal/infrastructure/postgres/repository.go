package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pedrohmirandadev/devices-api/internal/domain/device"
)

const selectColumns = "id, name, brand, state, created_at, updated_at, version"

// row maps a devices table row.
type row struct {
	ID        uuid.UUID `db:"id"`
	Name      string    `db:"name"`
	Brand     string    `db:"brand"`
	State     string    `db:"state"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
	Version   int64     `db:"version"`
}

func (r row) toDomain() *device.Device {
	return device.Restore(r.ID, r.Name, r.Brand, device.State(r.State), r.CreatedAt, r.UpdatedAt, r.Version)
}

// listRow is a row plus the total number of rows matching the filter.
type listRow struct {
	row
	Total int `db:"total"`
}

// Repository is the PostgreSQL implementation of device.Repository.
type Repository struct {
	pool *pgxpool.Pool
}

var _ device.Repository = (*Repository)(nil)

// NewRepository returns a Repository backed by pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Create inserts a new device.
func (r *Repository) Create(ctx context.Context, d *device.Device) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO devices (`+selectColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		d.ID(), d.Name(), d.Brand(), d.State().String(), d.CreatedAt(), d.UpdatedAt(), d.Version(),
	)
	if err != nil {
		return fmt.Errorf("insert device: %w", err)
	}

	return nil
}

// Get fetches a device by id.
func (r *Repository) Get(ctx context.Context, id uuid.UUID) (*device.Device, error) {
	rows, _ := r.pool.Query(ctx, `SELECT `+selectColumns+` FROM devices WHERE id = $1`, id)

	return collectOne(rows, "get device")
}

// List returns a page of devices ordered from newest to oldest, together with
// the total number of matching devices (computed in the same query).
func (r *Repository) List(ctx context.Context, f device.Filter) ([]*device.Device, int, error) {
	where, args := buildWhere(f)

	args = append(args, f.Limit, f.Offset)
	query := fmt.Sprintf(
		`SELECT %s, COUNT(*) OVER() AS total FROM devices%s ORDER BY created_at DESC, id DESC LIMIT $%d OFFSET $%d`,
		selectColumns, where, len(args)-1, len(args),
	)

	rows, _ := r.pool.Query(ctx, query, args...)
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[listRow])
	if err != nil {
		return nil, 0, fmt.Errorf("list devices: %w", err)
	}

	if len(list) == 0 {
		// The window count is not available on an empty page (e.g. offset past
		// the end), so fall back to an explicit count.
		total, err := r.count(ctx, where, args[:len(args)-2])

		return []*device.Device{}, total, err
	}

	items := make([]*device.Device, len(list))
	for i, lr := range list {
		items[i] = lr.toDomain()
	}

	return items, list[0].Total, nil
}

// Update writes the device using optimistic locking on its version.
func (r *Repository) Update(ctx context.Context, d *device.Device) (*device.Device, error) {
	rows, _ := r.pool.Query(ctx,
		`UPDATE devices SET name = $2, brand = $3, state = $4, updated_at = $5, version = version + 1
		 WHERE id = $1 AND version = $6
		 RETURNING `+selectColumns,
		d.ID(), d.Name(), d.Brand(), d.State().String(), d.UpdatedAt(), d.Version(),
	)

	saved, err := collectOne(rows, "update device")
	if errors.Is(err, device.ErrNotFound) {
		return nil, r.notFoundOrConflict(ctx, d.ID())
	}

	return saved, err
}

// Delete removes the device if its version is still the expected one.
func (r *Repository) Delete(ctx context.Context, id uuid.UUID, version int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM devices WHERE id = $1 AND version = $2`, id, version)
	if err != nil {
		return fmt.Errorf("delete device: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return r.notFoundOrConflict(ctx, id)
	}

	return nil
}

// notFoundOrConflict explains why a versioned write matched no row.
func (r *Repository) notFoundOrConflict(ctx context.Context, id uuid.UUID) error {
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM devices WHERE id = $1)`, id).Scan(&exists); err != nil {
		return fmt.Errorf("check device existence: %w", err)
	}

	if exists {
		return device.ErrConcurrentModification
	}

	return device.ErrNotFound
}

func (r *Repository) count(ctx context.Context, where string, args []any) (int, error) {
	var total int
	if err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM devices`+where, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("count devices: %w", err)
	}

	return total, nil
}

func buildWhere(f device.Filter) (string, []any) {
	var (
		conds []string
		args  []any
	)

	if f.Brand != nil {
		args = append(args, *f.Brand)
		conds = append(conds, fmt.Sprintf("lower(brand) = lower($%d)", len(args)))
	}
	if f.State != nil {
		args = append(args, f.State.String())
		conds = append(conds, fmt.Sprintf("state = $%d", len(args)))
	}

	if len(conds) == 0 {
		return "", args
	}

	return " WHERE " + strings.Join(conds, " AND "), args
}

// collectOne reads exactly one row. Query errors surface through rows, which is
// why callers can ignore the error returned by Query.
func collectOne(rows pgx.Rows, op string) (*device.Device, error) {
	r, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[row])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, device.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	return r.toDomain(), nil
}
