package ui

import "github.com/charmbracelet/lipgloss"

// Color palette - SomaFM inspired. Every color is adaptive: the Dark value
// is the original palette, the Light value a darker take that stays readable
// on light terminal backgrounds (lipgloss picks per the detected background).
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
)
