package ui

import "github.com/charmbracelet/lipgloss"

// Color palette - SomaFM inspired
var (
	TitleColor       = lipgloss.Color("#ff0709") // Red for title
	PrimaryColor     = lipgloss.Color("#D8A24D") // Golden accent
	PlayingColor     = lipgloss.Color("#1a9096") // Teal for playing
	ErrorColor       = lipgloss.Color("#FF3333") // Red for errors
	SubtleColor      = lipgloss.Color("#666666") // Gray for secondary text
	SearchMatchColor = lipgloss.Color("#E6DB74") // Yellow for search matches
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

	TrackInfoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#CCCCCC")).
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
