package cfdnsmanager

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// tunnelClient talks to the Cloudflare Tunnel configuration API. It reuses the
// request transport and error decoding of cloudflareClient via do(); only the
// credential and the path prefix differ. The credential is account-scoped
// (Cloudflare Tunnel Write), unlike the per-zone DNS tokens.
type tunnelClient struct {
	apiBase   string
	accountID string
	token     string
	http      *http.Client
}

// newTunnelClientWithBase is a test seam allowing a custom API base URL. The
// base defaults to the production endpoint when empty, so callers can pass the
// App's apiBase straight through.
func newTunnelClientWithBase(apiBase, accountID, token string) *tunnelClient {
	if apiBase == "" {
		apiBase = cloudflareAPIBase
	}
	return &tunnelClient{
		apiBase:   apiBase,
		accountID: accountID,
		token:     token,
		http: &http.Client{
			Timeout: httpTimeout,
		},
	}
}

// cfTunnelConfig is the tunnel configuration document. It mirrors the shape of
// `GET/PUT /accounts/{id}/cfd_tunnel/{tunnel_id}/configurations`.
//
// originRequest and WarpRouting are kept as raw JSON on purpose: the plugin
// does not model them, so a read-modify-write must round-trip them verbatim.
// Reconstructing the document from a typed struct would silently drop settings
// such as warp-routing, which would break private-network access on the tunnel.
type cfTunnelConfig struct {
	Ingress       []cfIngressRule `json:"ingress"`
	OriginRequest json.RawMessage `json:"originRequest,omitempty"`
	WarpRouting   json.RawMessage `json:"warp-routing,omitempty"`
}

// cfIngressRule is one ingress rule. The plugin manages Hostname, Service and
// the matchSNItoHost setting inside OriginRequest; Path and the rest of
// OriginRequest are carried through untouched so rules and options authored
// outside the plugin survive a write.
//
// Description is deliberately NOT modelled. It is absent from Cloudflare's
// documented ingress model, the dashboard does not render it, and a write that
// omits the key clears any value stored there (verified against the live API).
// Since the plugin must write this resource, it cannot both omit the field and
// preserve it; the choice is to not participate in a half-released feature.
//
// Path and the unknown parts of OriginRequest are raw JSON so their absence
// stays absent rather than becoming zero values, and so fields the plugin does
// not model round-trip byte for byte.
type cfIngressRule struct {
	Hostname      string          `json:"hostname,omitempty"`
	Service       string          `json:"service"`
	Path          json.RawMessage `json:"path,omitempty"`
	OriginRequest json.RawMessage `json:"originRequest,omitempty"`
}

// matchSNIToHostOption is the origin-request key the plugin manages. It makes
// cloudflared send the request's Host as the TLS SNI to the origin, which a
// wildcard-certificate origin (e.g. Caddy serving *.example.com) requires: the
// default SNI is the service URL's hostname, for which such an origin has no
// certificate, turning every request into a 502.
const matchSNIToHostOption = "matchSNItoHost"

// wantsMatchSNIToHost reports whether a service should carry matchSNItoHost.
//
// The test is the service type, not whether TLS is involved. Only cloudflared's
// HTTP service reads this option; its unix service is a separate type with no
// field for it, and every other scheme is a stream service that never performs
// a TLS handshake to the origin at all. A unix+tls service therefore goes
// without the option even though it does speak TLS — writing it there would
// look correct and have no effect, because the API stores unknown option keys
// without validating them.
func wantsMatchSNIToHost(service string) bool {
	return strings.HasPrefix(service, "https://")
}

// ensureMatchSNIToHost returns the origin request for a service, guaranteeing
// matchSNItoHost is true for HTTPS services. Any other option already present
// is preserved: this merges one key, it does not replace the object.
func ensureMatchSNIToHost(existing json.RawMessage, service string) json.RawMessage {
	if !wantsMatchSNIToHost(service) {
		return existing
	}
	opts := map[string]json.RawMessage{}
	if len(existing) > 0 {
		// A malformed value is left untouched rather than replaced, so a
		// hand-edited configuration is never silently discarded.
		if err := json.Unmarshal(existing, &opts); err != nil {
			return existing
		}
	}
	if v, ok := opts[matchSNIToHostOption]; ok && string(v) == "true" {
		return existing
	}
	opts[matchSNIToHostOption] = json.RawMessage("true")
	merged, err := json.Marshal(opts)
	if err != nil {
		return existing
	}
	return merged
}

// hasMatchSNIToHost reports whether a rule's origin request enables the option.
func hasMatchSNIToHost(originRequest json.RawMessage) bool {
	if len(originRequest) == 0 {
		return false
	}
	var opts struct {
		MatchSNIToHost bool `json:"matchSNItoHost"`
	}
	if err := json.Unmarshal(originRequest, &opts); err != nil {
		return false
	}
	return opts.MatchSNIToHost
}

// cfTunnelConfiguration is the API envelope's result object.
type cfTunnelConfiguration struct {
	AccountID string          `json:"account_id,omitempty"`
	Config    *cfTunnelConfig `json:"config,omitempty"`
	Source    string          `json:"source,omitempty"`
	Version   int             `json:"version,omitempty"`
}

// configurationPath is the configurations endpoint for one tunnel.
func (c *tunnelClient) configurationPath(tunnelID string) string {
	return "/accounts/" + c.accountID + "/cfd_tunnel/" + tunnelID + "/configurations"
}

// getConfiguration reads the tunnel's current configuration. A tunnel that has
// never been configured may report an empty or absent config; both are
// normalised to a config with no ingress rules rather than an error, so the
// first reconcile treats it as "nothing to preserve".
func (c *tunnelClient) getConfiguration(ctx context.Context, tunnelID string) (cfTunnelConfiguration, error) {
	var out cfTunnelConfiguration
	// do() is a method on cloudflareClient; share the implementation by
	// building a bare client around the same transport fields.
	cli := &cloudflareClient{apiBase: c.apiBase, token: c.token, http: c.http}
	if err := cli.do(ctx, http.MethodGet, c.configurationPath(tunnelID), nil, &out); err != nil {
		return cfTunnelConfiguration{}, err
	}
	if out.Config == nil {
		out.Config = &cfTunnelConfig{}
	}
	if out.Config.Ingress == nil {
		out.Config.Ingress = []cfIngressRule{}
	}
	return out, nil
}

// putConfiguration replaces the tunnel's configuration. The whole config is
// sent, including fields the plugin does not model, because PUT replaces the
// document rather than merging into it.
func (c *tunnelClient) putConfiguration(ctx context.Context, tunnelID string, cfg cfTunnelConfig) error {
	cli := &cloudflareClient{apiBase: c.apiBase, token: c.token, http: c.http}
	body := struct {
		Config cfTunnelConfig `json:"config"`
	}{Config: cfg}
	return cli.do(ctx, http.MethodPut, c.configurationPath(tunnelID), body, nil)
}
