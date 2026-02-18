package app

import (
	"fmt"
	"os"
	"sort"

	"somatui/internal/state"
	"somatui/internal/ui"

	"github.com/charmbracelet/bubbles/list"
)

// IsFavorite returns true if the item at the given index is a favorite.
func (m *Model) IsFavorite(idx int) bool {
	if m.State == nil {
		return false
	}
	items := m.List.Items()
	if idx < 0 || idx >= len(items) {
		return false
	}
	if i, ok := items[idx].(ui.Item); ok {
		return m.State.IsFavorite(i.Channel.ID)
	}
	return false
}

// ToggleFavorite toggles the favorite status of the currently selected channel.
func (m *Model) ToggleFavorite() {
	if m.State == nil {
		return
	}
	sel, ok := m.List.SelectedItem().(ui.Item)
	if !ok {
		return
	}
	selectedID := sel.Channel.ID
	m.State.ToggleFavorite(selectedID)
	if err := state.SaveState(m.State); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving state: %v\n", err)
	}

	// Re-sort items with favorites on top
	items := m.sortItemsWithFavorites(m.List.Items())
	m.List.SetItems(items)

	// Restore cursor to the same channel by ID
	for i, li := range items {
		if it, ok := li.(ui.Item); ok && it.Channel.ID == selectedID {
			m.List.Select(i)
			break
		}
	}

	// Update search matches since indices changed
	if m.SearchQuery != "" {
		m.UpdateSearchMatches()
	}
}

// sortItemsWithFavorites returns items sorted with favorites first,
// preserving relative order within each group.
func (m *Model) sortItemsWithFavorites(items []list.Item) []list.Item {
	if m.State == nil {
		return items
	}
	sorted := make([]list.Item, len(items))
	copy(sorted, items)
	sort.SliceStable(sorted, func(i, j int) bool {
		iItem, iOK := sorted[i].(ui.Item)
		jItem, jOK := sorted[j].(ui.Item)
		if !iOK || !jOK {
			return false
		}
		iFav := m.State.IsFavorite(iItem.Channel.ID)
		jFav := m.State.IsFavorite(jItem.Channel.ID)
		return iFav && !jFav
	})
	return sorted
}
