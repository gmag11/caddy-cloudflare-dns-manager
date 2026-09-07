package cf_dns_manager

import (
	"os"
	"testing"

	"github.com/caddyserver/caddy/v2"
	cf_caddyfile "github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	_ "github.com/caddyserver/caddy/v2/modules/standard"
)

func TestValidateWiring(t *testing.T) {
	os.Setenv("CF_EXAMPLE", "testtoken")
	defer os.Unsetenv("CF_EXAMPLE")

	input := `{
	cf_dns_manager {
		zone example.com api_token {$CF_EXAMPLE}
	}
}

example.com {
	cf_dns_manager {
		host foo.example.com
	}
	respond "ok"
}
`
	adapter := cf_caddyfile.Adapter{ServerType: httpcaddyfile.ServerType{}}
	out, _, err := adapter.Adapt([]byte(input), nil)
	if err != nil {
		t.Fatalf("adapt: %v", err)
	}
	var cfg caddy.Config
	if err := caddy.StrictUnmarshalJSON(out, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := caddy.Validate(&cfg); err != nil {
		t.Fatalf("validate: %v", err)
	}
}
