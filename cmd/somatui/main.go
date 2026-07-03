package main

import (
	"fmt"
	"os"

	"somatui/internal/app"
	"somatui/internal/audio"
	"somatui/internal/platform"
	"somatui/internal/state"
	"somatui/internal/ui"

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
	player, err := audio.NewPlayer("SomaTUI/" + version)
	if err != nil {
		fmt.Printf("Alas, there's been an error initializing the player: %v\n", err)
		os.Exit(1)
	}

	// Load application state
	appState, err := state.LoadState()
	if err != nil {
		fmt.Printf("Alas, there's been an error loading state: %v\n", err)
		os.Exit(1)
	}

	// Restore the persisted volume
	player.SetVolume(appState.GetVolume())

	// Initialize MPRIS for desktop integration (Linux only)
	mpris, err := platform.NewMPRIS()
	if err != nil {
		// MPRIS is optional, continue without it
		fmt.Fprintf(os.Stderr, "Warning: MPRIS initialization failed: %v\n", err)
	}

	// Create the main application model (need playing ID for delegate)
	m := &app.Model{
		Player:    player,
		Loading:   true,
		State:     appState,
		MPRIS:     mpris,
		UserAgent: "SomaTUI/" + version,
		About: app.AboutInfo{
			Version: version,
			Commit:  commit,
			Date:    date,
		},
	}

	// Initialize the Bubble Tea list component with styled delegate
	delegate := ui.NewStyledDelegate(&m.PlayingID, m.IsMatch, m.IsFavorite)
	l := list.New([]list.Item{}, delegate, 0, 0)
	l.SetShowTitle(false)        // We render our own header with column titles
	l.SetFilteringEnabled(false) // Disable filtering, we use search instead
	l.SetStatusBarItemName("channel", "channels")
	l.Styles.PaginationStyle = lipgloss.NewStyle().Foreground(ui.SubtleColor)
	l.Styles.HelpStyle = lipgloss.NewStyle().Foreground(ui.SubtleColor).Padding(0, 0, 0, 2)

	fullHelp, shortHelp := app.NewHelpKeys()
	l.AdditionalFullHelpKeys = func() []key.Binding {
		return fullHelp
	}
	l.AdditionalShortHelpKeys = func() []key.Binding {
		return shortHelp
	}
	m.List = l

	// Start the Bubble Tea program with window size handling
	p := tea.NewProgram(m, tea.WithAltScreen())

	// Set MPRIS sender to allow D-Bus commands to control the player
	if mpris != nil {
		mpris.SetSender(p)
	}

	if _, err := p.Run(); err != nil {
		fmt.Printf("Alas, there's been an error: %v\n", err)
		if mpris != nil {
			mpris.Close()
		}
		os.Exit(1)
	}

	// Clean up MPRIS on normal exit
	if mpris != nil {
		mpris.Close()
	}
}
