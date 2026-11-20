package providers

import (
	"fmt"
	"sort"
	"sync"
)

// Registry holds the available book-source plugins.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.ID()] = p
}

func (r *Registry) Get(id string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[id]
	return p, ok
}

func (r *Registry) List() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// Info describes the provider for /api/providers. Providers that implement
// InfoProvider supply extra metadata; others get a minimal description.
type InfoProvider interface {
	Info() ProviderInfo
}

func Describe(p Provider) ProviderInfo {
	if ip, ok := p.(InfoProvider); ok {
		info := ip.Info()
		info.ID = p.ID()
		if info.Name == "" {
			info.Name = p.DisplayName()
		}
		if info.Capabilities == nil {
			info.Capabilities = p.Capabilities()
		}
		return info
	}
	return ProviderInfo{ID: p.ID(), Name: p.DisplayName(), Capabilities: p.Capabilities()}
}

func ErrUnknownProvider(id string) error {
	return fmt.Errorf("%w: %s", ErrUnsupported, id)
}
