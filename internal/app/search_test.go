package app

import (
	"testing"

	"somad/internal/channels"
	"somad/internal/ui"

	"github.com/charmbracelet/bubbles/list"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrintableRunes(t *testing.T) {
	tests := []struct {
		name  string
		input []rune
		want  string
	}{
		{"ascii letters", []rune("aZ5"), "aZ5"},
		{"space", []rune{' '}, " "},
		{"punctuation", []rune("-_."), "-_."},
		{"non-ascii", []rune("über groß"), "über groß"},
		{"cjk", []rune("音楽"), "音楽"},
		{"control chars dropped", []rune{0, 8, 9, 10, 127, 'a'}, "a"},
		{"empty", nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, PrintableRunes(tt.input))
		})
	}
}

func TestUpdateSearchMatches_EmptyQuery(t *testing.T) {
	m := newTestModel(t)

	m.SearchQuery = ""
	m.UpdateSearchMatches()

	assert.Zero(t, m.matchCount())
}

func TestUpdateSearchMatches_WithMatches(t *testing.T) {
	m := newTestModel(t)

	m.SearchQuery = "zone"
	m.UpdateSearchMatches()

	// The list becomes a matches-only view, so "Drone Zone" (the only
	// match) is the sole, and therefore first, item shown.
	require.Equal(t, 1, m.matchCount())
	assert.Equal(t, 0, m.List.Index())
	require.Len(t, m.List.Items(), 1)
	sel, ok := m.List.Items()[0].(ui.Item)
	require.True(t, ok)
	assert.Equal(t, "dronezone", sel.Channel.ID)
}

func TestUpdateSearchMatches_MatchesDescription(t *testing.T) {
	m := newTestModel(t)

	m.SearchQuery = "spy"
	m.UpdateSearchMatches()

	// "Secret Agent" description contains "spy"
	assert.Equal(t, 1, m.matchCount())
}

func TestUpdateSearchMatches_CaseInsensitive(t *testing.T) {
	m := newTestModel(t)

	m.SearchQuery = "GROOVE"
	m.UpdateSearchMatches()

	assert.Equal(t, 1, m.matchCount())
}

func TestUpdateSearchMatches_NoMatches(t *testing.T) {
	m := newTestModel(t)

	m.SearchQuery = "xyzzy"
	m.UpdateSearchMatches()

	assert.Zero(t, m.matchCount())
}

func TestUpdateSearchMatches_MultipleMatches(t *testing.T) {
	m := newTestModel(t)

	// "ambient" appears in Groove Salad description and Drone Zone genre
	m.SearchQuery = "ambient"
	m.UpdateSearchMatches()

	assert.GreaterOrEqual(t, m.matchCount(), 2)
	assert.Equal(t, 0, m.List.Index())
	// List should be scrolled to first match
	assert.Equal(t, 0, m.List.Index())
}

func TestRefreshVisibleItems_KeepsSelectionWithinFilteredView(t *testing.T) {
	m := newTestModel(t)

	// "ambient" matches both Groove Salad's and Drone Zone's descriptions.
	m.SearchQuery = "ambient"
	m.UpdateSearchMatches()
	require.GreaterOrEqual(t, m.matchCount(), 2, "test setup: need at least two matches")
	m.NextMatch()
	sel, ok := m.List.SelectedItem().(ui.Item)
	require.True(t, ok)
	selectedID := sel.Channel.ID
	movedIndex := m.List.Index()
	require.NotZero(t, movedIndex, "test setup: cursor must have moved off the top match")

	// A refresh that funnels through refreshVisibleItems with keepID set (a
	// favorite toggle, the FavoritesMsg reconcile, or a catalog refresh)
	// must not snap the cursor back to the top match.
	m.refreshVisibleItems(selectedID)

	sel, ok = m.List.SelectedItem().(ui.Item)
	require.True(t, ok)
	assert.Equal(t, selectedID, sel.Channel.ID, "the selection must survive the refresh")
	assert.Equal(t, movedIndex, m.List.Index())
}

