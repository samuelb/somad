package main

import (
	"fmt"
	"os"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Version information (set via ldflags during build)
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	// Handle --version flag
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("somatui %s (commit: %s, built: %s)\n", version, commit, date)
		os.Exit(0)
	}

	// Initialize the audio player
	player, err := NewPlayer()
	if err != nil {
		fmt.Printf("Alas, there's been an error initializing the player: %v\n", err)
		os.Exit(1)
	}

	// Load application state
	state, err := LoadState()
	if err != nil {
		fmt.Printf("Alas, there's been an error loading state: %v\n", err)
		os.Exit(1)
	}

	// Create the main application model (need playing index for delegate)
	m := &model{
		player:  player,
		playing: -1,
		loading: true,
		state:   state,
		about: aboutInfo{
			Version: version,
			Commit:  commit,
			Date:    date,
		},
	}

	// Initialize the Bubble Tea list component with styled delegate
	delegate := newStyledDelegate(&m.playing)
	l := list.New([]list.Item{}, delegate, 0, 0)
	l.SetShowTitle(false) // We render our own header with column titles
	l.SetStatusBarItemName("channel", "channels")
	l.Styles.PaginationStyle = lipgloss.NewStyle().Foreground(subtleColor)
	l.Styles.HelpStyle = lipgloss.NewStyle().Foreground(subtleColor).Padding(0, 0, 0, 2)
	l.AdditionalFullHelpKeys = func() []key.Binding {
		return []key.Binding{
			key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "stop")),
			key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "about")),
		}
	}
	l.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{
			key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "stop")),
			key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "about")),
		}
	}
	m.list = l

	// Start the Bubble Tea program with window size handling
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Alas, there's been an error: %v\n", err)
		os.Exit(1)
	}
}
