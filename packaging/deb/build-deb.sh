#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -lt 2 ] || [ "$#" -gt 4 ]; then
  echo "usage: $0 <version> <binary> [architecture] [output-dir]" >&2
  exit 2
fi

version="${1#v}"
binary="$2"
arch="${3:-amd64}"
outdir="${4:-dist}"
package="somatui"

if [ ! -f "$binary" ]; then
  echo "error: binary is missing: $binary" >&2
  exit 1
fi

case "$arch" in
  amd64|arm64) ;;
  *)
    echo "error: unsupported Debian architecture: $arch" >&2
    exit 1
    ;;
esac

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

root="$tmpdir/${package}_${version}_${arch}"
install -d \
  "$root/DEBIAN" \
  "$root/usr/bin" \
  "$root/usr/share/doc/$package"

install -m 0755 "$binary" "$root/usr/bin/$package"
install -m 0644 LICENSE "$root/usr/share/doc/$package/copyright"
install -m 0644 README.md "$root/usr/share/doc/$package/README.md"

installed_size="$(du -sk "$root/usr" | awk '{print $1}')"
cat > "$root/DEBIAN/control" <<EOF
Package: $package
Version: $version
Section: sound
Priority: optional
Architecture: $arch
Maintainer: Samuel B <samuelb@users.noreply.github.com>
Homepage: https://github.com/samuelb/somatui
Depends: libc6 (>= 2.34), libasound2 | libasound2t64, ca-certificates
Installed-Size: $installed_size
Description: Terminal UI client for SomaFM radio
 SomaTUI is a keyboard-driven terminal client for browsing and streaming
 SomaFM radio channels, with background playback and CLI controls.
EOF

install -d "$outdir"
dpkg-deb --build --root-owner-group "$root" "$outdir/${package}_${version}_linux_${arch}.deb"
