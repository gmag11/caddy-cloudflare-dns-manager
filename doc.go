// Package cf_dns_manager implements a Caddy app that reconciles Cloudflare
// DNS A records for hosts declared in the Caddyfile.
//
// The Caddyfile surface consists of a global options block `cf_dns_manager`
// which registers managed Cloudflare zones (with their API tokens) and global
// settings, and a per-site directive `cf_dns_manager` which opts a single
// host in to DNS management.
package cfdnsmanager
