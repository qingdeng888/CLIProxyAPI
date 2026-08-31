package proxypool

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestResolveRotateRoundRobin(t *testing.T) {
	cfg := &config.Config{
		ProxyPool: []config.ProxyEntry{
			{Name: "px-1", URL: "socks5://1.2.3.4:1080"},
			{Name: "px-2", URL: "http://5.6.7.8:8080"},
		},
	}
	Sync(cfg)

	want := []string{"socks5://1.2.3.4:1080", "http://5.6.7.8:8080", "socks5://1.2.3.4:1080"}
	for _, selector := range []string{"pool", "pool:*", "rotate"} {
		// Sync resets the round-robin counter so each selector starts fresh.
		Sync(cfg)
		for i, expected := range want {
			if got := Resolve(selector); got != expected {
				t.Fatalf("Resolve(%q) call %d = %q, want %q", selector, i+1, got, expected)
			}
		}
	}
}

func TestResolveLegacyPoolNameTreatedAsRotate(t *testing.T) {
	Sync(&config.Config{
		ProxyPool: []config.ProxyEntry{{Name: "p1", URL: "http://1.1.1.1:8080"}},
	})

	for _, raw := range []string{"pool:p1", "POOL:p1", "Pool:p1", " pool:p1 ", "pool:anything"} {
		if got := Resolve(raw); got != "http://1.1.1.1:8080" {
			t.Fatalf("Resolve(%q) = %q, want http://1.1.1.1:8080", raw, got)
		}
	}
}

func TestResolveEmptyPoolReturnsEmpty(t *testing.T) {
	Sync(&config.Config{})

	for _, raw := range []string{"pool", "rotate", "pool:*"} {
		if got := Resolve(raw); got != "" {
			t.Fatalf("Resolve(%q) = %q, want empty string for empty pool", raw, got)
		}
	}
}

func TestResolveBindByID(t *testing.T) {
	cfg := &config.Config{
		ProxyPool: []config.ProxyEntry{
			{Name: "px-1", URL: "http://1.1.1.1:8080"},
			{Name: "px-2", URL: "http://2.2.2.2:8080"},
		},
	}
	cfg.SanitizeProxyPool()
	Sync(cfg)

	id := cfg.ProxyPool[1].ID
	for _, raw := range []string{"bind:" + id, "BIND:" + id, " bind:" + id + " "} {
		if got := Resolve(raw); got != "http://2.2.2.2:8080" {
			t.Fatalf("Resolve(%q) = %q, want http://2.2.2.2:8080", raw, got)
		}
	}
}

func TestResolveBindByURL(t *testing.T) {
	cfg := &config.Config{
		ProxyPool: []config.ProxyEntry{{Name: "px-1", URL: "http://1.1.1.1:8080"}},
	}
	cfg.SanitizeProxyPool()
	Sync(cfg)

	if got := Resolve("bind:http://1.1.1.1:8080"); got != "http://1.1.1.1:8080" {
		t.Fatalf("Resolve(bind:url) = %q, want http://1.1.1.1:8080", got)
	}
}

func TestResolveBindUnknownOrDisabledFallsBackToEmpty(t *testing.T) {
	disabled := false
	cfg := &config.Config{
		ProxyPool: []config.ProxyEntry{
			{Name: "off", URL: "http://1.1.1.1:8080", Enabled: &disabled},
		},
	}
	cfg.SanitizeProxyPool()
	Sync(cfg)

	for _, raw := range []string{"bind:unknown", "bind:http://1.1.1.1:8080", "bind:" + cfg.ProxyPool[0].ID, "bind:"} {
		if got := Resolve(raw); got != "" {
			t.Fatalf("Resolve(%q) = %q, want empty fallback", raw, got)
		}
	}
}

