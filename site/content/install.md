---
title: Install
description: Install Somad on macOS or Linux with Homebrew, a distribution package, Nix, or a pre-built binary.
weight: 10
---

Somad runs on Linux and macOS. Every method below installs a single binary
called `soma`; no extra runtime or audio setup is needed.

## Homebrew (macOS and Linux)

```sh
brew tap samuelb/tap
brew install somad
```

This installs a pre-built binary from the [latest release](https://github.com/samuelb/somad/releases),
so no compiler is required. Upgrade later with `brew upgrade somad`.

Recent versions of Homebrew ask you to explicitly trust a third-party tap before
installing from it. If you see an "untrusted tap" error, run
`brew trust samuelb/tap` and try again.

## Debian / Ubuntu

Download the `.deb` package from the [latest release](https://github.com/samuelb/somad/releases)
and install it with:

```sh
sudo apt install ./somad_*_linux_$(dpkg --print-architecture).deb
```

## Fedora / RHEL / openSUSE

Download the `.rpm` package from the [latest release](https://github.com/samuelb/somad/releases)
and install it with:

```sh
sudo dnf install ./somad-*.$(uname -m).rpm
```

## Nix

Run Somad directly from the flake:

```sh
nix run github:samuelb/somad
```

Or install it into your profile:

```sh
nix profile install github:samuelb/somad
```

## Arch Linux

Every [release](https://github.com/samuelb/somad/releases) ships a pinned
`PKGBUILD` (plus the matching source tarball) as release assets. Download it
and run `makepkg -si`. Alternatively, `packaging/arch/PKGBUILD` in the
repository builds the latest git state directly.

## Pre-built binaries

Download the latest release for your platform from the
[releases page](https://github.com/samuelb/somad/releases):

- `soma-macos.dmg` for macOS (universal: Intel and Apple Silicon). Mount it
  and copy `soma` somewhere on your `PATH`.
- `soma_darwin_universal` for macOS as a bare binary (same universal build).
- `soma_linux_amd64` for x86_64 Linux.
- `soma_linux_arm64` for ARM64 Linux.

Rename the file to `soma` if you want a shorter command.

### macOS

The binary is not signed, so macOS may refuse to run it at first. Remove the
quarantine flag:

```sh
xattr -d com.apple.quarantine /path/to/soma
```

Alternatively, go to *System Settings › Privacy & Security* and click
*Open Anyway*.

### Linux

Make the binary executable:

```sh
chmod +x /path/to/soma
```

## Build from source

Prerequisites: Go 1.25 or newer. On Linux, the ALSA development library is
required for audio support:

```sh
# Debian/Ubuntu
sudo apt-get install libasound2-dev

# Fedora
sudo dnf install alsa-lib-devel

# Arch
sudo pacman -S alsa-lib
```

Then build:

```sh
git clone https://github.com/samuelb/somad.git
cd somad
go build -o soma ./cmd/soma
```

## Shell completions

Bash and Zsh completion scripts are available. The Debian/Ubuntu package,
the Arch package and the Nix flake install them automatically.

For manual setup:

**Bash**

```sh
soma completion bash | sudo tee /usr/share/bash-completion/completions/soma
```

Or source it from your shell profile without installing system-wide:

```sh
echo 'source <(soma completion bash)' >> ~/.bashrc
```

**Zsh**

```sh
soma completion zsh | sudo tee /usr/local/share/zsh/site-functions/_soma
```

After installing completions for Zsh, restart your shell or clear the
completion cache:

```sh
rm ~/.zcompdump && exec zsh
```

Completions cover all subcommands, global connection flags, daemon flags, and
`--json` options. `soma play` and `soma favorite` also complete channel IDs
from the locally cached channel catalog; completing never starts the daemon
or touches the network.
