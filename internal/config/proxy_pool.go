package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
	log "github.com/sirupsen/logrus"
)

// ProxyIDForURL derives the stable entity ID for a proxy URL: the first 12 hex
// chars of the sha256 digest of the normalized URL. IDs survive config hot
// reloads as long as the URL is unchanged.
func ProxyIDForURL(normalizedURL string) string {
	sum := sha256.Sum256([]byte(normalizedURL))
	return hex.EncodeToString(sum[:])[:12]
}

// SanitizeProxyPool normalizes the flat proxy-pool configuration in place: it
// trims fields, validates URLs with proxyutil.Parse, drops invalid entries with
// a warning, deduplicates by normalized URL (first wins), auto-names empty
// names as "proxy-<n>" using the entry ordinal, and assigns each entry a
// deterministic ID. Disabled entries are kept, not dropped.
func (cfg *Config) SanitizeProxyPool() {
	if cfg == nil || len(cfg.ProxyPool) == 0 {
		return
	}
	sanitized := make([]ProxyEntry, 0, len(cfg.ProxyPool))
	seen := make(map[string]bool, len(cfg.ProxyPool))
	for i, entry := range cfg.ProxyPool {
		name := strings.TrimSpace(entry.Name)
		trimmed := strings.TrimSpace(entry.URL)
		if trimmed == "" {
			log.WithField("index", i).Warn("dropping proxy pool entry without a URL")
			continue
		}
		if _, errParse := proxyutil.Parse(trimmed); errParse != nil {
			log.WithFields(log.Fields{"index": i, "proxy": proxyutil.Redact(trimmed)}).Warn("dropping invalid proxy pool entry")
			continue
		}
		if seen[trimmed] {
			log.WithFields(log.Fields{"proxy": proxyutil.Redact(trimmed)}).Warn("duplicate proxy in pool; keeping the first entry")
			continue
		}
		seen[trimmed] = true
		if name == "" {
			name = fmt.Sprintf("proxy-%d", i+1)
		}
		sanitized = append(sanitized, ProxyEntry{
			ID:      ProxyIDForURL(trimmed),
			Name:    name,
			URL:     trimmed,
			Enabled: entry.Enabled,
			Remark:  strings.TrimSpace(entry.Remark),
		})
	}
	cfg.ProxyPool = sanitized
}
