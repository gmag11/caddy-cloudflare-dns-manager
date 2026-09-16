package cfdnsmanager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// mockTunnelAPI is an in-memory double for the Tunnel configuration API. It
// records every request so tests can assert on writes and, importantly, on the
// absence of writes.
type mockTunnelAPI struct {
	t *testing.T

	mu       sync.Mutex
	tunnelID string
	// config is the stored configuration; source mirrors the API's
	// local/cloudflare discriminator.
	config cfTunnelConfig
	source string
	// version mirrors Cloudflare bumping the version on every write.
	version int
	// failGet/failPut force an API error, for failure-isolation tests.
	failGet bool
	failPut bool
	// normalize simulates Cloudflare's server-side normalisation: it adds an
	// empty originRequest to each stored rule, which is exactly what makes a
	// naive deep-equality drift check write on every run.
	normalize bool

	calls []string
	// puts captures the decoded bodies of every write, in order.
	puts []cfTunnelConfig
}

func newMockTunnelAPI(t *testing.T, tunnelID string, seed cfTunnelConfig) *mockTunnelAPI {
	m := &mockTunnelAPI{
		t:        t,
		tunnelID: tunnelID,
		config:   seed,
		source:   "cloudflare",
	}
	if m.config.Ingress == nil {
		m.config.Ingress = []cfIngressRule{}
	}
	return m
}

// handler serves both the DNS mock's paths (unused here) and the tunnel
// configuration endpoints. It is mounted on a bare mux so tests can point the
// app's apiBase at it.
func (m *mockTunnelAPI) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/accounts/", m.handleAccount)
	return mux
}

func (m *mockTunnelAPI) server(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(m.handler())
	t.Cleanup(srv.Close)
	return srv.URL
}

// combinedServer mounts the DNS and Tunnel mocks behind one base URL. Both
// clients share App.apiBase, so a test exercising DNS and ingress together
// (failure isolation, credential absence) needs a single origin serving both
// path trees.
func combinedServer(t *testing.T, dns *mockCloudflare, tunnel *mockTunnelAPI) string {
	t.Helper()
	dnsHandler := dns.handler()
	parent := http.NewServeMux()
	parent.Handle("/zones", dnsHandler)
	parent.Handle("/zones/", dnsHandler)
	parent.Handle("/accounts/", tunnel.handler())

	srv := httptest.NewServer(parent)
	t.Cleanup(srv.Close)
	return srv.URL
}

func (m *mockTunnelAPI) handleAccount(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()

	path := r.URL.Path
	m.calls = append(m.calls, r.Method+" "+path)

	if !strings.HasSuffix(path, "/configurations") {
		http.Error(w, "unhandled "+r.Method+" "+path, 404)
		return
	}

	switch r.Method {
	case http.MethodGet:
		if m.failGet {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{
				"success": false,
				"errors":  []map[string]any{{"code": 1000, "message": "internal error"}},
			})
			return
		}
		cfg := m.config
		if m.normalize {
			cfg = normalizeConfig(cfg)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"result": map[string]any{
				"tunnel_id": m.tunnelID,
				"source":    m.source,
				"version":   m.version,
				"config":    cfg,
			},
		})

	case http.MethodPut:
		if m.failPut {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{
				"success": false,
				"errors":  []map[string]any{{"code": 1000, "message": "internal error"}},
			})
			return
		}
		var body struct {
			Config cfTunnelConfig `json:"config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		m.puts = append(m.puts, body.Config)
		m.config = body.Config
		m.version++
		json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"result": map[string]any{
				"tunnel_id": m.tunnelID,
				"source":    m.source,
				"version":   m.version,
				"config":    m.config,
			},
		})
	}
}

// normalizeConfig mirrors Cloudflare filling in defaults on stored rules.
func normalizeConfig(cfg cfTunnelConfig) cfTunnelConfig {
	out := cfg
	out.Ingress = make([]cfIngressRule, len(cfg.Ingress))
	for i, r := range cfg.Ingress {
		out.Ingress[i] = r
		if len(out.Ingress[i].OriginRequest) == 0 {
			out.Ingress[i].OriginRequest = json.RawMessage(`{}`)
		}
	}
	return out
}

// putCount returns how many writes were recorded.
func (m *mockTunnelAPI) putCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.puts)
}

// lastPut returns the most recent written configuration.
func (m *mockTunnelAPI) lastPut() cfTunnelConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.puts) == 0 {
		return cfTunnelConfig{}
	}
	return m.puts[len(m.puts)-1]
}

// storedConfig returns the configuration currently held by the mock.
func (m *mockTunnelAPI) storedConfig() cfTunnelConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.config
}

// hasCall reports whether any recorded request matches method and path suffix.
func (m *mockTunnelAPI) hasCall(method, suffix string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.calls {
		if strings.HasPrefix(c, method+" ") && strings.HasSuffix(c, suffix) {
			return true
		}
	}
	return false
}

// callCount counts recorded requests for a method.
func (m *mockTunnelAPI) callCount(method string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, c := range m.calls {
		if strings.HasPrefix(c, method+" ") {
			n++
		}
	}
	return n
}

// setSource overrides the reported configuration source.
func (m *mockTunnelAPI) setSource(source string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.source = source
}

// setIngress replaces the stored ingress rules.
func (m *mockTunnelAPI) setIngress(rules []cfIngressRule) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.Ingress = rules
}

// setFailGet forces the next GET to fail.
func (m *mockTunnelAPI) setFailGet(v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failGet = v
}

// setFailPut forces the next PUT to fail.
func (m *mockTunnelAPI) setFailPut(v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failPut = v
}

// setNormalize enables server-side normalisation of stored rules.
func (m *mockTunnelAPI) setNormalize(v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.normalize = v
}