func TestResolvePassthroughIdentity(t *testing.T) {
	Sync(&config.Config{})

	cases := map[string]string{
		"":                          "",
		"  ":                        "",
		"direct":                    "direct",
		"none":                      "none",
		"DIRECT":                    "DIRECT",
		"socks5://u:p@1.2.3.4:1080": "socks5://u:p@1.2.3.4:1080",
		"  http://5.6.7.8:8080  ":   "http://5.6.7.8:8080",
		"poolhouse":                 "poolhouse",
	}
	for raw, want := range cases {
		if got := Resolve(raw); got != want {
			t.Fatalf("Resolve(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestSyncNilConfigClearsRegistry(t *testing.T) {
	Sync(&config.Config{
		ProxyPool: []config.ProxyEntry{{Name: "p", URL: "http://1.1.1.1:8080"}},
	})
	Sync(nil)

	if got := Resolve("pool"); got != "" {
		t.Fatalf("Resolve(pool) after Sync(nil) = %q, want empty", got)
	}
	if proxies := PoolProxies(); len(proxies) != 0 {
		t.Fatalf("PoolProxies() after Sync(nil) = %v, want empty", proxies)
	}
}

func TestResolveSkipsDisabledProxies(t *testing.T) {
	disabled := false
	enabled := true
	cfg := &config.Config{
		ProxyPool: []config.ProxyEntry{
			{Name: "px-1", URL: "http://1.1.1.1:8080"},
			{Name: "px-2", URL: "http://2.2.2.2:8080", Enabled: &disabled},
			{Name: "px-3", URL: "http://3.3.3.3:8080", Enabled: &enabled},
		},
	}
	Sync(cfg)

	// The counter advances for every call; disabled entries are skipped in the
	// forward scan, so the observed sequence is 1,3,3,1,3,3.
	want := []string{"http://1.1.1.1:8080", "http://3.3.3.3:8080", "http://3.3.3.3:8080", "http://1.1.1.1:8080", "http://3.3.3.3:8080", "http://3.3.3.3:8080"}
	for i, expected := range want {
		if got := Resolve("pool"); got != expected {
			t.Fatalf("Resolve() call %d = %q, want %q", i+1, got, expected)
		}
	}
}

func TestResolveAllDisabledPoolReturnsEmpty(t *testing.T) {
	disabled := false
	Sync(&config.Config{
		ProxyPool: []config.ProxyEntry{
			{Name: "px-1", URL: "http://1.1.1.1:8080", Enabled: &disabled},
			{Name: "px-2", URL: "http://2.2.2.2:8080", Enabled: &disabled},
		},
	})

	for i := 0; i < 3; i++ {
		if got := Resolve("pool"); got != "" {
			t.Fatalf("Resolve() call %d = %q, want empty for all-disabled pool", i+1, got)
		}
	}
}

func TestSyncPreservesEnabledFlagsAndIDs(t *testing.T) {
	disabled := false
	cfg := &config.Config{
		ProxyPool: []config.ProxyEntry{
			{Name: "px-1", ID: "aaaa1111aaaa", URL: "http://1.1.1.1:8080"},
			{Name: "px-2", ID: "bbbb2222bbbb", URL: "http://2.2.2.2:8080", Enabled: &disabled},
		},
	}
	Sync(cfg)

	regMu.RLock()
	current := state.Load()
	proxies := current.proxies
	regMu.RUnlock()
	if len(proxies) != 2 {
		t.Fatalf("expected 2 pool proxies, got %d", len(proxies))
	}
	if proxies[0].url != "http://1.1.1.1:8080" || !proxies[0].enabled || proxies[0].id != "aaaa1111aaaa" {
		t.Fatalf("unexpected first pool proxy: %+v", proxies[0])
	}
	if proxies[1].url != "http://2.2.2.2:8080" || proxies[1].enabled || proxies[1].id != "bbbb2222bbbb" {
		t.Fatalf("unexpected second pool proxy: %+v", proxies[1])
	}
}

func TestPoolProxiesSortedByNameThenURL(t *testing.T) {
	Sync(&config.Config{
		ProxyPool: []config.ProxyEntry{
			{Name: "zebra", URL: "http://1.1.1.1:8080"},
			{Name: "alpha", URL: "http://3.3.3.3:8080"},
			{Name: "alpha", URL: "http://2.2.2.2:8080"},
		},
	})

	infos := PoolProxies()
	if len(infos) != 3 {
		t.Fatalf("PoolProxies() length = %d, want 3", len(infos))
	}
	if infos[0].Name != "alpha" || infos[0].URL != "http://2.2.2.2:8080" {
		t.Fatalf("unexpected first entry: %+v", infos[0])
	}
	if infos[1].Name != "alpha" || infos[1].URL != "http://3.3.3.3:8080" {
		t.Fatalf("unexpected second entry: %+v", infos[1])
	}
	if infos[2].Name != "zebra" {
		t.Fatalf("unexpected third entry: %+v", infos[2])
	}
}
