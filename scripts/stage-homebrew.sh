#!/usr/bin/env sh
# Render the Homebrew tap files from the templates in packaging/homebrew/
# (a tree mirroring the samuelb/homebrew-tap layout: Formula/, Casks/) by
# stamping in the release version and the published binaries' checksums.
# The rendered tree is what the release workflow hands to the tap's
# reusable publish workflow. Mirrors whirr's scripts/stage-homebrew.sh.
# Usage: stage-homebrew.sh <version> <checksums.txt> [out_dir]
#   version   release version without a leading "v" (e.g. 1.2.3)
set -eu

version="${1:?version required (X.Y.Z, no leading v)}"
checksums="${2:?path to checksums.txt required}"
out_dir="${3:-tap}"

sha() {
    grep " $1\$" "$checksums" | awk '{ print $1 }'
}

darwin_universal="$(sha soma_darwin_universal)"
linux_arm64="$(sha soma_linux_arm64)"
linux_amd64="$(sha soma_linux_amd64)"

for name_value in "soma_darwin_universal:$darwin_universal" "soma_linux_arm64:$linux_arm64" "soma_linux_amd64:$linux_amd64"; do
    if [ -z "${name_value#*:}" ]; then
        echo "missing checksum for ${name_value%%:*} in $checksums" >&2
        exit 1
    fi
done

mkdir -p "$out_dir"
cp -R packaging/homebrew/. "$out_dir/"

tmp="$(mktemp)"
sed \
    -e "s|^  version \".*\"|  version \"$version\"|" \
    -e "s|REPLACE_WITH_DARWIN_UNIVERSAL_SHA256|$darwin_universal|" \
    -e "s|REPLACE_WITH_LINUX_ARM64_SHA256|$linux_arm64|" \
    -e "s|REPLACE_WITH_LINUX_AMD64_SHA256|$linux_amd64|" \
    "$out_dir/Formula/somad.rb" > "$tmp" && mv "$tmp" "$out_dir/Formula/somad.rb"

if grep -R "REPLACE_WITH" "$out_dir" >/dev/null 2>&1; then
    echo "unstamped placeholders remain in $out_dir:" >&2
    grep -Rn "REPLACE_WITH" "$out_dir" >&2
    exit 1
fi

echo "staged Homebrew tap files in $out_dir (version $version)"
