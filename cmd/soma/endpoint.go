package main

import (
	"cmp"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"somad/internal/client"
	"somad/internal/config"
	"somad/internal/protocol"
	"somad/internal/security"
	"somad/internal/tlsutil"
)

// endpoint is where the TUI and CLI commands connect: the local Unix socket
// by default, or a remote TCP server. Resolved once in main from the global
// connection flags, $SOMAD_SERVER, and the config file — in that order.
var endpoint client.Endpoint

// connFlags are the global client connection flags, given before the
// command (e.g. `soma --server myserver:5454 play groovesalad`).
type connFlags struct {
	server         string
	tls            bool
	tlsCA          string
	tlsFingerprint string
	pskFile        string
}

// register binds the connection flags on fs.
func (f *connFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.server, "server", "", "connect to the soma daemon at this host:port instead of the local one")
	fs.BoolVar(&f.tls, "tls", false, "use TLS for the --server connection")
	fs.StringVar(&f.tlsCA, "tls-ca", "", "PEM certificate/CA file to trust (implies --tls)")
	fs.StringVar(&f.tlsFingerprint, "tls-fingerprint", "", "pin the server certificate by SHA-256 fingerprint (implies --tls)")
	fs.StringVar(&f.pskFile, "psk-file", "", "file holding the server's pre-shared key")
}

// resolveEndpoint turns the connection flags and config into the endpoint to
// use: the Unix socket unless a remote server address is configured.
func resolveEndpoint(f connFlags, cfg *config.Config) (client.Endpoint, error) {
	addr := cmp.Or(f.server, os.Getenv("SOMAD_SERVER"), str(cfg.Client.Server))
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
		// config.Load already expanded a configured tls_ca; a --tls-ca flag
		// bypasses that, so it needs the same "~/" handling here (a shell
		// normally expands it, but a quoted flag value should still work).
		flagCA, err := config.ExpandHome(f.tlsCA)
		if err != nil {
			return client.Endpoint{}, err
		}
		caPath, fingerprint = flagCA, f.tlsFingerprint
	}
	useTLS := f.tls || caPath != "" || fingerprint != "" || boolVal(cfg.Client.TLS)

	psk := str(cfg.Client.PSK)
	// Likewise for --psk-file: cfg.Client.PSKFile is already expanded, but a
	// flag value is not.
	flagPSKFile, err := config.ExpandHome(f.pskFile)
	if err != nil {
		return client.Endpoint{}, err
	}
	if pskFile := cmp.Or(flagPSKFile, str(cfg.Client.PSKFile)); pskFile != "" {
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

// str returns the value of a possibly-nil config string field (config
// fields are pointers so an explicit "" is distinguishable from an absent
// key), or "" when unset. Used to seed flag defaults from the config file.
func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// boolVal is str for bool-valued config fields: unset reads as false.
func boolVal(p *bool) bool {
	return p != nil && *p
}

// readPSKFile reads a pre-shared key from a file, trimming surrounding
// whitespace (hand-written key files inevitably end in a newline). The file
// must pass the same SSH-style permission check (security.CheckOwnerOnly)
// the daemon applies to the socket directory: anyone who can read the PSK controls
// playback (and can shut the daemon down), so it must not be group- or
// world-readable, nor owned by another user.
//
// The file is opened once and the permission check runs against that same
// descriptor's Stat, so a swap of the path between a separate stat and the
// read (TOCTOU: e.g. a symlink dropped in after the check passes) cannot
// slip an unchecked file through.
func readPSKFile(path string) (string, error) {
	f, err := os.Open(path) // #nosec G304 -- path comes from the user's own config/flags
	if err != nil {
		return "", fmt.Errorf("opening PSK file: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := checkPSKFilePermissions(f); err != nil {
		return "", err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return "", fmt.Errorf("reading PSK file: %w", err)
	}
	psk := strings.TrimSpace(string(data))
	if psk == "" {
		return "", fmt.Errorf("PSK file %s is empty", path)
	}
	return psk, nil
}

// checkPSKFilePermissions rejects a PSK file that is not a regular file, is
// readable by group or others, or is owned by a different user than the one
// running soma. It stats the already-open file, not the path, so the check
// applies to exactly the bytes readPSKFile goes on to read.
func checkPSKFilePermissions(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat PSK file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("PSK file %s is not a regular file", f.Name())
	}
	if err := security.CheckOwnerOnly(info, "PSK file "+f.Name()); err != nil {
		return fmt.Errorf("%w (chmod 600 it, or regenerate it with soma daemon --gen-psk)", err)
	}
	return nil
}
