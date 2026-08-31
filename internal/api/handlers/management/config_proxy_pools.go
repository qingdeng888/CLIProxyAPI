package management

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
	log "github.com/sirupsen/logrus"
)

const (
	// proxyPoolTestMaxProxies caps the number of proxies per test request.
	proxyPoolTestMaxProxies = 64
	// proxyPoolTestTimeout bounds each per-proxy diagnostic request. This is a
	// bounded management diagnostic, modeled after the allowed management
	// APICall timeout in api_tools.go.
	proxyPoolTestTimeout = 10 * time.Second
	// proxyPoolTestConcurrency caps concurrently running per-proxy tests.
	proxyPoolTestConcurrency = 8
	// proxyPoolTestDefaultURL is the default test URL for the diagnostic endpoint.
	proxyPoolTestDefaultURL = "https://www.gstatic.com/generate_204"
	// proxyPoolTestMaxErrorLen truncates per-proxy error strings.
	proxyPoolTestMaxErrorLen = 200
	// proxyPoolBulkMaxLines caps the number of proxy_urls lines per bulk request.
	proxyPoolBulkMaxLines = 64
	// proxyPoolDefaultNamePrefix is the auto-name prefix for bulk-added proxies.
	proxyPoolDefaultNamePrefix = "proxy"
)

// proxyPoolEntryResponse is the wire format for a single pool proxy entry.
type proxyPoolEntryResponse struct {
	ID           string `json:"id"`
	Name         string `json:"name,omitempty"`
	URL          string `json:"url"`
	Enabled      bool   `json:"enabled"`
	Remark       string `json:"remark,omitempty"`
	LastCheckAt  string `json:"last_check_at,omitempty"`
	LastCheckOK  bool   `json:"last_check_ok,omitempty"`
	LastCheckMsg string `json:"last_check_msg,omitempty"`
}

// GetProxyPool returns the configured flat proxy pool with runtime statistics.
func (h *Handler) GetProxyPool(c *gin.Context) {
	h.mu.Lock()
	entries := make([]proxyPoolEntryResponse, 0, len(h.cfg.ProxyPool))
	enabledCount := 0
	for _, entry := range h.cfg.ProxyPool {
		enabled := entry.IsEnabled()
		if enabled {
			enabledCount++
		}
		entries = append(entries, proxyPoolEntryResponse{
			ID:           entry.ID,
			Name:         entry.Name,
			URL:          entry.URL,
			Enabled:      enabled,
			Remark:       entry.Remark,
			LastCheckAt:  entry.LastCheckAt,
			LastCheckOK:  entry.LastCheckOK,
			LastCheckMsg: entry.LastCheckMsg,
		})
	}
	h.mu.Unlock()

	c.JSON(http.StatusOK, gin.H{
		"proxies": entries,
		"runtime": gin.H{
			"enabled_count":  enabledCount,
			"disabled_count": len(entries) - enabledCount,
		},
	})
}

// PutProxyPool fully replaces the flat proxy pool after validating every URL.
// Client-supplied IDs are ignored; stable IDs are regenerated server-side from
// the normalized URLs.
func (h *Handler) PutProxyPool(c *gin.Context) {
	var body struct {
		Proxies []config.ProxyEntry `json:"proxies"`
	}
	if errBindJSON := c.ShouldBindJSON(&body); errBindJSON != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	next := make([]config.ProxyEntry, 0, len(body.Proxies))
	seen := make(map[string]bool, len(body.Proxies))
	for _, entry := range body.Proxies {
		trimmed := strings.TrimSpace(entry.URL)
		if trimmed == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "proxy URL must not be empty"})
			return
		}
		if _, errParse := proxyutil.Parse(trimmed); errParse != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid proxy URL: " + proxyutil.Redact(trimmed)})
			return
		}
		if seen[trimmed] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "duplicate proxy URL: " + proxyutil.Redact(trimmed)})
			return
		}
		seen[trimmed] = true
		next = append(next, config.ProxyEntry{
			Name:    strings.TrimSpace(entry.Name),
			URL:     trimmed,
			Enabled: entry.Enabled,
			Remark:  strings.TrimSpace(entry.Remark),
		})
	}
	sanitized := &config.Config{ProxyPool: next}
	sanitized.SanitizeProxyPool()

	h.mu.Lock()
	h.cfg.ProxyPool = sanitized.ProxyPool
	h.mu.Unlock()
	h.persist(c)
}

