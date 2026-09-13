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

	return parseIPv4Body(string(body))
}

// parseIPv4Body extracts an IPv4 address from either a plain IP body or a
// key=value body such as Cloudflare's /cdn-cgi/trace (ip=... line).
func parseIPv4Body(body string) (string, error) {
	body = strings.TrimSpace(body)
	if isIPv4(body) {
		return body, nil
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ip=") {
			candidate := strings.TrimSpace(strings.TrimPrefix(line, "ip="))
			if isIPv4(candidate) {
				return candidate, nil
			}
			return "", fmt.Errorf("endpoint returned non-IPv4 address %q", candidate)
		}
	}
	return "", fmt.Errorf("no IPv4 address found in detection response")
}

func isIPv4(s string) bool {
	ip := net.ParseIP(strings.TrimSpace(s))
	return ip != nil && ip.To4() != nil
}
