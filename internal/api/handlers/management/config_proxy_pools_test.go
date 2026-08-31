package management

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

// setupProxyPoolRouter returns a handler wired with a temp config file and a
// router registering the new proxy-pool management routes.
func setupProxyPoolRouter(t *testing.T) (*Handler, *gin.Engine) {
	t.Helper()
	h := &Handler{cfg: &config.Config{}, configFilePath: writeTestConfigFile(t)}
	router := gin.New()
	router.GET("/proxy-pool", h.GetProxyPool)
	router.PUT("/proxy-pool", h.PutProxyPool)
	router.POST("/proxy-pool/bulk", h.BulkAddProxyPool)
	router.PUT("/proxy-pool/status", h.PutProxyPoolStatus)
	router.POST("/proxy-pool/test", h.TestProxyPool)
	router.DELETE("/proxy-pool/:id", h.DeleteProxyPoolEntry)
	return h, router
}

func doJSON(router *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func TestProxyPoolGetPutRoundTrip(t *testing.T) {
	t.Parallel()

	_, router := setupProxyPoolRouter(t)

	body := `{"proxies":[{"name":"px-1","url":"socks5://1.2.3.4:1080"},{"url":"http://5.6.7.8:8080","enabled":false,"remark":"backup"}]}`
	recorder := doJSON(router, http.MethodPut, "/proxy-pool", body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	recorder = doJSON(router, http.MethodGet, "/proxy-pool", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var response struct {
		Proxies []proxyPoolEntryResponse `json:"proxies"`
		Runtime struct {
			EnabledCount  int `json:"enabled_count"`
			DisabledCount int `json:"disabled_count"`
		} `json:"runtime"`
	}
	if errDecode := json.NewDecoder(recorder.Body).Decode(&response); errDecode != nil {
		t.Fatalf("decode response: %v", errDecode)
	}
	if len(response.Proxies) != 2 {
		t.Fatalf("expected 2 proxies, got %d: %s", len(response.Proxies), recorder.Body.String())
	}
	if response.Runtime.EnabledCount != 1 || response.Runtime.DisabledCount != 1 {
		t.Fatalf("unexpected runtime stats: %+v", response.Runtime)
	}
	first := response.Proxies[0]
	if first.ID == "" || first.Name != "px-1" || first.URL != "socks5://1.2.3.4:1080" || !first.Enabled {
		t.Fatalf("unexpected first entry: %+v", first)
	}
	second := response.Proxies[1]
	if second.Enabled || second.Remark != "backup" || second.Name == "" {
		t.Fatalf("unexpected second entry: %+v", second)
	}
}

func TestProxyPoolPutRejectsInvalidURL(t *testing.T) {
	t.Parallel()

	_, router := setupProxyPoolRouter(t)

	recorder := doJSON(router, http.MethodPut, "/proxy-pool", `{"proxies":[{"url":"not-a-proxy"}]}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
}

func TestProxyPoolPutIgnoresClientIDs(t *testing.T) {
	t.Parallel()

	h, router := setupProxyPoolRouter(t)

	recorder := doJSON(router, http.MethodPut, "/proxy-pool", `{"proxies":[{"id":"client-forged","url":"http://1.2.3.4:8080"}]}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("PUT status = %d; body = %s", recorder.Code, recorder.Body.String())
	}
	if h.cfg.ProxyPool[0].ID != config.ProxyIDForURL("http://1.2.3.4:8080") {
		t.Fatalf("expected server-derived ID, got %q", h.cfg.ProxyPool[0].ID)
	}
}

func TestProxyPoolBulkAdd(t *testing.T) {
	t.Parallel()

	h, router := setupProxyPoolRouter(t)
	// Seed an existing entry to verify dedupe against the current pool.
	h.cfg.ProxyPool = []config.ProxyEntry{{ID: config.ProxyIDForURL("http://9.9.9.9:8080"), Name: "px-1", URL: "http://9.9.9.9:8080"}}

	body := "http://1.2.3.4:8080\n" +
		"\n" +
		"# a full-line comment\n" +
		"socks5://5.6.7.8:1080\n" +
		"not-a-proxy\n" +
		"http://1.2.3.4:8080\n" +
		"http://9.9.9.9:8080\n"
	recorder := doJSON(router, http.MethodPost, "/proxy-pool/bulk", `{"proxy_urls":`+jsonQuote(body)+`,"name_prefix":"px","remark":"batch"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", recorder.Code, recorder.Body.String())
	}

	var response struct {
		Created []proxyPoolBulkCreated `json:"created"`
		Skipped []proxyPoolBulkSkipped `json:"skipped"`
	}
	if errDecode := json.NewDecoder(recorder.Body).Decode(&response); errDecode != nil {
		t.Fatalf("decode response: %v", errDecode)
	}
	if len(response.Created) != 2 {
		t.Fatalf("expected 2 created, got %+v", response.Created)
	}
	// Line 5 invalid, line 6 in-batch dupe, line 7 existing-pool dupe.
	if len(response.Skipped) != 3 {
		t.Fatalf("expected 3 skipped, got %+v", response.Skipped)
	}
	if response.Skipped[0].Line != 5 || response.Skipped[1].Line != 6 || response.Skipped[2].Line != 7 {
		t.Fatalf("unexpected skipped lines: %+v", response.Skipped)
	}
	for _, item := range response.Skipped {
		if strings.Contains(item.Reason, "http") || strings.Contains(item.Reason, "proxy") && strings.Contains(item.Reason, "://") {
			t.Fatalf("skipped reason must not echo the raw line: %q", item.Reason)
		}
	}
	if response.Created[0].Name != "px-2" || response.Created[1].Name != "px-3" {
		t.Fatalf("expected ordinals continuing from existing count, got %+v", response.Created)
	}
	if !strings.Contains(recorder.Body.String(), "not-a-proxy") && len(response.Created) > 0 {
		if strings.Contains(recorder.Body.String(), "batch") == false && h.cfg.ProxyPool[1].Remark != "batch" {
			t.Fatalf("expected remark applied to created entries")
		}
	}
	for _, entry := range h.cfg.ProxyPool {
		if entry.ID != config.ProxyIDForURL(entry.URL) {
			t.Fatalf("entry ID %q does not match derived ID for %q", entry.ID, entry.URL)
		}
	}
}

// jsonQuote encodes a string as a JSON string literal.
func jsonQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func TestProxyPoolStatusBatch(t *testing.T) {
	t.Parallel()

	h, router := setupProxyPoolRouter(t)
	cfg := &config.Config{ProxyPool: []config.ProxyEntry{
		{Name: "px-1", URL: "http://1.1.1.1:8080"},
		{Name: "px-2", URL: "http://2.2.2.2:8080"},
	}}
	cfg.SanitizeProxyPool()
	h.cfg = cfg

	disabled := false
	body := `{"ids":["` + cfg.ProxyPool[0].ID + `","missing01"],"enabled":false,"enabled2":null}`
	body = strings.Replace(body, `,"enabled2":null`, "", 1)
	recorder := doJSON(router, http.MethodPut, "/proxy-pool/status", body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", recorder.Code, recorder.Body.String())
	}

	var response struct {
		Changed   int `json:"changed"`
		Requested int `json:"requested"`
		Missing   int `json:"missing"`
	}
	if errDecode := json.NewDecoder(recorder.Body).Decode(&response); errDecode != nil {
		t.Fatalf("decode response: %v", errDecode)
	}
	if response.Changed != 1 || response.Requested != 2 || response.Missing != 1 {
		t.Fatalf("unexpected counts: %+v", response)
	}
	if h.cfg.ProxyPool[0].IsEnabled() {
		t.Fatal("expected the targeted proxy to be disabled")
	}
	_ = disabled
}

func TestProxyPoolDeleteByID(t *testing.T) {
	t.Parallel()

	h, router := setupProxyPoolRouter(t)
	cfg := &config.Config{ProxyPool: []config.ProxyEntry{{Name: "px-1", URL: "http://1.1.1.1:8080"}}}
	cfg.SanitizeProxyPool()
	h.cfg = cfg

	recorder := doJSON(router, http.MethodDelete, "/proxy-pool/"+cfg.ProxyPool[0].ID, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", recorder.Code, recorder.Body.String())
	}
	if len(h.cfg.ProxyPool) != 0 {
		t.Fatalf("expected the entry to be removed, got %+v", h.cfg.ProxyPool)
	}

	recorder = doJSON(router, http.MethodDelete, "/proxy-pool/unknown0", "")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown id status = %d, want 404", recorder.Code)
	}
}

func TestProxyPoolTestEndpointPersistsLastCheck(t *testing.T) {
	t.Parallel()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	h, router := setupProxyPoolRouter(t)
	cfg := &config.Config{ProxyPool: []config.ProxyEntry{{Name: "px-1", URL: "http://127.0.0.1:1"}}}
	cfg.SanitizeProxyPool()
	h.cfg = cfg

	recorder := doJSON(router, http.MethodPost, "/proxy-pool/test",
		`{"ids":["`+cfg.ProxyPool[0].ID+`"],"urls":["direct"],"test-url":"`+target.URL+`"}`)
	// Mixed ids+urls must be rejected.
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for ids+urls mix, got %d; body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = doJSON(router, http.MethodPost, "/proxy-pool/test",
		`{"ids":["`+cfg.ProxyPool[0].ID+`"],"test-url":"`+target.URL+`"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", recorder.Code, recorder.Body.String())
	}

	var response struct {
		Results []proxyPoolTestResult `json:"results"`
	}
	if errDecode := json.NewDecoder(recorder.Body).Decode(&response); errDecode != nil {
		t.Fatalf("decode response: %v", errDecode)
	}
	if len(response.Results) != 1 {
		t.Fatalf("expected 1 result, got %d: %s", len(response.Results), recorder.Body.String())
	}
	if response.Results[0].URL != "http://127.0.0.1:1" || response.Results[0].OK {
		t.Fatalf("unexpected result: %+v", response.Results[0])
	}
	if h.cfg.ProxyPool[0].LastCheckAt == "" || h.cfg.ProxyPool[0].LastCheckOK || h.cfg.ProxyPool[0].LastCheckMsg == "" {
		t.Fatalf("expected last_check fields persisted, got %+v", h.cfg.ProxyPool[0])
	}
}

func TestProxyPoolTestEndpointURLFormAndFailurePersist(t *testing.T) {
	t.Parallel()

	h, router := setupProxyPoolRouter(t)
	cfg := &config.Config{ProxyPool: []config.ProxyEntry{{Name: "px-1", URL: "http://127.0.0.1:1"}}}
	cfg.SanitizeProxyPool()
	h.cfg = cfg

	recorder := doJSON(router, http.MethodPost, "/proxy-pool/test", `{"urls":["direct","http://127.0.0.1:1"]}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", recorder.Code, recorder.Body.String())
	}

	var response struct {
		Results []proxyPoolTestResult `json:"results"`
	}
	if errDecode := json.NewDecoder(recorder.Body).Decode(&response); errDecode != nil {
		t.Fatalf("decode response: %v", errDecode)
	}
	if len(response.Results) != 2 {
		t.Fatalf("expected 2 results, got %d: %s", len(response.Results), recorder.Body.String())
	}
	direct := response.Results[0]
	if direct.URL != "direct" || !direct.OK || direct.Error != "" {
		t.Fatalf("unexpected direct result: %+v", direct)
	}
	bogus := response.Results[1]
	if bogus.URL != "http://127.0.0.1:1" || bogus.OK || bogus.Error == "" {
		t.Fatalf("expected bogus proxy to fail, got: %+v", bogus)
	}
	if h.cfg.ProxyPool[0].LastCheckAt == "" || h.cfg.ProxyPool[0].LastCheckOK || h.cfg.ProxyPool[0].LastCheckMsg == "" {
		t.Fatalf("expected failure last_check fields persisted, got %+v", h.cfg.ProxyPool[0])
	}
}

func TestProxyPoolTestEndpointRejectsTooManyProxies(t *testing.T) {
	t.Parallel()

	_, router := setupProxyPoolRouter(t)

	quoted := make([]string, proxyPoolTestMaxProxies+1)
	for i := range quoted {
		quoted[i] = `"direct"`
	}
	body := `{"urls":[` + strings.Join(quoted, ",") + `]}`
	recorder := doJSON(router, http.MethodPost, "/proxy-pool/test", body)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
}

func TestProxyPoolTestEndpointRejectsEmptyRequest(t *testing.T) {
	t.Parallel()

	_, router := setupProxyPoolRouter(t)

	recorder := doJSON(router, http.MethodPost, "/proxy-pool/test", `{}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
}

func TestProxyPoolTestEndpointUnknownID(t *testing.T) {
	t.Parallel()

	h, router := setupProxyPoolRouter(t)

	recorder := doJSON(router, http.MethodPost, "/proxy-pool/test", `{"id":"unknown00"}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if len(h.cfg.ProxyPool) != 0 {
		t.Fatal("expected no pool entries")
	}
}
