package cfdnsmanager

import (
	"context"
	"encoding/json"
	"net/http"
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

// cfIngressRule is one ingress rule. Only Hostname and Service are managed by
// the plugin; Path and OriginRequest are carried through untouched so rules
// authored outside the plugin survive a write. Both are raw JSON for the same
// round-trip reason as above, and Path is raw so its absence stays absent
// rather than becoming an empty string.
type cfIngressRule struct {
	Hostname      string          `json:"hostname,omitempty"`
	Service       string          `json:"service"`
	Path          json.RawMessage `json:"path,omitempty"`
	OriginRequest json.RawMessage `json:"originRequest,omitempty"`
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
