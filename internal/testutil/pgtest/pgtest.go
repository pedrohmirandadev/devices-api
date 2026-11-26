// Package pgtest starts throwaway PostgreSQL containers for integration tests.
package pgtest

import (
	"context"
	"fmt"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// DefaultImage is the PostgreSQL image used by integration tests. It matches
// the version used by docker-compose.
const DefaultImage = "postgres:17-alpine"

// Postgres is a running PostgreSQL container.
type Postgres struct {
	// DSN is the connection string of the container's database.
	DSN       string
	container testcontainers.Container
}

// Start runs a PostgreSQL container from image and waits until it is ready.
func Start(ctx context.Context, image string) (*Postgres, error) {
	c, err := tcpostgres.Run(ctx, image,
		tcpostgres.WithDatabase("devices"),
		tcpostgres.WithUsername("devices"),
		tcpostgres.WithPassword("devices"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		_ = testcontainers.TerminateContainer(c)

		return nil, fmt.Errorf("start postgres container: %w", err)
	}

	return &Postgres{DSN: c.MustConnectionString(ctx, "sslmode=disable"), container: c}, nil
}

// Close stops and removes the container.
func (p *Postgres) Close() error {
	return testcontainers.TerminateContainer(p.container)
}
