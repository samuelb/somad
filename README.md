# SomaTUI

**✨ This project was entirely vibe-coded. ✨**

A modern, TUI (Terminal User Interface) client for streaming and exploring SomaFM radio channels. Built in Go using the [Bubble Tea](https://github.com/charmbracelet/bubbletea) framework, it features a snappy interface, audio playback, and now-playing track metadata.

## Features

- Browse and filter the full list of SomaFM radio channels
- Play high-quality MP3 streams directly in your terminal
- View real-time track information (artist/title) from ICY metadata
- Buffered streaming with automatic reconnection on network issues
- Styled UI with color-coded playback states and visual indicators
- Select and remember your last-played channel
- Fast startup with cached channels and background refresh
- Smooth, keyboard-driven navigation and playback controls

## Installation

### Prerequisites
- Go 1.20 or newer
- Linux (audio may require features specific to your system) or MacOS

### Build from Source

```sh
git clone https://github.com/yourusername/somatui.git
cd somatui
go build -o somatui
```

## Usage

Simply run:

```sh
./somatui
```

### Keyboard Controls
- <kbd>Up/Down</kbd> or <kbd>j/k</kbd>: Navigate channels
- <kbd>Enter</kbd> or <kbd>Space</kbd>: Play selected channel
- <kbd>s</kbd>: Stop playback
- <kbd>/</kbd>: Filter channels
- <kbd>q</kbd> or <kbd>Ctrl+C</kbd>: Quit

## Configuration & Cache

- Config and cache files are stored under your XDG-compliant config/cache directory, e.g. `~/.config/somacli` and `~/.cache/somacli`.
- The client remembers your last selected channel across sessions.

## Dependencies

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) (TUI framework)
- [Bubbles](https://github.com/charmbracelet/bubbles) (TUI components)
- [Lip Gloss](https://github.com/charmbracelet/lipgloss) (styling)
- [oto](https://github.com/ebitengine/oto) (audio output)
- [go-mp3](https://github.com/hajimehoshi/go-mp3) (MP3 decoding)

See `go.mod` for the full dependency list.

## Contributing

Contributions are welcome! Feel free to open issues or pull requests.

1. Fork the repo
2. Create a feature branch
3. Make your changes
4. Submit a pull request

## License

[MIT](LICENSE)

---

_This project is not affiliated with SomaFM. All content and station streams are provided by [somafm.com](https://somafm.com/)._
