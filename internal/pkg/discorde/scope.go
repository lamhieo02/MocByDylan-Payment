package discorde

import (
	"sync"

	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/pkg/discord"
)

type Scope struct {
	mu   sync.RWMutex
	tags map[string]string
}

func NewScope() *Scope {
	return &Scope{
		tags: make(map[string]string),
	}
}

func (scope *Scope) SetTag(key, value string) {
	scope.mu.Lock()
	defer scope.mu.Unlock()

	scope.tags[key] = value
}

func (scope *Scope) SetTags(tags map[string]string) {
	scope.mu.Lock()
	defer scope.mu.Unlock()

	for k, v := range tags {
		scope.tags[k] = v
	}
}

func (scope *Scope) RemoveTag(key string) {
	scope.mu.Lock()
	defer scope.mu.Unlock()

	delete(scope.tags, key)
}

// SetChannel routes the event captured within this scope to the named channel.
func (scope *Scope) SetChannel(channel discord.ChannelName) {
	scope.SetTag(tagChannel, string(channel))
}

// ApplyToEvent copies scope tags onto the event.
// The tagChannel entry is consumed to set event.Channel and is not propagated
// into event.Tags.
func (scope *Scope) ApplyToEvent(event *Event) *Event {
	scope.mu.Lock()
	defer scope.mu.Unlock()

	if len(scope.tags) == 0 {
		return event
	}

	if event.Tags == nil {
		event.Tags = make(map[string]string, len(scope.tags))
	}

	for key, value := range scope.tags {
		if key == tagChannel {
			event.Channel = discord.ChannelName(value)
			continue
		}
		event.Tags[key] = value
	}

	return event
}

func (scope *Scope) Clone() *Scope {
	scope.mu.RLock()
	defer scope.mu.RUnlock()

	clone := NewScope()
	for key, value := range scope.tags {
		clone.tags[key] = value
	}

	return clone
}
