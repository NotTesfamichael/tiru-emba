// Package peer holds the in-memory view of who's currently online.
//
// Registry is intentionally NOT protected by a mutex. It's owned exclusively
// by the Bubble Tea Update loop (a single goroutine), which is the only
// place it's ever mutated or read. Discovery events arrive on a channel and
// are turned into Registry mutations inside Update, so there's no shared
// mutable state crossing goroutine boundaries -- channels do that job.
package peer

import (
	"sort"
	"time"
)

// Info describes one online teammate, as last announced by their heartbeat.
type Info struct {
	ID       string
	Handle   string
	Addr     string
	TCPPort  int
	LastSeen time.Time
}

type Registry struct {
	byID map[string]Info
}

func NewRegistry() *Registry {
	return &Registry{byID: make(map[string]Info)}
}

// Upsert records/refreshes a sighting of a peer.
func (r *Registry) Upsert(info Info) {
	r.byID[info.ID] = info
}

// Prune drops any peer whose last heartbeat is older than ttl, relative to
// now. Returns true if the registry changed.
func (r *Registry) Prune(now time.Time, ttl time.Duration) bool {
	changed := false
	for id, info := range r.byID {
		if now.Sub(info.LastSeen) > ttl {
			delete(r.byID, id)
			changed = true
		}
	}
	return changed
}

// Lookup resolves a handle (e.g. "@alex") to its current known address.
func (r *Registry) Lookup(handle string) (Info, bool) {
	for _, info := range r.byID {
		if info.Handle == handle {
			return info, true
		}
	}
	return Info{}, false
}

// List returns all known online peers sorted by handle, for stable rendering.
func (r *Registry) List() []Info {
	out := make([]Info, 0, len(r.byID))
	for _, info := range r.byID {
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Handle < out[j].Handle })
	return out
}

func (r *Registry) Len() int {
	return len(r.byID)
}
