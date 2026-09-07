package cf_dns_manager

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
)

// adaptCaddyfile adapts input with the httpcaddyfile server type and returns
// the resulting JSON config.
func adaptCaddyfile(t *testing.T, input string) map[string]any {
	t.Helper()
	adapter := caddyfile.Adapter{ServerType: httpcaddyfile.ServerType{}}
	out, _, err := adapter.Adapt([]byte(input), nil)
	if err != nil {
		t.Fatalf("adapting Caddyfile: %v\ninput:\n%s", err, input)
	}
	var cfg map[string]any
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("decoding adapted config: %v", err)
	}
	return cfg
}

func requireAdaptErr(t *testing.T, input string, contains string) {
	t.Helper()
	adapter := caddyfile.Adapter{ServerType: httpcaddyfile.ServerType{}}
	_, _, err := adapter.Adapt([]byte(input), nil)
	if err == nil {
		t.Fatalf("expected adapt error containing %q, got none\ninput:\n%s", contains, input)
	}
	if !strings.Contains(err.Error(), contains) {
		t.Fatalf("expected adapt error containing %q, got: %v", contains, err)
	}
}
