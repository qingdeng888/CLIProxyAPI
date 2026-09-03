// Package proxypool maintains a process-wide registry of the single flat proxy
// pool. Any "proxy-url" style setting value is a mode selector:
//
//   - "" / "direct" / "none" and plain proxy URLs pass through unchanged
//   - "pool", "pool:*", "rotate" pick a random enabled proxy
//   - "bind:<id>" / "bind:<url>" pin the selector to one pool proxy
//   - legacy "pool:<name>" values are treated as "pool" (random rotate)
package proxypool

import (
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
	log "github.com/sirupsen/logrus"
)

// poolProxy is a single proxy entity in the runtime pool.
type poolProxy struct {
	id      string
	name    string
	url     string
	enabled bool
}

// poolState holds the flat proxy list plus the subset of enabled proxies so
// random selection never has to skip disabled entries.
type poolState struct {
	proxies []poolProxy
	enabled []poolProxy
}

// next returns a uniformly random enabled proxy URL; an empty enabled set
// returns "".
func (p *poolState) next() string {
	if len(p.enabled) == 0 {
		log.Debug("proxy pool has no enabled proxies")
		return ""
	}
	candidate := p.enabled[rand.IntN(len(p.enabled))]
	log.WithFields(log.Fields{
		"proxy_name": candidate.name,
		"proxy_id":   candidate.id,
		"proxy_via":  proxyutil.Redact(candidate.url),
	}).Debug("proxy pool rotate selected")
	return candidate.url
}

// pick returns the first enabled proxy matching the given predicate.
// The mode argument only labels the debug log line ("bind" vs "rotate").
func (p *poolState) pick(mode string, match func(poolProxy) bool) string {
	for _, candidate := range p.proxies {
		if !candidate.enabled || !match(candidate) {
			continue
		}
		log.WithFields(log.Fields{
			"mode":       mode,
			"proxy_name": candidate.name,
			"proxy_id":   candidate.id,
			"proxy_via":  proxyutil.Redact(candidate.url),
		}).Debug("proxy pool bind selected")
		return candidate.url
	}
	return ""
}

var (
	regMu sync.RWMutex
	state atomic.Pointer[poolState]
)

func init() {
	state.Store(&poolState{})
	// Keep the registry in sync with every successfully loaded config without
	// creating an import cycle (config must not import this package).
	config.OnConfigLoaded = Sync
}

// Sync atomically replaces the runtime pool with the flat proxy list from cfg.
// The list is deep-copied so later mutations of cfg do not affect the registry.
func Sync(cfg *config.Config) {
	next := &poolState{}
	if cfg != nil && len(cfg.ProxyPool) > 0 {
		next.proxies = make([]poolProxy, 0, len(cfg.ProxyPool))
		for _, entry := range cfg.ProxyPool {
			if trimmed := strings.TrimSpace(entry.URL); trimmed != "" {
				proxy := poolProxy{
					id:      entry.ID,
					name:    strings.TrimSpace(entry.Name),
					url:     trimmed,
					enabled: entry.IsEnabled(),
				}
				next.proxies = append(next.proxies, proxy)
				if proxy.enabled {
					next.enabled = append(next.enabled, proxy)
				}
			}
		}
	}
	regMu.Lock()
	state.Store(next)
	regMu.Unlock()
	if log.IsLevelEnabled(log.DebugLevel) {
		for _, p := range next.proxies {
			log.WithFields(log.Fields{
				"proxy_name":   p.name,
				"proxy_id":     p.id,
				"pool_enabled": p.enabled,
			}).Debug("proxy pool synced")
		}
	}
}

// isSelector reports whether the value is a pool mode selector rather than a
// plain proxy URL or the direct/none keywords.
func isSelector(lower string) bool {
	return lower == "pool" || lower == "pool:*" || lower == "rotate" ||
		strings.HasPrefix(lower, "pool:") || strings.HasPrefix(lower, "bind:")
}

// resolveRotate returns a uniformly random enabled pool proxy.
func resolveRotate() string {
	regMu.RLock()
	current := state.Load()
	regMu.RUnlock()
	if len(current.proxies) == 0 {
		log.Debug("proxy pool selector matched but the pool is empty")
		return ""
	}
	return current.next()
}

// resolveBind pins the selector to one pool proxy. It resolves by stable entity
// ID first and then, when the value parses as a proxy URL, by URL equality.
// Unknown or disabled matches fall back to "" so the caller keeps inherit
// semantics, mirroring the aqua-platform fallback design.
func resolveBind(value string) string {
	if value == "" {
		log.Warn("proxy pool bind selector is empty")
		return ""
	}
	regMu.RLock()
	current := state.Load()
	regMu.RUnlock()
	target := current.pick("bind", func(candidate poolProxy) bool { return candidate.id == value })
	if target != "" {
		return target
	}
	if _, errParse := proxyutil.Parse(value); errParse == nil {
		// The value looks like a proxy URL; resolve by URL equality.
		target = current.pick("bind", func(candidate poolProxy) bool { return candidate.url == value })
		if target != "" {
			return target
		}
		log.WithField("proxy", value).Warn("proxy pool bind selector does not match any enabled pool proxy")
		return ""
	}
	log.WithField("bind", value).Warn("proxy pool bind selector does not match any pool proxy id")
	return ""
}

// Resolve returns the proxy URL to use for the given selector value.
//
//   - "" / "direct" / "none" and plain proxy URLs are returned trimmed and
//     unchanged (callers' proxyutil.Parse treats them as inherit / direct /
//     proxy modes respectively)
//   - "pool", "pool:*", "rotate" and the legacy "pool:<name>" form pick a
//     uniformly random enabled pool proxy; an empty pool returns "" so the
//     caller falls back to inherit semantics
//   - "bind:<id>" (or "bind:<url>") returns that proxy's URL when it exists and
//     is enabled; otherwise "" plus a warning
func Resolve(raw string) string {
	trimmed := strings.TrimSpace(raw)
	lower := strings.ToLower(trimmed)
	switch {
	case !isSelector(lower):
		return trimmed
	case lower == "pool" || lower == "pool:*" || lower == "rotate":
		return resolveRotate()
	case strings.HasPrefix(lower, "bind:"):
		return resolveBind(strings.TrimSpace(trimmed[len("bind:"):]))
	default:
		// Legacy "pool:<name>" named pools no longer exist; treat as rotate.
		if name := strings.TrimSpace(trimmed[len("pool:"):]); name != "" && name != "*" {
			log.WithField("legacy_pool", name).Debug("legacy named proxy pool; treating as pool rotate")
		}
		return resolveRotate()
	}
}

// PoolProxyInfo is the read-only projection of a pool proxy for the
// management API.
type PoolProxyInfo struct {
	ID      string
	Name    string
	URL     string
	Enabled bool
}

// PoolProxies returns all pool proxies sorted by name and then URL.
func PoolProxies() []PoolProxyInfo {
	regMu.RLock()
	current := state.Load()
	infos := make([]PoolProxyInfo, 0, len(current.proxies))
	for _, proxy := range current.proxies {
		infos = append(infos, PoolProxyInfo{ID: proxy.id, Name: proxy.name, URL: proxy.url, Enabled: proxy.enabled})
	}
	regMu.RUnlock()
	sort.Slice(infos, func(i, j int) bool {
		if infos[i].Name != infos[j].Name {
			return infos[i].Name < infos[j].Name
		}
		return infos[i].URL < infos[j].URL
	})
	return infos
}
