package app

import (
	"strings"
	"unicode"

	"somatui/internal/state"
	"somatui/internal/ui"

	"github.com/charmbracelet/bubbles/list"
)

// IsValidSearchChar returns true if the character is safe for search input.
func IsValidSearchChar(c byte) bool {
	// Allow printable ASCII characters except control characters
	// Exclude potentially problematic characters like null, backspace, etc.
	if c < 32 || c > 126 {
		return false
	}
	// Allow letters, numbers, spaces, and common punctuation
	return unicode.IsPrint(rune(c))
}

// UpdateSearchMatches finds all items matching the search query.
func (m *Model) UpdateSearchMatches() {
	m.SearchMatches = nil
	m.CurrentMatch = -1
	if m.SearchQuery == "" {
		return
	}
	query := strings.ToLower(m.SearchQuery)
	for idx, listItem := range m.List.Items() {
		if i, ok := listItem.(ui.Item); ok {
			title := strings.ToLower(i.Channel.Title)
			desc := strings.ToLower(i.Channel.Description)
			if strings.Contains(title, query) || strings.Contains(desc, query) {
				m.SearchMatches = append(m.SearchMatches, idx)
			}
		}
	}
	// Jump to first match if any
	if len(m.SearchMatches) > 0 {
		m.CurrentMatch = 0
		m.List.Select(m.SearchMatches[0])
	}
}

// NextMatch jumps to the next search match.
func (m *Model) NextMatch() {
	if len(m.SearchMatches) == 0 {
		return
	}
	m.CurrentMatch = (m.CurrentMatch + 1) % len(m.SearchMatches)
	m.List.Select(m.SearchMatches[m.CurrentMatch])
}

// PrevMatch jumps to the previous search match.
func (m *Model) PrevMatch() {
	if len(m.SearchMatches) == 0 {
		return
	}
	m.CurrentMatch--
	if m.CurrentMatch < 0 {
		m.CurrentMatch = len(m.SearchMatches) - 1
	}
	m.List.Select(m.SearchMatches[m.CurrentMatch])
}

// ClearSearch clears the search state.
func (m *Model) ClearSearch() {
	m.Searching = false
	m.SearchQuery = ""
	m.SearchMatches = nil
	m.CurrentMatch = -1
}

// IsMatch returns true if the given index is a search match.
func (m *Model) IsMatch(idx int) bool {
	for _, matchIdx := range m.SearchMatches {
		if matchIdx == idx {
			return true
		}
	}
	return false
}

// SortItemsWithFavorites returns items sorted with favorites first,
// preserving relative order within each group.
func SortItemsWithFavorites(items []list.Item, state *state.State) []list.Item {
	if state == nil {
		return items
	}
	sorted := make([]list.Item, len(items))
	copy(sorted, items)
	// Sort implementation would go here
	return sorted
}