// proxyPoolBulkRequest is the body for the bulk add endpoint.
type proxyPoolBulkRequest struct {
	// ProxyURLs holds newline-separated proxy URLs; "#" comment lines are skipped.
	ProxyURLs string `json:"proxy_urls"`
	// NamePrefix is the auto-name prefix; defaults to "proxy".
	NamePrefix string `json:"name_prefix"`
	// Remark is an optional note applied to every created entry.
	Remark string `json:"remark"`
}

// proxyPoolBulkCreated is one successfully created pool entry.
type proxyPoolBulkCreated struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// proxyPoolBulkSkipped is one rejected input line. The raw line is never
// echoed back because proxy passwords may contain sensitive material.
type proxyPoolBulkSkipped struct {
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

// BulkAddProxyPool parses a newline-separated batch of proxy URLs, validates
// them, deduplicates within the batch and against the existing pool, and
// appends the survivors to the flat pool.
func (h *Handler) BulkAddProxyPool(c *gin.Context) {
	var body proxyPoolBulkRequest
	if errBindJSON := c.ShouldBindJSON(&body); errBindJSON != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	prefix := strings.TrimSpace(body.NamePrefix)
	if prefix == "" {
		prefix = proxyPoolDefaultNamePrefix
	}

	h.mu.Lock()
	existing := make(map[string]bool, len(h.cfg.ProxyPool))
	for _, entry := range h.cfg.ProxyPool {
		existing[entry.URL] = true
	}
	nextCount := len(h.cfg.ProxyPool)

	created := make([]proxyPoolBulkCreated, 0)
	skipped := make([]proxyPoolBulkSkipped, 0)
	seen := make(map[string]bool)
	for i, raw := range strings.Split(body.ProxyURLs, "\n") {
		lineNo := i + 1
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if _, errParse := proxyutil.Parse(trimmed); errParse != nil {
			skipped = append(skipped, proxyPoolBulkSkipped{Line: lineNo, Reason: "invalid proxy URL"})
			continue
		}
		if seen[trimmed] || existing[trimmed] {
			skipped = append(skipped, proxyPoolBulkSkipped{Line: lineNo, Reason: "duplicate proxy URL"})
			continue
		}
		seen[trimmed] = true
		nextCount++
		entry := config.ProxyEntry{
			Name:   fmt.Sprintf("%s-%d", prefix, nextCount),
			URL:    trimmed,
			Remark: strings.TrimSpace(body.Remark),
		}
		entry.ID = config.ProxyIDForURL(trimmed)
		h.cfg.ProxyPool = append(h.cfg.ProxyPool, entry)
		existing[trimmed] = true
		created = append(created, proxyPoolBulkCreated{ID: entry.ID, Name: entry.Name, URL: entry.URL})
	}
	h.mu.Unlock()

	// Re-sanitize so auto-names and IDs stay consistent with config load rules.
	h.mu.Lock()
	h.cfg.SanitizeProxyPool()
	// Map created entries to their final names after sanitize (IDs are stable;
	// auto-names may differ from the provisional prefix numbering when entries
	// were dropped during sanitization).
	byID := make(map[string]config.ProxyEntry, len(h.cfg.ProxyPool))
	for _, entry := range h.cfg.ProxyPool {
		byID[entry.ID] = entry
	}
	finalCreated := make([]proxyPoolBulkCreated, 0, len(created))
	for _, item := range created {
		if entry, ok := byID[item.ID]; ok {
			finalCreated = append(finalCreated, proxyPoolBulkCreated{ID: entry.ID, Name: entry.Name, URL: entry.URL})
		}
	}
	h.mu.Unlock()
	if len(skipped) > 0 {
		log.WithField("count", len(skipped)).Debug("proxy pool bulk add skipped some lines")
	}
	c.JSON(http.StatusOK, gin.H{"created": finalCreated, "skipped": skipped})
	if !h.persistQuiet(c) {
		return
	}
}

// proxyPoolStatusRequest is the body for the batch enable/disable endpoint.
type proxyPoolStatusRequest struct {
	// IDs lists the pool proxy entity IDs to update.
	IDs []string `json:"ids"`
	// Enabled is the target state for every listed ID.
	Enabled *bool `json:"enabled"`
}

// PutProxyPoolStatus batch-enables or disables pool proxies by ID.
func (h *Handler) PutProxyPoolStatus(c *gin.Context) {
	var body proxyPoolStatusRequest
	if errBindJSON := c.ShouldBindJSON(&body); errBindJSON != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	if body.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "enabled must be a boolean"})
		return
	}
	if len(body.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ids must not be empty"})
		return
	}
	target := make(map[string]bool, len(body.IDs))
	for _, id := range body.IDs {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			target[trimmed] = true
		}
	}

	h.mu.Lock()
	changed := 0
	missing := make(map[string]bool, len(target))
	for id := range target {
		missing[id] = true
	}
	for i := range h.cfg.ProxyPool {
		if !target[h.cfg.ProxyPool[i].ID] {
			continue
		}
		delete(missing, h.cfg.ProxyPool[i].ID)
		if h.cfg.ProxyPool[i].IsEnabled() != *body.Enabled {
			enabled := *body.Enabled
			h.cfg.ProxyPool[i].Enabled = &enabled
		}
		changed++
	}
	h.mu.Unlock()

	c.JSON(http.StatusOK, gin.H{
		"changed":   changed,
		"requested": len(target),
		"missing":   len(missing),
	})
	if !h.persistQuiet(c) {
		return
	}
}

