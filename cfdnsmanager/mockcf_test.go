package cf_dns_manager

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// mockCloudflare is an in-memory Cloudflare API double tracking recorded calls.
type mockCloudflare struct {
	t        *testing.T
	mu       sync.Mutex
	zoneID   string
	zoneName string
	records  []cfDNSRecord
	nextID   int
	calls    []string
	// zonePrune mirrors the zone-level prune opt-in.
	zonePrune bool
}

func newMockCloudflare(t *testing.T, zoneName string, seed []cfDNSRecord) *mockCloudflare {
	m := &mockCloudflare{
		t:        t,
		zoneID:   "zone-" + zoneName,
		zoneName: zoneName,
		records:  append([]cfDNSRecord{}, seed...),
		nextID:   1000,
	}
	return m
}

func (m *mockCloudflare) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/zones", m.handleZones)
	mux.HandleFunc("/zones/", m.handleZone)
	return mux
}

func (m *mockCloudflare) handleZones(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "GET /zones")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"result":  []cfZone{{ID: m.zoneID, Name: m.zoneName}},
	})
}

func (m *mockCloudflare) handleZone(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	path := r.URL.Path
	m.calls = append(m.calls, r.Method+" "+path)

	switch {
	case strings.HasSuffix(path, "/dns_records") && r.Method == http.MethodGet:
		// Cloudflare returns record names as FQDNs; mirror that so tests cover
		// the real name shape rather than the plugin's relative form.
		out := make([]cfDNSRecord, len(m.records))
		copy(out, m.records)
		for i := range out {
			out[i].Name = m.fqdn(out[i].Name)
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": out})
	case strings.HasSuffix(path, "/dns_records") && r.Method == http.MethodPost:
		var rec cfDNSRecord
		if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		rel := m.rel(rec.Name)
		// Mirror Cloudflare's "identical record already exists" rejection so a
		// failed lookup that leads to a duplicate create is caught by tests.
		for i := range m.records {
			if m.records[i].Name == rel && m.records[i].Type == rec.Type {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{
					"success": false,
					"errors": []map[string]any{
						{"code": 81058, "message": "An identical record already exists."},
					},
				})
				return
			}
		}
		m.nextID++
		rec.ID = fmt.Sprintf("rec-%d", m.nextID)
		rec.Name = rel // store zone-relative internally
		m.records = append(m.records, rec)
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": rec})
	case strings.Contains(path, "/dns_records/") && r.Method == http.MethodPut:
		id := path[strings.LastIndex(path, "/")+1:]
		var rec cfDNSRecord
		if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		rec.ID = id
		rec.Name = m.rel(rec.Name) // store zone-relative internally
		for i := range m.records {
			if m.records[i].ID == id {
				m.records[i] = rec
				break
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": rec})
	case strings.Contains(path, "/dns_records/") && r.Method == http.MethodDelete:
		id := path[strings.LastIndex(path, "/")+1:]
		for i := range m.records {
			if m.records[i].ID == id {
				m.records = append(m.records[:i], m.records[i+1:]...)
				break
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": nil})
	default:
		http.Error(w, "unhandled "+r.Method+" "+path, 404)
	}
}

// fqdn maps a stored (relative) record name to the FQDN shape Cloudflare
// returns from its API: "@" becomes the zone apex, "foo" becomes
// "foo.<zone>". Names that are already FQDNs are returned unchanged.
func (m *mockCloudflare) fqdn(name string) string {
	if name == "@" {
		return m.zoneName
	}
	if name == "" || strings.HasSuffix(name, "."+m.zoneName) || name == m.zoneName {
		return name
	}
	return name + "." + m.zoneName
}

// rel is the inverse of fqdn: it maps a record name to the zone-relative form
// used for internal storage and lookup.
func (m *mockCloudflare) rel(name string) string {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	zone := strings.ToLower(m.zoneName)
	if name == "" || name == zone {
		return "@"
	}
	if strings.HasSuffix(name, "."+zone) {
		return strings.TrimSuffix(name[:len(name)-len(zone)], ".")
	}
	return name
}

// recordByName returns the stored record matching the given name, or nil.
func (m *mockCloudflare) recordByName(name string) *cfDNSRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.records {
		if m.records[i].Name == name {
			return &m.records[i]
		}
	}
	return nil
}

// hasCall reports whether any recorded API call exactly equals s.
func (m *mockCloudflare) hasCall(s string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.calls {
		if c == s {
			return true
		}
	}
	return false
}

func (m *mockCloudflare) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(m.handler())
	t.Cleanup(srv.Close)
	return srv
}

// startDetectionServer returns an httptest server returning ip=<publicIP>.
func startDetectionServer(t *testing.T, publicIP string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ip=" + publicIP + "\n"))
	}))
	t.Cleanup(srv.Close)
	return srv
}
