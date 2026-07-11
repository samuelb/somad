package protocol

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
)

// Pre-shared-key authentication for TCP connections: the server issues a
// one-time nonce (authChallenge) and the client proves knowledge of the key
// by returning HMAC-SHA256(psk, nonce) (auth). The key itself never travels
// over the wire, so an eavesdropper on an unencrypted TCP connection cannot
// recover it. Unix-socket connections are exempt; they are already protected
// by the socket directory's file permissions.

// AuthNonceSize is the size of the server's challenge nonce in bytes.
const AuthNonceSize = 32

// AuthChallengeParams asks the server for a challenge nonce.
type AuthChallengeParams struct{}

// AuthChallengeResult carries the server's one-time nonce, base64-encoded
// (standard encoding).
type AuthChallengeResult struct {
	Nonce string `json:"nonce"`
}

// AuthParams answers the challenge with base64(HMAC-SHA256(psk, nonce)).
type AuthParams struct {
	MAC string `json:"mac"`
}

// NewAuthNonce returns a fresh random challenge nonce.
func NewAuthNonce() ([]byte, error) {
	nonce := make([]byte, AuthNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generating auth nonce: %w", err)
	}
	return nonce, nil
}

// ComputeAuthMAC returns HMAC-SHA256 over the nonce, keyed with the
// pre-shared key.
func ComputeAuthMAC(psk string, nonce []byte) []byte {
	mac := hmac.New(sha256.New, []byte(psk))
	mac.Write(nonce)
	return mac.Sum(nil)
}

// VerifyAuthMAC reports whether mac is the correct response to nonce under
// psk, in constant time.
func VerifyAuthMAC(psk string, nonce, mac []byte) bool {
	return hmac.Equal(ComputeAuthMAC(psk, nonce), mac)
}
