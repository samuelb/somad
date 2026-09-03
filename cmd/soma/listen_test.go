package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckTCPSecurity(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		useTLS   bool
		psk      string
		insecure bool
		wantErr  string // substring the error must contain; empty means allowed
	}{
		{name: "loopback needs nothing", addr: "127.0.0.1:5454"},
		{name: "localhost needs nothing", addr: "localhost:5454"},
		{name: "ipv6 loopback needs nothing", addr: "[::1]:5454"},
		{name: "all interfaces without psk refused", addr: ":5454", wantErr: "without authentication"},
		{name: "public without psk refused", addr: "0.0.0.0:5454", wantErr: "without authentication"},
		{name: "public psk without tls refused", addr: "0.0.0.0:5454", psk: "s", wantErr: "no TLS"},
		{name: "public with tls and psk allowed", addr: "0.0.0.0:5454", useTLS: true, psk: "s"},
		{name: "insecure overrides", addr: "0.0.0.0:5454", insecure: true},
		{name: "unparseable addr treated as non-loopback", addr: "garbage", wantErr: "without authentication"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkTCPSecurity(daemonOptions{listen: tt.addr, tlsEnabled: tt.useTLS, psk: tt.psk, insecure: tt.insecure})
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
