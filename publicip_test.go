package cfdnsmanager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseIPv4Body(t *testing.T) {
	cases := []struct {
		body string
		want string
		err  bool
	}{
		{"8.8.8.8", "8.8.8.8", false},
		{"1.1.1.1\n", "1.1.1.1", false},
		{"ip=203.0.113.9\nfl=42\n", "203.0.113.9", false},
		{"ip=2001:db8::1\n", "", true},
		{"hello world", "", true},
		{"", "", true},
		{"not.an.ip", "", true},
	}
	for _, c := range cases {
		got, err := parseIPv4Body(c.body)
		if c.err {
			if err == nil {
				t.Errorf("parseIPv4Body(%q) expected error, got %q", c.body, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseIPv4Body(%q) unexpected error: %v", c.body, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseIPv4Body(%q) = %q, want %q", c.body, got, c.want)
		}
	}
}

func TestDetectPublicIPv4UsesConfiguredEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ip=203.0.113.42\n"))
	}))
	defer srv.Close()

	ip, err := detectPublicIPv4(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if ip != "203.0.113.42" {
		t.Errorf("got %q, want 203.0.113.42", ip)
	}
}

func TestDetectPublicIPv4Errors(t *testing.T) {
	t.Run("network error", func(t *testing.T) {
		// unroutable/closed port
		_, err := detectPublicIPv4(context.Background(), &http.Client{}, "http://127.0.0.1:1/")
		if err == nil {
			t.Fatal("expected error for connection refused")
		}
	})

	t.Run("non-2xx", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer srv.Close()
		if _, err := detectPublicIPv4(context.Background(), srv.Client(), srv.URL); err == nil {
			t.Fatal("expected error for 500")
		}
	})

	t.Run("ipv6", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("ip=2001:db8::1\n"))
		}))
		defer srv.Close()
		if _, err := detectPublicIPv4(context.Background(), srv.Client(), srv.URL); err == nil || !strings.Contains(err.Error(), "non-IPv4") {
			t.Fatalf("expected non-IPv4 error, got %v", err)
		}
	})
}
