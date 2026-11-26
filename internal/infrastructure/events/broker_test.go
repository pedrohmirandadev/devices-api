package events_test

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pedrohmirandadev/devices-api/internal/domain/device"
	"github.com/pedrohmirandadev/devices-api/internal/infrastructure/events"
)

func event() device.StateChanged {
	return device.StateChanged{DeviceID: uuid.New(), PreviousState: device.StateAvailable, NewState: device.StateInUse}
}

func TestPublishFansOut(t *testing.T) {
	b := events.NewBroker(4, slog.New(slog.DiscardHandler))
	a, cancelA := b.Subscribe()
	c, cancelC := b.Subscribe()
	defer cancelA()
	defer cancelC()
	require.Equal(t, 2, b.Subscribers())

	e := event()
	b.Publish(context.Background(), e)

	assert.Equal(t, e, <-a)
	assert.Equal(t, e, <-c)
}

func TestSlowSubscriberDropsEvents(t *testing.T) {
	var logs bytes.Buffer
	b := events.NewBroker(1, slog.New(slog.NewTextHandler(&logs, nil)))
	ch, cancel := b.Subscribe()
	defer cancel()

	first := event()
	b.Publish(context.Background(), first)
	b.Publish(context.Background(), event()) // buffer full: dropped, must not block

	assert.Equal(t, first, <-ch)
	assert.Empty(t, ch)
	assert.Contains(t, logs.String(), "dropping state change event")
}

func TestCancelIsIdempotentAndClosesChannel(t *testing.T) {
	b := events.NewBroker(1, slog.New(slog.DiscardHandler))
	ch, cancel := b.Subscribe()

	cancel()
	cancel()

	_, open := <-ch
	assert.False(t, open)
	assert.Zero(t, b.Subscribers())
}

func TestClose(t *testing.T) {
	b := events.NewBroker(1, slog.New(slog.DiscardHandler))
	ch, cancel := b.Subscribe()

	b.Close()
	cancel() // already removed by Close: no double close

	_, open := <-ch
	assert.False(t, open)

	late, lateCancel := b.Subscribe()
	lateCancel()
	_, open = <-late
	assert.False(t, open, "subscriptions after Close are closed immediately")

	b.Publish(context.Background(), event()) // no subscribers, no panic
}

func TestConcurrentUse(t *testing.T) {
	b := events.NewBroker(8, slog.New(slog.DiscardHandler))

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, cancel := b.Subscribe()
			cancel()
		}()
		go func() {
			defer wg.Done()
			b.Publish(context.Background(), event())
		}()
	}
	wg.Wait()
	b.Close()

	assert.Zero(t, b.Subscribers())
}
