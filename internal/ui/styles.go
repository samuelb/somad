package ui

import "github.com/charmbracelet/lipgloss"

// Color palette, following the somafm.com website colors. Every color is
// adaptive: the Dark value is the original palette, the Light value a darker
// take that stays readable on light terminal backgrounds (lipgloss picks per
// the detected background). The TUI must keep working out of the box on both
// dark and light terminals; a user-configurable theme is deliberately not
// planned (see docs/adr/0020).
var (
	TitleColor       = lipgloss.AdaptiveColor{Light: "#C40608", Dark: "#ff0709"} // Red for title
	PrimaryColor     = lipgloss.AdaptiveColor{Light: "#8F6400", Dark: "#D8A24D"} // Golden accent
	PlayingColor     = lipgloss.AdaptiveColor{Light: "#0E686D", Dark: "#1a9096"} // Teal for playing
	ErrorColor       = lipgloss.AdaptiveColor{Light: "#C21807", Dark: "#FF3333"} // Red for errors
	SubtleColor      = lipgloss.AdaptiveColor{Light: "#5C5C5C", Dark: "#666666"} // Gray for secondary text
	SearchMatchColor = lipgloss.AdaptiveColor{Light: "#7A6800", Dark: "#E6DB74"} // Yellow for search matches
	TextColor        = lipgloss.AdaptiveColor{Light: "#1A1A1A", Dark: "#FFFFFF"} // Primary text
	MutedTextColor   = lipgloss.AdaptiveColor{Light: "#3D3D3D", Dark: "#CCCCCC"} // De-emphasized text
)

// Styles
var (
	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(TitleColor).
			MarginLeft(2)

	StatusBarStyle = lipgloss.NewStyle().
			Padding(0, 1).
			MarginTop(1)

	StatusPlayingStyle = lipgloss.NewStyle().
				Foreground(PlayingColor).
				Bold(true)

	StatusStoppedStyle = lipgloss.NewStyle().
				Foreground(SubtleColor)

	StatusConnectingStyle = lipgloss.NewStyle().
				Foreground(PrimaryColor).
				Bold(true)

	TrackInfoStyle = lipgloss.NewStyle().
			Foreground(MutedTextColor).
			Italic(true)

	LoadingStyle = lipgloss.NewStyle().
			Foreground(PrimaryColor).
			Bold(true).
			Padding(2, 4)

	ErrorBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ErrorColor).
			Foreground(ErrorColor).
			Padding(1, 2).
			MarginTop(2).
			MarginLeft(2)

	SearchBarStyle = lipgloss.NewStyle().
			Foreground(SearchMatchColor).
			MarginLeft(2)

	// Footer styles (about and history footers under the status bar).
	FooterSeparatorStyle = lipgloss.NewStyle().
				Foreground(SubtleColor)

	FooterBodyStyle = lipgloss.NewStyle().
			Foreground(SubtleColor).
			Padding(0, 0, 0, 2)

	// List row styles. The delegate sets the width per render (the list can
	// resize), so these carry everything but Width.
	listenerNormalStyle   = lipgloss.NewStyle().Foreground(SubtleColor).Align(lipgloss.Right)
	listenerSelectedStyle = lipgloss.NewStyle().Foreground(MutedTextColor).Align(lipgloss.Right)
	listenerPlayingStyle  = lipgloss.NewStyle().Foreground(PlayingColor).Align(lipgloss.Right)
	listenerMatchStyle    = lipgloss.NewStyle().Foreground(SearchMatchColor).Align(lipgloss.Right)
	playingTitleStyle     = lipgloss.NewStyle().Foreground(PlayingColor).Padding(0, 0, 0, 2)
	matchTitleStyle       = lipgloss.NewStyle().Foreground(SearchMatchColor).Padding(0, 0, 0, 2)
	unselectedDescStyle   = lipgloss.NewStyle().Foreground(SubtleColor).Padding(0, 0, 0, 2)
)
