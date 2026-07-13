class Somad < Formula
  desc "Client for streaming SomaFM radio channels"
  homepage "https://github.com/samuelb/somad"
  version "0.13.0"
  license "MIT"

  on_macos do
    url "https://github.com/samuelb/somad/releases/download/v#{version}/soma_darwin_universal"
    # Release automation replaces this placeholder with the published checksum.
    sha256 "REPLACE_WITH_DARWIN_UNIVERSAL_SHA256"
  end

  on_linux do
    on_arm do
      url "https://github.com/samuelb/somad/releases/download/v#{version}/soma_linux_arm64"
      sha256 "REPLACE_WITH_LINUX_ARM64_SHA256"
    end
    on_intel do
      url "https://github.com/samuelb/somad/releases/download/v#{version}/soma_linux_amd64"
      sha256 "REPLACE_WITH_LINUX_AMD64_SHA256"
    end
  end

  def install
    bin.install Dir["soma_*"].first => "soma"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/soma --version")
  end
end
