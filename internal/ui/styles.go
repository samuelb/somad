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
	// Chrome: card borders and the unlit part of the volume gauge.
	BorderColor = lipgloss.AdaptiveColor{Light: "#B4B4B4", Dark: "#444444"} // Idle card borders
	DimColor    = lipgloss.AdaptiveColor{Light: "#D2D2D2", Dark: "#333333"} // Unlit gauge cells
)

// Styles
var (
	// TitleStyle is the "SomaFM Stations" title that opens the header.
	TitleStyle = lipgloss.NewStyle().Bold(true).Foreground(TitleColor)

	// FavoritesSectionStyle names the favorites-only view in the header.
	FavoritesSectionStyle = lipgloss.NewStyle().Bold(true).Foreground(PrimaryColor)

	SubtleStyle = lipgloss.NewStyle().Foreground(SubtleColor)
	MutedStyle  = lipgloss.NewStyle().Foreground(MutedTextColor)
	TextStyle   = lipgloss.NewStyle().Foreground(TextColor)
	BoldStyle   = lipgloss.NewStyle().Foreground(TextColor).Bold(true)
	ErrorStyle  = lipgloss.NewStyle().Foreground(ErrorColor)
	AccentStyle = lipgloss.NewStyle().Foreground(PrimaryColor)
	DimStyle    = lipgloss.NewStyle().Foreground(DimColor)

	StatusPlayingStyle = lipgloss.NewStyle().
				Foreground(PlayingColor).
				Bold(true)

	StatusStoppedStyle = lipgloss.NewStyle().
				Foreground(SubtleColor).
				Bold(true)

	StatusConnectingStyle = lipgloss.NewStyle().
				Foreground(PrimaryColor).
				Bold(true)

	StatusErrorStyle = lipgloss.NewStyle().
				Foreground(ErrorColor).
				Bold(true)

	// TrackInfoStyle renders a stream title that does not split into artist
	// and title.
	TrackInfoStyle = lipgloss.NewStyle().
			Foreground(TextColor).
			Italic(true)

	LoadingStyle = lipgloss.NewStyle().
			Foreground(MutedTextColor)

	SpinnerStyle = lipgloss.NewStyle().
			Foreground(PrimaryColor).
			Bold(true)

	ErrorBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ErrorColor).
			Padding(1, 3)

	// Search bar: the "/" prompt, the query, and the block cursor while
	// typing.
	SearchPromptStyle = lipgloss.NewStyle().Foreground(PrimaryColor).Bold(true)
	SearchQueryStyle  = lipgloss.NewStyle().Foreground(SearchMatchColor)
	SearchCursorStyle = lipgloss.NewStyle().Reverse(true)

	// Help bar keys and descriptions.
	HelpKeyStyle  = lipgloss.NewStyle().Foreground(MutedTextColor)
	HelpDescStyle = lipgloss.NewStyle().Foreground(SubtleColor)
	HelpSepStyle  = lipgloss.NewStyle().Foreground(BorderColor)

	// Pagination dots under the list.
	ActiveDotStyle   = lipgloss.NewStyle().Foreground(PrimaryColor)
	InactiveDotStyle = lipgloss.NewStyle().Foreground(BorderColor)

	// List row styles: title, genre, description and listener count per
	// row state (normal, selected, playing), and the marks and tuning
	// label drawn beside them.
	normalTitleStyle   = lipgloss.NewStyle().Foreground(TextColor)
	selectedTitleStyle = lipgloss.NewStyle().Foreground(PrimaryColor).Bold(true)
	playingTitleStyle  = lipgloss.NewStyle().Foreground(PlayingColor).Bold(true)
	matchStyle         = lipgloss.NewStyle().Foreground(SearchMatchColor).Bold(true).Underline(true)
	genreStyle         = lipgloss.NewStyle().Foreground(SubtleColor)
	normalDescStyle    = lipgloss.NewStyle().Foreground(SubtleColor)
	selectedDescStyle  = lipgloss.NewStyle().Foreground(MutedTextColor)
	selectBarStyle     = lipgloss.NewStyle().Foreground(PrimaryColor)
	FavoriteStyle      = lipgloss.NewStyle().Foreground(TitleColor)
	playingMarkStyle   = lipgloss.NewStyle().Foreground(PlayingColor)
	countNormalStyle   = lipgloss.NewStyle().Foreground(SubtleColor)
	countSelectedStyle = lipgloss.NewStyle().Foreground(MutedTextColor)
	countPlayingStyle  = lipgloss.NewStyle().Foreground(PlayingColor)
	tuningStyle        = lipgloss.NewStyle().Foreground(PrimaryColor)
)
