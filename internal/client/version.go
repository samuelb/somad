package client

// VersionSkewed reports whether serverVersion differs from clientVersion in a
// way that should trigger a restart of a local daemon onto the client's
// binary (see spawn.go and ADR-0006). It is plain string inequality, with one
// exemption: "dev" on either side never counts as skew, so a `go build` dev
// binary and an installed release running on the same machine do not restart
// each other's daemon on every channel change. Full semver ordering — restart
// only onto a newer client — is deliberately not implemented; ADR-0006
// records why.
func VersionSkewed(clientVersion, serverVersion string) bool {
	if clientVersion == "dev" || serverVersion == "dev" {
		return false
	}
	return clientVersion != serverVersion
}
