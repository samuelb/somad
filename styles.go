package main

import "github.com/charmbracelet/lipgloss"

// Color palette - SomaFM inspired
var (
	titleColor     = lipgloss.Color("#ff0709") // Red for title
	primaryColor   = lipgloss.Color("#D8A24D") // Golden accent
	playingColor   = lipgloss.Color("#1a9096") // Teal for playing
	errorColor     = lipgloss.Color("#FF3333") // Red for errors
	subtleColor    = lipgloss.Color("#666666") // Gray for secondary text
)

// Styles
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(titleColor).
			MarginLeft(2)

	statusBarStyle = lipgloss.NewStyle().
			Padding(0, 1).
			MarginTop(1)

	statusPlayingStyle = lipgloss.NewStyle().
				Foreground(playingColor).
				Bold(true)

	statusStoppedStyle = lipgloss.NewStyle().
				Foreground(subtleColor)

	trackInfoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#CCCCCC")).
			Italic(true)

	loadingStyle = lipgloss.NewStyle().
			Foreground(primaryColor).
			Bold(true).
			Padding(2, 4)

	errorBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(errorColor).
			Foreground(errorColor).
			Padding(1, 2).
			MarginTop(2).
			MarginLeft(2)

	aboutBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(primaryColor).
			Background(lipgloss.Color("#1a1a1a")).
			Padding(1, 3)
)