// DeleteProxyPoolEntry removes a single pool proxy by its stable ID.
func (h *Handler) DeleteProxyPoolEntry(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing proxy id"})
		return
	}
	h.mu.Lock()
	remaining := make([]config.ProxyEntry, 0, len(h.cfg.ProxyPool))
	found := false
	for _, entry := range h.cfg.ProxyPool {
		if entry.ID == id {
			found = true
			continue
		}
		remaining = append(remaining, entry)
	}
	if !found {
		h.mu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": "proxy not found"})
		return
	}
	h.cfg.ProxyPool = remaining
	h.mu.Unlock()
	h.persistQuiet(c)
}

// proxyPoolTestRequest is the request body for the batch proxy test endpoint.
// Exactly one of IDs or URLs identifies the proxies to test; a single "id"
// field is also accepted for convenience.
type proxyPoolTestRequest struct {
	// IDs lists pool proxy entity IDs to test (must exist in the pool).
	IDs []string `json:"ids"`
	// URLs lists raw proxy URLs to test; "direct" tests without a proxy.
	URLs []string `json:"urls"`
	// ID tests a single pool proxy by entity ID.
	ID string `json:"id"`
	// TestURL is an optional HTTP target; defaults to proxyPoolTestDefaultURL.
	TestURL string `json:"test-url"`
}

// proxyPoolTestResult is the per-proxy outcome of a batch test.
type proxyPoolTestResult struct {
	URL       string `json:"url"`
	OK        bool   `json:"ok"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error"`
}

// testProxyURL performs a bounded GET against testURL through the given proxy
// setting and reports whether any 2xx/3xx (including 204) response arrived.
func testProxyURL(raw, testURL string) (bool, int64, string) {
	transport, _, errBuildTransport := proxyutil.BuildHTTPTransport(raw)
	if errBuildTransport != nil {
		return false, 0, fmt.Sprintf("invalid proxy: %v", errBuildTransport)
	}
	if transport == nil {
		transport = proxyutil.NewDirectTransport()
	}
	// Bounded management diagnostic timeout (same allowance as the management
	// APICall timeout in api_tools.go).
	client := &http.Client{
		Timeout:   proxyPoolTestTimeout,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			// Accept any response; redirects resolve on their own and 3xx counts as success anyway.
			return http.ErrUseLastResponse
		},
	}
	start := time.Now()
	req, errNewRequest := http.NewRequest(http.MethodGet, testURL, nil)
	if errNewRequest != nil {
		return false, 0, fmt.Sprintf("build request: %v", errNewRequest)
	}
	resp, errDo := client.Do(req)
	latency := time.Since(start).Milliseconds()
	if errDo != nil {
		return false, latency, errDo.Error()
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.WithFields(log.Fields{"proxy": proxyutil.Redact(raw)}).WithError(errClose).Debug("proxy pool test: close response body failed")
		}
	}()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return false, latency, fmt.Sprintf("unexpected status code %d", resp.StatusCode)
	}
	return true, latency, ""
}

