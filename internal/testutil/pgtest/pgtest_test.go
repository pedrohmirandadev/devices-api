package pgtest_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pedrohmirandadev/devices-api/internal/testutil/pgtest"
)

func TestStartAndClose(t *testing.T) {
	ctx := context.Background()

	pg, err := pgtest.Start(ctx, pgtest.DefaultImage)
	require.NoError(t, err)

	conn, err := pgx.Connect(ctx, pg.DSN)
	require.NoError(t, err)
	require.NoError(t, conn.Ping(ctx))
	require.NoError(t, conn.Close(ctx))

	assert.NoError(t, pg.Close())
}

func TestStartInvalidImage(t *testing.T) {
	_, err := pgtest.Start(context.Background(), "invalid/image-that-does-not-exist:0")
	assert.ErrorContains(t, err, "start postgres container")
}
