package relay

import (
	"fmt"
	"sync"
)

// Hub tracks which authenticated users currently have a live connection, so
// a message arriving on one connection's goroutine can be delivered to
// another's. It has no notion of organizations at all -- that scoping
// (who should be told about whom) lives in Server, via Orgs; Hub only
// answers "is this handle online, and if so, how do I reach it." At most
// one active connection per handle -- reconnect-eviction (letting a new
// login replace an existing one) isn't attempted here, kept as a
// documented simplification.
type Hub struct {
	mu      sync.Mutex
	clients map[string]chan<- Envelope // keyed by normalized handle
}

func NewHub() *Hub {
	return &Hub{clients: make(map[string]chan<- Envelope)}
}

// Join registers handle as online with outbox as where to push it
// envelopes. Fails if handle is already connected elsewhere. Unlike Phase
// 2, this no longer returns "who else is online" -- with presence scoped
// to organization membership, that's the caller's job via Orgs.MateHandles,
// not something the Hub (which knows nothing about orgs) can answer.
func (h *Hub) Join(handle string, outbox chan<- Envelope) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, exists := h.clients[handle]; exists {
		return fmt.Errorf("relay: %s is already connected elsewhere", handle)
	}
	h.clients[handle] = outbox
	return nil
}

// Leave unregisters handle, but only if outbox is still the channel it was
// registered with -- guards against a stale Leave (from a connection that
// a later Join for the same handle would otherwise have already evicted,
// were eviction implemented) removing a newer registration.
func (h *Hub) Leave(handle string, outbox chan<- Envelope) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if existing, ok := h.clients[handle]; ok && existing == outbox {
		delete(h.clients, handle)
	}
}

// Send delivers env to handle's outbox. Returns false if handle isn't
// online, or if its outbox is full (a slow/stuck reader; treated the same
// as "undeliverable" rather than blocking the sender's goroutine).
func (h *Hub) Send(handle string, env Envelope) bool {
	h.mu.Lock()
	outbox, ok := h.clients[handle]
	h.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case outbox <- env:
		return true
	default:
		return false
	}
}

// Online reports whether handle currently has a live connection.
func (h *Hub) Online(handle string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.clients[handle]
	return ok
}
