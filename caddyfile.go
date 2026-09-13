package cfdnsmanager

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

// parseGlobalOption parses the global options block:
//
//	cf_dns_manager {
//	    zone <zone> api_token <token> [prune]
//	    ip_url <url>
//	    tag_prefix <prefix>
//	    instance <id>
//	}
//
// The App config is returned as an httpcaddyfile.App so the adapter injects it
// into the final config's apps map. Host declarations live in per-site blocks
// and are attached to the app at runtime (not through this frozen JSON).
func parseGlobalOption(d *caddyfile.Dispenser, _ any) (any, error) {
	d.Next() // consume option name

	app := new(App)
	for d.NextBlock(0) {
		switch d.Val() {
		case "zone":
			zc, err := parseZone(d)
			if err != nil {
				return nil, err
			}
			app.Zones = append(app.Zones, zc)
		case "ip_url":
			if !d.NextArg() {
				return nil, d.ArgErr()
			}
			app.IPURL = d.Val()
			if d.NextArg() {
				return nil, d.ArgErr()
			}
		case "tag_prefix":
			if !d.NextArg() {
				return nil, d.ArgErr()
			}
			app.TagPrefix = d.Val()
			if d.NextArg() {
				return nil, d.ArgErr()
			}
		case "instance":
			if !d.NextArg() {
				return nil, d.ArgErr()
			}
			app.Instance = d.Val()
			if d.NextArg() {
				return nil, d.ArgErr()
			}
		default:
			return nil, d.Errf("unrecognized subdirective: %s", d.Val())
		}
	}

	return httpcaddyfile.App{
		Name:  appName,
		Value: caddyconfig.JSON(app, nil),
	}, nil
}

// parseZone parses: zone <zone> api_token <token> [prune]
func parseZone(d *caddyfile.Dispenser) (ZoneConfig, error) {
	var zc ZoneConfig
	if !d.NextArg() {
		return zc, d.ArgErr()
	}
	zc.Zone = strings.ToLower(strings.TrimSuffix(d.Val(), "."))
	if !d.NextArg() || d.Val() != "api_token" {
		return zc, d.Errf("zone %q: expected 'api_token <token>'", zc.Zone)
	}
	if !d.NextArg() {
		return zc, d.Errf("zone %q: missing api_token value", zc.Zone)
	}
	zc.APIToken = d.Val()
	if d.NextArg() {
		if d.Val() != "prune" {
			return zc, d.Errf("zone %q: unexpected argument %q (expected 'prune')", zc.Zone, d.Val())
		}
		zc.Prune = true
		if d.NextArg() {
			return zc, d.ArgErr()
		}
	}
	if zc.Zone == "" || zc.APIToken == "" {
		return zc, d.Errf("zone requires a name and api_token")
	}
	return zc, nil
}

// parseDirective parses the per-site cf_dns_manager directive.
//
//	cf_dns_manager {
//	    host @ref|<fqdn>   # required
//	    ip <ipv4>          # optional
//	    proxied yes|no     # optional
//	    force_adopt        # optional
//	}
//
// Each directive manages exactly one host. It validates the declaration at
// adapt time, then emits a single no-op middleware handler carrying the host
// config. When Caddy provisions that handler, it registers the host with the
// cf_dns_manager app instance, which reconciles everything in its Start().
func parseDirective(h httpcaddyfile.Helper) ([]httpcaddyfile.ConfigValue, error) {
	h.Next() // consume directive name

	// The declared zones come from the global options block (parsed before
	// per-site directives run).
	zones, err := zonesFromOptions(h)
	if err != nil {
		return nil, err
	}

	hc, err := parseHostBlock(h)
	if err != nil {
		return nil, err
	}

	// Resolve the host to a zone now so undeclared zones fail at adapt time.
	if err := assignZone(zones, &hc); err != nil {
		return nil, err
	}

	handler := hostHandler{Host: hc}
	return h.NewRoute(nil, handler), nil
}

// hostHandler is a no-op middleware handler. It exists so the per-site
// directive is a valid route inside site and handle blocks; its real job is
// registering its host config with the app during Provision.
type hostHandler struct {
	// Host is the parsed host declaration carried through config JSON.
	Host HostConfig `json:"host,omitempty"`
}

// ServeHTTP passes the request through unchanged.
func (hostHandler) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	return next.ServeHTTP(w, r)
}

func (hostHandler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.cf_dns_manager_host",
		New: func() caddy.Module { return new(hostHandler) },
	}
}

// Provision registers this handler's host with the app so the app can
// reconcile it at Start time.
func (hh *hostHandler) Provision(ctx caddy.Context) error {
	appIface, err := ctx.App(appName)
	if err != nil {
		return fmt.Errorf("getting %s app for host registration: %v", appName, err)
	}
	app := appIface.(*App)
	app.addHost(hh.Host)
	return nil
}

