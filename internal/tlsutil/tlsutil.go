// Package tlsutil builds the TLS configurations for the optional TCP
// transport: it auto-generates a self-signed server certificate when none is
// configured, and lets clients trust a server either through a CA/cert file
// or by pinning the certificate's SHA-256 fingerprint (the copy-paste-simple
// path for auto-generated certificates).
package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"strings"
	"time"

	"somad/internal/atomicfile"
)

// certValidity is how long an auto-generated certificate lasts. It is a
// pairing credential for a personal music daemon, not a public identity, so
// it is long-lived on purpose: expiry would only break the user's setup.
const certValidity = 10 * 365 * 24 * time.Hour

// EnsureServerCert generates a self-signed ECDSA P-256 certificate at
// certPath/keyPath when either file is missing, and reports whether it did.
// hosts are extra DNS names or IPs to include; the machine's hostname,
// "localhost", and the loopback addresses are always included.
func EnsureServerCert(certPath, keyPath string, hosts []string) (created bool, err error) {
	_, certErr := os.Stat(certPath)
	_, keyErr := os.Stat(keyPath)
	if certErr == nil && keyErr == nil {
		return false, nil
	}
	if !os.IsNotExist(certErr) && certErr != nil {
		return false, fmt.Errorf("checking TLS certificate %s: %w", certPath, certErr)
	}
	if !os.IsNotExist(keyErr) && keyErr != nil {
		return false, fmt.Errorf("checking TLS key %s: %w", keyPath, keyErr)
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return false, fmt.Errorf("generating TLS key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return false, fmt.Errorf("generating certificate serial: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "somad"},
		// Backdated an hour so a client with slight clock skew accepts it
		// immediately after generation.
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(certValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		// IsCA lets the self-signed certificate act as its own trust anchor
		// when a client loads it via tls_ca.
		IsCA: true,
	}
	addHost := func(h string) {
		if h == "" {
			return
		}
		if ip := net.ParseIP(h); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
			return
		}
		template.DNSNames = append(template.DNSNames, h)
	}
	addHost("localhost")
	template.IPAddresses = append(template.IPAddresses, net.IPv4(127, 0, 0, 1), net.IPv6loopback)
	if hostname, err := os.Hostname(); err == nil {
		addHost(hostname)
	}
	for _, h := range hosts {
		addHost(h)
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return false, fmt.Errorf("creating TLS certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return false, fmt.Errorf("encoding TLS key: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	// Key first: if the second write fails, a cert without its key is the
	// harmless leftover (EnsureServerCert regenerates both next time).
	if err := atomicfile.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return false, fmt.Errorf("writing TLS key: %w", err)
	}
	if err := atomicfile.WriteFile(certPath, certPEM, 0o600); err != nil {
		return false, fmt.Errorf("writing TLS certificate: %w", err)
	}
	return true, nil
}

// ServerTLSConfig loads the certificate pair and returns the server-side TLS
// configuration together with the certificate's fingerprint (for pairing
// clients via tls_fingerprint).
func ServerTLSConfig(certPath, keyPath string) (*tls.Config, string, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, "", fmt.Errorf("loading TLS certificate pair: %w", err)
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	return cfg, Fingerprint(cert.Certificate[0]), nil
}

// Fingerprint returns the SHA-256 digest of a certificate's DER encoding in
// the "sha256:<hex>" form used by tls_fingerprint.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// normalizeFingerprint canonicalizes user-supplied fingerprints: lowercase
// hex with the optional "sha256:" prefix and any colon separators removed.
func normalizeFingerprint(fp string) string {
	fp = strings.ToLower(strings.TrimSpace(fp))
	fp = strings.TrimPrefix(fp, "sha256:")
	return strings.ReplaceAll(fp, ":", "")
}

// ClientTLSConfig returns the client-side TLS configuration. Exactly one
// trust source applies: a CA/cert file, a pinned certificate fingerprint, or
// (when both are empty) the system trust store.
func ClientTLSConfig(caPath, fingerprint, serverName string) (*tls.Config, error) {
	if caPath != "" && fingerprint != "" {
		return nil, errors.New("configure either a TLS CA file or a certificate fingerprint, not both")
	}
	cfg := &tls.Config{
		ServerName: serverName,
		MinVersion: tls.VersionTLS12,
	}
	switch {
	case caPath != "":
		pemData, err := os.ReadFile(caPath) // #nosec G304 -- path comes from the user's own config/flags
		if err != nil {
			return nil, fmt.Errorf("reading TLS CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemData) {
			return nil, fmt.Errorf("no certificates found in TLS CA file %s", caPath)
		}
		cfg.RootCAs = pool
	case fingerprint != "":
		want := normalizeFingerprint(fingerprint)
		if len(want) != sha256.Size*2 {
			return nil, fmt.Errorf("invalid TLS fingerprint %q: want 64 hex digits (sha256)", fingerprint)
		}
		// Chain verification is replaced by the pin, not skipped: the
		// connection only proceeds when the server presents exactly the
		// pinned certificate. VerifyConnection (rather than
		// VerifyPeerCertificate) so resumed sessions are checked too.
		cfg.InsecureSkipVerify = true // #nosec G402 -- VerifyConnection below pins the certificate
		cfg.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("server presented no TLS certificate")
			}
			got := normalizeFingerprint(Fingerprint(cs.PeerCertificates[0].Raw))
			if got != want {
				return fmt.Errorf("server TLS certificate fingerprint mismatch: got sha256:%s", got)
			}
			return nil
		}
	}
	return cfg, nil
}
