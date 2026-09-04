package app

import (
	"strings"
	"unicode"

	"somad/internal/channels"
	"somad/internal/ui"

	"github.com/charmbracelet/bubbles/list"
	"github.com/sahilm/fuzzy"
)

// PrintableRunes returns the printable runes of the input (including space
// and non-ASCII characters) as a string, dropping control characters.
func PrintableRunes(runes []rune) string {
	var b strings.Builder
	for _, r := range runes {
		if unicode.IsPrint(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// refreshVisibleItems recomputes which of the model's allItems are shown in
// m.List: the favorites-only filter (if active), then a fuzzy matches-only
// subset on top of that (if a search query is active). It keeps the cursor
// on the channel identified by keepID when that channel is still visible,
// or on the first item otherwise. Callers (search, favorites, and the
// channels-event handler) all funnel through this so the list and the
// filter state never disagree.
func (m *Model) refreshVisibleItems(keepID string) {
	items := m.allItems
	if m.FavoritesOnly {
		items = filterItems(items, func(i ui.Item) bool {
			return m.isFavoriteID(i.Channel.ID)
		})
	}

	if m.SearchQuery != "" {
		items = fuzzyMatchItems(items, m.SearchQuery)
	}
	m.List.SetItems(items)
	// Keep the cursor on the channel that was selected before this refresh
	// (a favorite toggle, the favorites-only reconcile, or a catalog
	// refresh) when it is still visible, instead of always snapping back to
	// the top (which, while searching, is the top-ranked match).
	// UpdateSearchMatches passes "" for keepID on every keystroke, so typing
	// still jumps to the top match: selectChannelByID("") is always a no-op.
	if !m.selectChannelByID(keepID) {
		m.List.Select(0)
	}
}

// filterItems returns the items for which keep reports true, preserving
// their relative order.
func filterItems(items []list.Item, keep func(ui.Item) bool) []list.Item {
	out := make([]list.Item, 0, len(items))
	for _, it := range items {
		if i, ok := it.(ui.Item); ok && keep(i) {
			out = append(out, it)
		}
	}
	return out
}

// itemFieldSource adapts a []list.Item slice and a field accessor to
// fuzzy.Source so github.com/sahilm/fuzzy can match against it directly.
type itemFieldSource struct {
	items []list.Item
	field func(channels.Channel) string
}

func (s itemFieldSource) String(i int) string {
	it, _ := s.items[i].(ui.Item)
	return s.field(it.Channel)
}

func (s itemFieldSource) Len() int { return len(s.items) }

// fuzzyMatchItems returns the items whose title or description fuzzy-match
// query, ordered by match quality: all title matches first (best score
// first), then any description-only matches (best score first). Matching is
// case-insensitive; github.com/sahilm/fuzzy folds case internally.
func fuzzyMatchItems(items []list.Item, query string) []list.Item {
	titleMatches := fuzzy.FindFrom(query, itemFieldSource{items, func(c channels.Channel) string { return c.Title }})
	descMatches := fuzzy.FindFrom(query, itemFieldSource{items, func(c channels.Channel) string { return c.Description }})

	seen := make(map[int]bool, len(titleMatches)+len(descMatches))
	out := make([]list.Item, 0, len(titleMatches)+len(descMatches))
	for _, mtc := range titleMatches {
		if !seen[mtc.Index] {
			seen[mtc.Index] = true
			out = append(out, items[mtc.Index])
		}
	}
	for _, mtc := range descMatches {
		if !seen[mtc.Index] {
			seen[mtc.Index] = true
			out = append(out, items[mtc.Index])
		}
	}
	return out
}

// UpdateSearchMatches recomputes the matches-only view for the current
// search query (fuzzy matching against title and description, ranked by
// score with title matches before description matches) and jumps the
// cursor to the top match. Called on every keystroke while searching.
func (m *Model) UpdateSearchMatches() {
	m.refreshVisibleItems("")
}

// NextMatch jumps to the next search match, wrapping around.
func (m *Model) NextMatch() {
	if n := m.matchCount(); n > 0 {
		m.List.Select((m.List.Index() + 1) % n)
	}
}

// PrevMatch jumps to the previous search match, wrapping around.
func (m *Model) PrevMatch() {
	if n := m.matchCount(); n > 0 {
		m.List.Select((m.List.Index() + n - 1) % n)
	}
}

// ClearSearch clears the search state and restores the full list, keeping
// the cursor on the channel that was selected within the filtered view.
func (m *Model) ClearSearch() {
	selectedID := m.selectedChannelID()
	m.Searching = false
	m.SearchQuery = ""
	m.refreshVisibleItems(selectedID)
}

// IsMatch reports whether the row at idx (into m.List.Items()) is a search
// match, for the delegate's highlighting. While a query is active the list
// holds only matches, so every visible row is one.
func (m *Model) IsMatch(idx int) bool {
	return idx >= 0 && idx < m.matchCount()
}