var _ caddyhttp.MiddlewareHandler = (*hostHandler)(nil)

// parseHostBlock parses the directive body.
func parseHostBlock(h httpcaddyfile.Helper) (HostConfig, error) {
	var hc HostConfig
	hc.Proxied = boolPtr(true) // default proxied

	for h.NextBlock(0) {
		switch d := h.Val(); d {
		case "host":
			if hc.Host != "" {
				return hc, h.Errf("host specified more than once")
			}
			if !h.NextArg() {
				return hc, h.Errf("host requires one argument (@matcher or FQDN)")
			}
			hostVal := h.Val()
			host, err := resolveHostToken(h, hostVal)
			if err != nil {
				return hc, err
			}
			hc.Host = host
			if h.NextArg() {
				return hc, h.ArgErr()
			}
		case "ip":
			if !h.NextArg() {
				return hc, h.ArgErr()
			}
			ip := h.Val()
			if net.ParseIP(ip) == nil || !strings.Contains(ip, ".") {
				return hc, h.Errf("ip must be an IPv4 address, got %q", ip)
			}
			hc.IP = ip
			if h.NextArg() {
				return hc, h.ArgErr()
			}
		case "proxied":
			if !h.NextArg() {
				return hc, h.ArgErr()
			}
			switch h.Val() {
			case "yes":
				hc.Proxied = boolPtr(true)
			case "no":
				hc.Proxied = boolPtr(false)
			default:
				return hc, h.Errf("proxied must be yes or no, got %q", h.Val())
			}
			if h.NextArg() {
				return hc, h.ArgErr()
			}
		case "force_adopt":
			if h.NextArg() {
				return hc, h.ArgErr()
			}
			hc.ForceAdopt = true
		default:
			return hc, h.Errf("unrecognized subdirective: %s", d)
		}
	}

	if hc.Host == "" {
		return hc, h.Errf("cf_dns_manager requires a host subdirective")
	}

	return hc, nil
}

// resolveHostToken resolves a host argument that is either a literal FQDN or
// a named matcher reference (@name). Matcher references are resolved against
// the matchers visible in the current scope (including those inherited from
// the enclosing site block into nested handle/route blocks).
func resolveHostToken(h httpcaddyfile.Helper, val string) (string, error) {
	if !strings.HasPrefix(val, "@") {
		if val == "" {
			return "", fmt.Errorf("host cannot be empty")
		}
		return strings.ToLower(strings.TrimSuffix(val, ".")), nil
	}

	// Build a synthetic dispenser whose only token is the matcher reference,
	// then use the Helper's matcher resolution to obtain the module map.
	toks, err := caddyfile.Tokenize([]byte(val), h.File())
	if err != nil {
		return "", fmt.Errorf("resolving host matcher %q: %v", val, err)
	}
	sub := h.WithDispenser(caddyfile.NewDispenser(toks))
	matcherSet, ok, err := sub.MatcherToken()
	if err != nil {
		return "", fmt.Errorf("resolving host matcher %q: %v", val, err)
	}
	if !ok {
		return "", fmt.Errorf("host matcher %q not found in this scope", val)
	}

	// matcherSet is a caddy.ModuleMap: matcher module -> raw JSON. The host
	// matcher module is registered under the key "host".
	raw, hasHost := matcherSet["host"]
	if !hasHost {
		return "", fmt.Errorf("matcher %q does not contain a host matcher; cf_dns_manager host must reference a matcher with a single host, or use a literal FQDN", val)
	}
	var hosts []string
	if err := json.Unmarshal(raw, &hosts); err != nil {
		return "", fmt.Errorf("matcher %q host list is not decodable: %v", val, err)
	}
	if len(hosts) != 1 {
		return "", fmt.Errorf("matcher %q must map to exactly one host, got %d; use separate cf_dns_manager directives or a literal FQDN", val, len(hosts))
	}
	return strings.ToLower(strings.TrimSuffix(hosts[0], ".")), nil
}

// zonesFromOptions reads the declared zones out of the global options value.
// The global option returns an httpcaddyfile.App whose Value is the JSON
// config; we unmarshal it back to obtain the zone list for validation.
func zonesFromOptions(h httpcaddyfile.Helper) ([]ZoneConfig, error) {
	opt := h.Option("cf_dns_manager")
	if opt == nil {
		return nil, h.Errf("the cf_dns_manager global options block is required before using per-site cf_dns_manager directives")
	}
	appCfg, ok := opt.(httpcaddyfile.App)
	if !ok {
		return nil, h.Errf("internal error: cf_dns_manager global option has unexpected type %T", opt)
	}
	var app App
	if err := json.Unmarshal(appCfg.Value, &app); err != nil {
		return nil, h.Errf("internal error decoding cf_dns_manager global option: %v", err)
	}
	return app.Zones, nil
}

func boolPtr(b bool) *bool { return &b }
