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
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": m.records})
	case strings.HasSuffix(path, "/dns_records") && r.Method == http.MethodPost:
		var rec cfDNSRecord
		if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		m.nextID++
		rec.ID = fmt.Sprintf("rec-%d", m.nextID)
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
