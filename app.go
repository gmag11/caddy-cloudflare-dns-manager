package cfdnsmanager

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"go.uber.org/zap"
)

func init() {
	caddy.RegisterModule(new(App))
	caddy.RegisterModule(hostHandler{})
	httpcaddyfile.RegisterGlobalOption("cf_dns_manager", parseGlobalOption)
	httpcaddyfile.RegisterDirective("cf_dns_manager", parseDirective)
	httpcaddyfile.RegisterDirectiveOrder("cf_dns_manager", httpcaddyfile.Before, "invoke")
}

const (
	// AppID is the Caddy module ID of this plugin's app.
	AppID = "cf_dns_manager"
	// appName is the config key used for the app.
	appName = "cf_dns_manager"

	// DefaultIPURL is the default endpoint used to detect the public IPv4.
	DefaultIPURL = "https://cloudflare.com/cdn-cgi/trace"

	// defaultTagPrefix is used for the ownership comment when no prefix is set.
	defaultTagPrefix = "caddy-cf-dns"
	// httpTimeout bounds every outbound HTTP call (Cloudflare API + IP detection).
	httpTimeout = 15 * time.Second
)

// App is the Caddy app that reconciles Cloudflare DNS records.
// It is configured via the `cf_dns_manager` global options block, and
// collects host declarations from per-site `cf_dns_manager` directives.
type App struct {
	// Zones is the list of managed Cloudflare zones and their tokens.
	Zones []ZoneConfig `json:"zones,omitempty"`

	// IPURL overrides the public-IP detection endpoint.
	IPURL string `json:"ip_url,omitempty"`

	// TagPrefix is the ownership-comment prefix.
	TagPrefix string `json:"tag_prefix,omitempty"`

	// Instance identifies this Caddy server for ownership tagging.
	Instance string `json:"instance,omitempty"`

	// logger is the app logger.
	logger *zap.Logger `json:"-"`

	// hostsMu guards hosts.
	hostsMu sync.Mutex `json:"-"`
	// hosts accumulates per-site host declarations during adapt.
	hosts []HostConfig `json:"-"`

	// apiBase overrides the Cloudflare API base URL (test seam).
	apiBase string `json:"-"`
}

// HostConfig is a single per-site host declaration.
type HostConfig struct {
	// Host is the fully-qualified hostname to reconcile.
	Host string `json:"host"`
	// IP is an explicit IPv4 override; empty means "detected public IP".
	IP string `json:"ip,omitempty"`
	// Proxied requests Cloudflare proxied mode. Defaults to true.
	Proxied *bool `json:"proxied,omitempty"`
	// ForceAdopt allows adopting/updating an untagged existing record.
	ForceAdopt bool `json:"force_adopt,omitempty"`
	// ZoneConfig is the resolved managed zone (filled at adapt time).
	ZoneConfig ZoneConfig `json:"-"`
}

// ZoneConfig declares a managed Cloudflare zone.
type ZoneConfig struct {
	// Zone is the zone apex (e.g. example.com).
	Zone string `json:"zone"`
	// APIToken is the Cloudflare API token.
	APIToken string `json:"api_token"`
	// Prune enables deleting this instance's orphaned A records in this zone.
	Prune bool `json:"prune,omitempty"`
}

// CaddyModule returns the Caddy module information.
func (*App) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  AppID,
		New: func() caddy.Module { return new(App) },
	}
}

// lastProvisionedApp is a test hook capturing the most recently provisioned
// App so in-package integration tests can inspect registered hosts and run
// Start against a mock Cloudflare. It is never used in production paths.
var lastProvisionedApp *App

// Provision initializes the app.
func (app *App) Provision(ctx caddy.Context) error {
	app.logger = ctx.Logger()
	lastProvisionedApp = app
	if app.TagPrefix == "" {
		app.TagPrefix = defaultTagPrefix
	}
	if app.Instance == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return fmt.Errorf("getting hostname for instance id: %v", err)
		}
		app.Instance = hostname
	}
	return nil
}

// Validate checks the configuration.
func (app *App) Validate() error {
	for i, z := range app.Zones {
		if z.Zone == "" {
			return fmt.Errorf("zone %d: zone name is required", i)
		}
		if z.APIToken == "" {
			return fmt.Errorf("zone %q: api_token is required", z.Zone)
		}
	}
	if app.TagPrefix == "" {
		return fmt.Errorf("tag_prefix cannot be empty")
	}
	if app.Instance == "" {
		return fmt.Errorf("instance cannot be empty")
	}
	return nil
}

// Start runs reconciliation for every declared host.
func (app *App) Start() error {
	hosts := app.hostsSnapshot()
	if len(hosts) == 0 {
		app.logger.Debug("no hosts declared for cf_dns_manager; skipping reconcile")
		return nil
	}
	if err := app.Reconcile(hosts); err != nil {
		return fmt.Errorf("cf_dns_manager reconcile: %v", err)
	}
	return nil
}

// Stop is part of the caddy.App interface.
func (app *App) Stop() error {
	return nil
}

// Cleanup is part of caddy.CleanerUpper.
func (app *App) Cleanup() error {
	return nil
}

var (
	_ caddy.App          = (*App)(nil)
	_ caddy.Provisioner  = (*App)(nil)
	_ caddy.Validator    = (*App)(nil)
	_ caddy.CleanerUpper = (*App)(nil)
	_ caddy.Module       = (*App)(nil)
)
