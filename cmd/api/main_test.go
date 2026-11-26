package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pedrohmirandadev/devices-api/internal/testutil/pgtest"
)

func TestMainExitsWithErrorOnInvalidConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	var code int
	exit = func(c int) { code = c }
	t.Cleanup(func() { exit = nil })

	main()

	assert.Equal(t, 1, code)
}

func TestRunStopsGracefullyOnSIGTERM(t *testing.T) {
	pg, err := pgtest.Start(context.Background(), pgtest.DefaultImage)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Close() })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	env := map[string]string{"DATABASE_URL": pg.DSN, "HTTP_ADDR": addr}
	var stdout, stderr bytes.Buffer

	done := make(chan int, 1)
	go func() { done <- run(func(k string) string { return env[k] }, &stdout, &stderr) }()

	require.Eventually(t, func() bool {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()

		return resp.StatusCode == http.StatusOK
	}, 10*time.Second, 50*time.Millisecond)

	require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGTERM))

	select {
	case code := <-done:
		assert.Equal(t, 0, code, stderr.String())
	case <-time.After(10 * time.Second):
		t.Fatal("server did not stop after SIGTERM")
	}
}