// collectProxyPoolTestTargets resolves the request into the ordered list of
// proxy URLs to test plus the pool entry indexes that results should be
// recorded onto (by id or url), or nil when the request is invalid. The caller
// must hold h.mu.
func (h *Handler) collectProxyPoolTestTargets(body proxyPoolTestRequest) ([]string, map[int]int, string) {
	targets := make([]string, 0)
	if body.ID != "" {
		body.IDs = append(body.IDs, body.ID)
	}
	singleURL := len(body.IDs) == 0 && len(body.URLs) == 1
	urls := body.URLs
	if len(body.IDs) > 0 {
		if len(urls) > 0 {
			return nil, nil, "provide either ids or urls, not both"
		}
		urls = make([]string, 0, len(body.IDs))
		for _, id := range body.IDs {
			trimmedID := strings.TrimSpace(id)
			found := false
			for _, entry := range h.cfg.ProxyPool {
				if entry.ID == trimmedID {
					urls = append(urls, entry.URL)
					found = true
					break
				}
			}
			if !found {
				return nil, nil, "unknown proxy id: " + trimmedID
			}
		}
	}
	if len(urls) == 0 {
		return nil, nil, "ids or urls must not be empty"
	}
	if len(urls) > proxyPoolTestMaxProxies {
		return nil, nil, fmt.Sprintf("too many proxies: %d (max %d)", len(urls), proxyPoolTestMaxProxies)
	}

	matches := make(map[int]int, len(urls))
	for _, raw := range urls {
		trimmed := strings.TrimSpace(raw)
		targets = append(targets, trimmed)
		for i, entry := range h.cfg.ProxyPool {
			if entry.URL == trimmed || (singleURL && entry.ID == trimmed) {
				matches[len(targets)-1] = i
				break
			}
		}
	}
	return targets, matches, ""
}

// TestProxyPool tests a batch of proxies concurrently, records the outcome on
// matching pool entries (so results survive reload), and returns one result per
// input, in input order.
func (h *Handler) TestProxyPool(c *gin.Context) {
	var body proxyPoolTestRequest
	if errBindJSON := c.ShouldBindJSON(&body); errBindJSON != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	testURL := strings.TrimSpace(body.TestURL)
	if testURL == "" {
		testURL = proxyPoolTestDefaultURL
	}
	if !strings.HasPrefix(testURL, "http://") && !strings.HasPrefix(testURL, "https://") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "test-url must be an http or https URL"})
		return
	}

	h.mu.Lock()
	targets, matches, message := h.collectProxyPoolTestTargets(body)
	h.mu.Unlock()
	if message != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": message})
		return
	}

	results := make([]proxyPoolTestResult, len(targets))
	sem := make(chan struct{}, proxyPoolTestConcurrency)
	var wg sync.WaitGroup
	for i, raw := range targets {
		wg.Add(1)
		go func(idx int, proxyRaw string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ok, latency, errMsg := testProxyURL(proxyRaw, testURL)
			if len(errMsg) > proxyPoolTestMaxErrorLen {
				errMsg = errMsg[:proxyPoolTestMaxErrorLen]
			}
			results[idx] = proxyPoolTestResult{URL: proxyRaw, OK: ok, LatencyMS: latency, Error: errMsg}
		}(i, raw)
	}
	wg.Wait()

	// Persist last-check outcomes onto matching pool entries so they survive
	// config reloads. matchProxyPoolEntry re-resolves indexes because the
	// concurrent test phase does not hold h.mu.
	h.mu.Lock()
	now := time.Now().UTC().Format(time.RFC3339)
	for i, result := range results {
		idx, okMatch := matches[i]
		if !okMatch || idx < 0 || idx >= len(h.cfg.ProxyPool) {
			continue
		}
		h.cfg.ProxyPool[idx].LastCheckAt = now
		h.cfg.ProxyPool[idx].LastCheckOK = result.OK
		h.cfg.ProxyPool[idx].LastCheckMsg = result.Error
	}
	h.mu.Unlock()
	c.JSON(http.StatusOK, gin.H{"results": results})
	if !h.persistQuiet(c) {
		return
	}
}
