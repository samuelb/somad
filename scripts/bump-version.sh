#!/usr/bin/env sh
# Bump the project to a release version in every file that carries one.
# For somad that is only flake.nix (the Go binary itself is versioned via
# ldflags at build time; the PKGBUILD and Homebrew templates are stamped by
# their stage scripts at release time). Mirrors whirr's scripts/bump-version.sh.
# Usage: bump-version.sh <version>
#   version   release version without a leading "v" (e.g. 1.2.3)
set -eu

version="${1:?version required (X.Y.Z, no leading v)}"
tmp="$(mktemp)"

# --- flake.nix: bump the package `version` attribute and the ldflags -X main.version ---
awk -v ver="$version" '
  !done_attr && /^[[:space:]]*version = "[^"]*";[[:space:]]*$/ {
    sub(/"[^"]*"/, "\"" ver "\""); done_attr = 1
  }
  /-X main\.version=v/ {
    sub(/-X main\.version=v[^"]*/, "-X main.version=v" ver)
  }
  { print }
' flake.nix > "$tmp" && mv "$tmp" flake.nix

echo "bumped to $version"
