package cfdnsmanager

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

// detectPublicIPv4 queries url for the caller's public IPv4 address. The
// default endpoint returns a `key=value` trace body; a configurable endpoint
// may return either a plain-text IP or a key=value body. It returns an error
// when no IPv4 can be determined (network failure, non-2xx, malformed body,
// or only IPv6 present).
func detectPublicIPv4(ctx context.Context, client *http.Client, url string) (string, error) {
	return detectPublicIP(ctx, client, url, false)
}

// detectPublicIPv6 queries url for the caller's public IPv6 address, applying
// the same body shapes as IPv4 detection. It returns an error when no IPv6 can
// be determined (network failure, non-2xx, malformed body, or a non-IPv6
// result). A returned IPv4 is always an error: the default endpoint is
// IPv6-only, so a v4 body means the endpoint or network is misconfigured.
func detectPublicIPv6(ctx context.Context, client *http.Client, url string) (string, error) {
	return detectPublicIP(ctx, client, url, true)
}

// detectPublicIP performs the shared request and parses the body for the
// requested family. want6 selects IPv6; otherwise IPv4 is required.
func detectPublicIP(ctx context.Context, client *http.Client, url string, want6 bool) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("building ip detection request: %v", err)
	}
	req.Header.Set("User-Agent", "caddy-cloudflare-dns-manager")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("querying %s: %v", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", fmt.Errorf("querying %s: status %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return "", fmt.Errorf("reading ip detection response: %v", err)
	}

	if want6 {
		return parseIPBody(string(body), true)
	}
	return parseIPBody(string(body), false)
}

// parseIPv4Body extracts an IPv4 address from either a plain IP body or a
// key=value body such as Cloudflare's /cdn-cgi/trace (ip=... line).
func parseIPv4Body(body string) (string, error) {
	return parseIPBody(body, false)
}

// parseIPv6Body extracts an IPv6 address from either a plain IP body or a
// key=value body. A v4 or malformed body is an error.
func parseIPv6Body(body string) (string, error) {
	return parseIPBody(body, true)
}

// parseIPBody extracts an address of the requested family from either a plain
// IP body or a key=value body such as Cloudflare's /cdn-cgi/trace (ip=...
// line). A candidate of the wrong family is an error, not a silent skip.
func parseIPBody(body string, want6 bool) (string, error) {
	family := "IPv4"
	if want6 {
		family = "IPv6"
	}
	match := isIPv4
	if want6 {
		match = isIPv6
	}

	body = strings.TrimSpace(body)
	if match(body) {
		return body, nil
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ip=") {
			candidate := strings.TrimSpace(strings.TrimPrefix(line, "ip="))
			if match(candidate) {
				return candidate, nil
			}
			return "", fmt.Errorf("endpoint returned non-%s address %q", family, candidate)
		}
	}
	return "", fmt.Errorf("no %s address found in detection response", family)
}

func isIPv4(s string) bool {
	ip := net.ParseIP(strings.TrimSpace(s))
	return ip != nil && ip.To4() != nil
}

// isIPv6 reports whether s is an IPv6 address. IPv4 and IPv4-mapped addresses
// (e.g. ::ffff:1.2.3.4) are rejected: they are not usable AAAA content.
func isIPv6(s string) bool {
	ip := net.ParseIP(strings.TrimSpace(s))
	return ip != nil && ip.To4() == nil && ip.To16() != nil
}
