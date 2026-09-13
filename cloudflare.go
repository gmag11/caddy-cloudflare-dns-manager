package cfdnsmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

const cloudflareAPIBase = "https://api.cloudflare.com/client/v4"

// cloudflareClient is a minimal Cloudflare REST client for DNS records.
type cloudflareClient struct {
	apiBase string
	token   string
	http    *http.Client
}

func newCloudflareClient(token string) *cloudflareClient {
	return newCloudflareClientWithBase(cloudflareAPIBase, token)
}

// newCloudflareClientWithBase is a test seam allowing a custom API base URL.
func newCloudflareClientWithBase(apiBase, token string) *cloudflareClient {
	return &cloudflareClient{
		apiBase: apiBase,
		token:   token,
		http: &http.Client{
			Timeout: httpTimeout,
		},
	}
}

// cfResponse is the standard Cloudflare API envelope.
type cfResponse struct {
	Success  bool              `json:"success"`
	Errors   []cfAPIError      `json:"errors"`
	Messages []json.RawMessage `json:"messages"`
	Result   json.RawMessage   `json:"result"`
}

type cfAPIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// cfZone is the subset of the zone object we need.
type cfZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// cfDNSRecord is the subset of a DNS record we use.
type cfDNSRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
	Comment string `json:"comment,omitempty"`
}

// do performs a request and decodes the standard CF envelope. For writes it
// returns whether the operation succeeded at the API level.
func (c *cloudflareClient) do(ctx context.Context, method, path string, body any, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request body: %v", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.apiBase+path, reqBody)
	if err != nil {
		return fmt.Errorf("building request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare api %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading cloudflare response: %v", err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("cloudflare api %s %s: rate limited (429)", method, path)
	}

	var env cfResponse
	if err := json.Unmarshal(respBody, &env); err != nil {
		return fmt.Errorf("cloudflare api %s %s: decoding response (status %d): %v", method, path, resp.StatusCode, err)
	}

	if !env.Success || resp.StatusCode >= 300 {
		return fmt.Errorf("cloudflare api %s %s: request failed (status %d): %s", method, path, resp.StatusCode, formatCFErrors(env.Errors))
	}

	if out != nil && len(env.Result) > 0 && string(env.Result) != "null" {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("cloudflare api %s %s: decoding result: %v", method, path, err)
		}
	}
	return nil
}

func formatCFErrors(errs []cfAPIError) string {
	if len(errs) == 0 {
		return "no error detail provided"
	}
	var b bytes.Buffer
	for i, e := range errs {
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "code=%d message=%q", e.Code, e.Message)
	}
	return b.String()
}

// zoneIDByName resolves a zone apex to its Cloudflare zone ID.
func (c *cloudflareClient) zoneIDByName(ctx context.Context, zone string) (string, error) {
	u := url.URL{
		Path: "/zones",
	}
	q := u.Query()
	q.Set("name", zone)
	q.Set("per_page", "1")
	var zones []cfZone
	if err := c.do(ctx, http.MethodGet, "/zones?"+q.Encode(), nil, &zones); err != nil {
		return "", err
	}
	for _, z := range zones {
		if z.Name == zone {
			return z.ID, nil
		}
	}
	return "", fmt.Errorf("zone %q not found for this token", zone)
}

// listARecords lists A records in a zone. When name is non-empty, filters to
// that record name (exact match of the relative name Cloudflare uses, where
// apex is "@" or empty).
func (c *cloudflareClient) listRecords(ctx context.Context, zoneID string) ([]cfDNSRecord, error) {
	var records []cfDNSRecord
	// Cloudflare paginates at 100; DNS record counts are small in practice,
	// so a single page with per_page=100 is sufficient. Loop defensively.
	page := 1
	for {
		q := url.Values{}
		q.Set("type", "A")
		q.Set("per_page", "100")
		q.Set("page", fmt.Sprintf("%d", page))
		var pageRecords []cfDNSRecord
		path := "/zones/" + zoneID + "/dns_records?" + q.Encode()
		if err := c.do(ctx, http.MethodGet, path, nil, &pageRecords); err != nil {
			return nil, err
		}
		records = append(records, pageRecords...)
		if len(pageRecords) < 100 {
			break
		}
		page++
	}
	return records, nil
}

// createRecord creates an A record.
func (c *cloudflareClient) createRecord(ctx context.Context, zoneID string, rec cfDNSRecord) error {
	var created cfDNSRecord
	return c.do(ctx, http.MethodPost, "/zones/"+zoneID+"/dns_records", rec, &created)
}

// updateRecord updates an existing A record.
func (c *cloudflareClient) updateRecord(ctx context.Context, zoneID, recordID string, rec cfDNSRecord) error {
	var updated cfDNSRecord
	return c.do(ctx, http.MethodPut, "/zones/"+zoneID+"/dns_records/"+recordID, rec, &updated)
}

// deleteRecord deletes an A record.
func (c *cloudflareClient) deleteRecord(ctx context.Context, zoneID, recordID string) error {
	var out cfResponse
	return c.do(ctx, http.MethodDelete, "/zones/"+zoneID+"/dns_records/"+recordID, nil, &out)
}
