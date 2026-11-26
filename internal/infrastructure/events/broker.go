// Package events provides an in-process publish/subscribe broker for domain
// events. It implements device.EventPublisher.
package events

import (
	"context"
	"log/slog"
	"sync"

	"github.com/pedrohmirandadev/devices-api/internal/domain/device"
)

// Broker fans out StateChanged events to every current subscriber.
//
// Publishing never blocks: each subscriber has a bounded buffer and events are
// dropped (and logged) for subscribers that cannot keep up, so a slow client
// can never stall the API. Missed events are not replayed, as allowed by the
// requirements.
type Broker struct {
	mu     sync.RWMutex
	subs   map[chan device.StateChanged]struct{}
	buffer int
	closed bool
	logger *slog.Logger
}

var _ device.EventPublisher = (*Broker)(nil)

// NewBroker creates a broker whose subscribers buffer up to buffer events.
func NewBroker(buffer int, logger *slog.Logger) *Broker {
	return &Broker{
		subs:   make(map[chan device.StateChanged]struct{}),
		buffer: buffer,
		logger: logger,
	}
}

// Subscribe registers a new subscriber. The returned channel is closed when the
// subscriber cancels or the broker is closed. cancel is idempotent.
func (b *Broker) Subscribe() (events <-chan device.StateChanged, cancel func()) {
	ch := make(chan device.StateChanged, b.buffer)

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		close(ch)

		return ch, func() {}
	}

	b.subs[ch] = struct{}{}

	return ch, func() { b.remove(ch) }
}

// Publish delivers e to all subscribers without blocking.
func (b *Broker) Publish(ctx context.Context, e device.StateChanged) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for ch := range b.subs {
		select {
		case ch <- e:
		default:
			b.logger.WarnContext(ctx, "dropping state change event for slow subscriber",
				slog.String("device_id", e.DeviceID.String()))
		}
	}
}

// Subscribers returns the number of active subscribers.
func (b *Broker) Subscribers() int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return len(b.subs)
}

// Close disconnects every subscriber. Later subscriptions are closed immediately.
func (b *Broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.closed = true
	for ch := range b.subs {
		delete(b.subs, ch)
		close(ch)
	}
}

func (b *Broker) remove(ch chan device.StateChanged) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.subs[ch]; ok {
		delete(b.subs, ch)
		close(ch)
	}
}