func TestRefreshVisibleItems_FallsBackToTopMatchWhenKeepIDNotVisible(t *testing.T) {
	m := newTestModel(t)

	m.SearchQuery = "ambient"
	m.UpdateSearchMatches()
	require.NotZero(t, m.matchCount())

	// A channel outside the filtered view (e.g. one search query away, or
	// simply never in it) falls back to the top match, same as before.
	m.refreshVisibleItems("secretagent")

	assert.Equal(t, 0, m.List.Index())
}

func TestNextMatch_WrapsAround(t *testing.T) {
	m := newTestModel(t)

	m.SearchQuery = "ambient"
	m.UpdateSearchMatches()
	require := m.matchCount()
	if require < 2 {
		t.Skip("need at least two matches for wrap-around test")
	}

	// Advance to last match
	for i := 0; i < m.matchCount()-1; i++ {
		m.NextMatch()
	}
	assert.Equal(t, m.matchCount()-1, m.List.Index())

	// One more should wrap to first
	m.NextMatch()
	assert.Equal(t, 0, m.List.Index())
}

func TestPrevMatch_WrapsAround(t *testing.T) {
	m := newTestModel(t)

	m.SearchQuery = "ambient"
	m.UpdateSearchMatches()
	if m.matchCount() < 2 {
		t.Skip("need at least two matches for wrap-around test")
	}

	// Go backward from first match should wrap to last
	m.PrevMatch()
	assert.Equal(t, m.matchCount()-1, m.List.Index())
}

func TestNextMatch_NoMatches(t *testing.T) {
	m := newTestModel(t)

	// Calling with no matches should not panic
	m.NextMatch()
	assert.Zero(t, m.matchCount())
}

func TestPrevMatch_NoMatches(t *testing.T) {
	m := newTestModel(t)

	m.PrevMatch()
	assert.Zero(t, m.matchCount())
}

func TestClearSearch(t *testing.T) {
	m := newTestModel(t)

	m.Searching = true
	m.SearchQuery = "groove"
	m.UpdateSearchMatches()

	m.ClearSearch()

	assert.False(t, m.Searching)
	assert.Empty(t, m.SearchQuery)
	assert.Zero(t, m.matchCount())
}

func TestIsMatch(t *testing.T) {
	m := newTestModel(t)
	m.SearchQuery = "ambient"
	m.UpdateSearchMatches()
	n := m.matchCount()
	require.GreaterOrEqual(t, n, 2, "test setup: need at least two matches")

	// While a query is active the list holds only matches, so every visible
	// row is one; out-of-range indices are not.
	assert.True(t, m.IsMatch(0))
	assert.True(t, m.IsMatch(n-1))
	assert.False(t, m.IsMatch(n))
	assert.False(t, m.IsMatch(-1))
}

func TestIsMatch_NoQuery(t *testing.T) {
	m := newTestModel(t)

	assert.False(t, m.IsMatch(0))
	assert.False(t, m.IsMatch(1))
}

func TestFuzzyMatchItems_TitleRankedBeforeDescription(t *testing.T) {
	items := []list.Item{
		ui.Item{Channel: channels.Channel{ID: "a", Title: "Something Else", Description: "nothing relevant"}},
		ui.Item{Channel: channels.Channel{ID: "b", Title: "Zebra Crossing", Description: "unrelated"}},
		ui.Item{Channel: channels.Channel{ID: "c", Title: "Unrelated Too", Description: "a zebra wanders by"}},
	}

	result := fuzzyMatchItems(items, "zebra")

	require.Len(t, result, 2)
	first, ok := result[0].(ui.Item)
	require.True(t, ok)
	assert.Equal(t, "b", first.Channel.ID, "the title match ranks before the description-only match")
	second, ok := result[1].(ui.Item)
	require.True(t, ok)
	assert.Equal(t, "c", second.Channel.ID)
}

func TestFuzzyMatchItems_CaseInsensitive(t *testing.T) {
	items := []list.Item{
		ui.Item{Channel: channels.Channel{ID: "a", Title: "Groove Salad"}},
	}

	result := fuzzyMatchItems(items, "GROOVE")

	assert.Len(t, result, 1)
}

func TestFuzzyMatchItems_NoMatch(t *testing.T) {
	items := []list.Item{
		ui.Item{Channel: channels.Channel{ID: "a", Title: "Groove Salad"}},
	}

	assert.Empty(t, fuzzyMatchItems(items, "xyzzy"))
}
