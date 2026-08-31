package config

import (
	"testing"
)

func TestSanitizeProxyPool_TrimsDropsAndDedupes(t *testing.T) {
	cfg := &Config{
		ProxyPool: []ProxyEntry{
			// Empty URL should be dropped.
			{Name: "empty-url"},
			// Invalid proxy URLs should be dropped with a warning.
			{Name: "bad-url", URL: "not-a-proxy"},
			{Name: "no-scheme", URL: "1.2.3.4:1080"},
			// Whitespace is trimmed; the valid entry survives.
			{Name: "  padded  ", URL: "  socks5://1.2.3.4:1080  ", Remark: "  note  "},
			// Duplicate URL (after normalization): first wins.
			{Name: "dupe", URL: "socks5://1.2.3.4:1080"},
		},
	}
	cfg.SanitizeProxyPool()

	if len(cfg.ProxyPool) != 1 {
		t.Fatalf("expected 1 sanitized entry, got %d: %+v", len(cfg.ProxyPool), cfg.ProxyPool)
	}
	entry := cfg.ProxyPool[0]
	if entry.Name != "padded" || entry.URL != "socks5://1.2.3.4:1080" || entry.Remark != "note" {
		t.Fatalf("unexpected sanitized entry: %+v", entry)
	}
	if entry.ID == "" || len(entry.ID) != 12 {
		t.Fatalf("expected a 12-char ID, got %q", entry.ID)
	}
	if entry.ID != ProxyIDForURL("socks5://1.2.3.4:1080") {
		t.Fatalf("ID %q does not match derived ID for the normalized URL", entry.ID)
	}
}

func TestSanitizeProxyPool_AutoNamesUseOrdinal(t *testing.T) {
	cfg := &Config{
		ProxyPool: []ProxyEntry{
			{URL: "http://1.1.1.1:8080"},
			{Name: "custom", URL: "http://2.2.2.2:8080"},
			{URL: "http://3.3.3.3:8080"},
		},
	}
	cfg.SanitizeProxyPool()

	if cfg.ProxyPool[0].Name != "proxy-1" {
		t.Fatalf("expected auto-name proxy-1, got %s", cfg.ProxyPool[0].Name)
	}
	if cfg.ProxyPool[1].Name != "custom" {
		t.Fatalf("expected explicit name custom, got %s", cfg.ProxyPool[1].Name)
	}
	// The ordinal follows the entry position, even after a named entry.
	if cfg.ProxyPool[2].Name != "proxy-3" {
		t.Fatalf("expected auto-name proxy-3, got %s", cfg.ProxyPool[2].Name)
	}
}

func TestSanitizeProxyPool_StableIDsAcrossReload(t *testing.T) {
	first := &Config{ProxyPool: []ProxyEntry{{Name: "a", URL: "socks5://1.2.3.4:1080"}, {URL: "http://5.6.7.8:8080"}}}
	first.SanitizeProxyPool()

	// A second sanitize of the same content must produce identical IDs.
	second := &Config{ProxyPool: []ProxyEntry{{Name: "a", URL: "socks5://1.2.3.4:1080"}, {URL: "http://5.6.7.8:8080"}}}
	second.SanitizeProxyPool()

	for i := range first.ProxyPool {
		if first.ProxyPool[i].ID != second.ProxyPool[i].ID {
			t.Fatalf("ID mismatch for entry %d: %s vs %s", i, first.ProxyPool[i].ID, second.ProxyPool[i].ID)
		}
	}
	if first.ProxyPool[0].ID == first.ProxyPool[1].ID {
		t.Fatal("distinct URLs must derive distinct IDs")
	}
}

func TestSanitizeProxyPool_KeepsDisabledEntries(t *testing.T) {
	disabled := false
	cfg := &Config{ProxyPool: []ProxyEntry{{Name: "off", URL: "http://1.1.1.1:8080", Enabled: &disabled}}}
	cfg.SanitizeProxyPool()

	if len(cfg.ProxyPool) != 1 {
		t.Fatalf("expected disabled entry to be kept, got %+v", cfg.ProxyPool)
	}
	if cfg.ProxyPool[0].IsEnabled() {
		t.Fatal("disabled entry must remain disabled")
	}
}

func TestSanitizeProxyPool_NilAndEmpty(t *testing.T) {
	var nilCfg *Config
	nilCfg.SanitizeProxyPool() // must not panic

	cfg := &Config{}
	cfg.SanitizeProxyPool()
	if cfg.ProxyPool != nil {
		t.Fatalf("expected nil proxy pool, got %+v", cfg.ProxyPool)
	}
}

func TestProxyIDForURL_KnownVector(t *testing.T) {
	// sha256("http://1.2.3.4:8080") starts with fe2293703308...
	got := ProxyIDForURL("http://1.2.3.4:8080")
	if got != "fe2293703308" {
		t.Fatalf("ProxyIDForURL() = %q, want fe2293703308", got)
	}
}
