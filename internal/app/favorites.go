package app

import (
	"slices"

	"somad/internal/ui"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// IsFavorite returns true if the item at the given index is a favorite.
func (m *Model) IsFavorite(idx int) bool {
	items := m.List.Items()
	if idx < 0 || idx >= len(items) {
		return false
	}
	if i, ok := items[idx].(ui.Item); ok {
		return m.isFavoriteID(i.Channel.ID)
	}
	return false
}

func (m *Model) isFavoriteID(id string) bool {
	return slices.Contains(m.Favorites, id)
}

// ToggleFavorite optimistically toggles the selected channel's favorite flag
// locally (for a snappy re-sort) and returns a command that persists it on
// the server; the server's channels event reconciles the final state.
func (m *Model) ToggleFavorite() tea.Cmd {
	sel, ok := m.List.SelectedItem().(ui.Item)
	if !ok {
		return nil
	}
	selectedID := sel.Channel.ID

	if i := slices.Index(m.Favorites, selectedID); i >= 0 {
		m.Favorites = slices.Delete(slices.Clone(m.Favorites), i, i+1)
	} else {
		m.Favorites = append(slices.Clone(m.Favorites), selectedID)
	}

	// Re-sort items with favorites on top, keeping the cursor on the same channel
	m.List.SetItems(m.sortItemsWithFavorites(m.List.Items()))
	m.selectChannelByID(selectedID)

	// Update search matches since indices changed
	if m.SearchQuery != "" {
		m.UpdateSearchMatches()
	}

	b := m.Backend
	return func() tea.Msg {
		if b == nil {
			return nil
		}
		favs, err := b.ToggleFavorite(selectedID)
		if err != nil {
			return requestErr("favorite", err)
		}
		return FavoritesMsg{Favorites: favs}
	}
}

// applyFavorites installs the server's authoritative favorites list and
// re-sorts, keeping the cursor on the selected channel. It reconciles the
// optimistic flip in ToggleFavorite with what the server actually persisted.
func (m *Model) applyFavorites(favs []string) {
	m.Favorites = favs
	var selectedID string
	if sel, ok := m.List.SelectedItem().(ui.Item); ok {
		selectedID = sel.Channel.ID
	}
	m.List.SetItems(m.sortItemsWithFavorites(m.List.Items()))
	m.selectChannelByID(selectedID)
	if m.SearchQuery != "" {
		m.UpdateSearchMatches()
	}
}

// sortItemsWithFavorites returns items partitioned with favorites first,
// preserving relative order within each group. O(n) via two-pass partition.
func (m *Model) sortItemsWithFavorites(items []list.Item) []list.Item {
	sorted := make([]list.Item, 0, len(items))
	for _, item := range items {
		if i, ok := item.(ui.Item); ok && m.isFavoriteID(i.Channel.ID) {
			sorted = append(sorted, item)
		}
	}
	for _, item := range items {
		if i, ok := item.(ui.Item); ok && !m.isFavoriteID(i.Channel.ID) {
			sorted = append(sorted, item)
		}
	}
	return sorted
}
