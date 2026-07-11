package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"somad/internal/client"
	"somad/internal/config"
	"somad/internal/protocol"
	"somad/internal/tlsutil"
)

// endpoint is where the TUI and CLI commands connect: the local Unix socket
// by default, or a remote TCP server. Resolved once in main from the global
// connection flags, $SOMAD_SERVER, and the config file — in that order.
var endpoint client.Endpoint

// connFlags are the global client connection flags, given before the
// command (e.g. `soma --server myserver:5454 play groovesalad`). main
// registers them on its FlagSet.
type connFlags struct {
	server         string
	tls            bool
	tlsCA          string
	tlsFingerprint string
	pskFile        string
}

// resolveEndpoint turns the connection flags and config into the endpoint to
// use: the Unix socket unless a remote server address is configured.
func resolveEndpoint(f connFlags, cfg *config.Config) (client.Endpoint, error) {
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}

	addr := f.server
	if addr == "" {
		addr = os.Getenv("SOMAD_SERVER")
	}
	if addr == "" {
		addr = str(cfg.Client.Server)
	}
	if addr == "" {
		// The remaining connection flags only mean something for a TCP
		// endpoint; ignoring them silently would mask a typo'd setup.
		if f.tls || f.tlsCA != "" || f.tlsFingerprint != "" || f.pskFile != "" {
			return client.Endpoint{}, errors.New(
				"--tls, --tls-ca, --tls-fingerprint, and --psk-file require --server (or $SOMAD_SERVER, or client.server in the config)")
		}
		return client.UnixEndpoint(protocol.SocketPath()), nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return client.Endpoint{}, fmt.Errorf("invalid server address %q: want host:port", addr)
	}

	caPath, fingerprint := str(cfg.Client.TLSCA), str(cfg.Client.TLSFingerprint)
	// A trust flag replaces both configured trust sources, so a one-off
	// --tls-fingerprint works even when the config file names a tls_ca.
	if f.tlsCA != "" || f.tlsFingerprint != "" {
		caPath, fingerprint = f.tlsCA, f.tlsFingerprint
	}
	useTLS := f.tls || caPath != "" || fingerprint != "" ||
		(cfg.Client.TLS != nil && *cfg.Client.TLS)

	psk := str(cfg.Client.PSK)
	if pskFile := firstNonEmpty(f.pskFile, str(cfg.Client.PSKFile)); pskFile != "" {
		if psk, err = readPSKFile(pskFile); err != nil {
			return client.Endpoint{}, err
		}
	}

	ep := client.Endpoint{Network: "tcp", Address: addr, PSK: psk}
	if useTLS {
		if ep.TLS, err = tlsutil.ClientTLSConfig(caPath, fingerprint, host); err != nil {
			return client.Endpoint{}, err
		}
	}
	return ep, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// readPSKFile reads a pre-shared key from a file, trimming surrounding
// whitespace (hand-written key files inevitably end in a newline).
func readPSKFile(path string) (string, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path comes from the user's own config/flags
	if err != nil {
		return "", fmt.Errorf("reading PSK file: %w", err)
	}
	psk := strings.TrimSpace(string(data))
	if psk == "" {
		return "", fmt.Errorf("PSK file %s is empty", path)
	}
	return psk, nil
}
