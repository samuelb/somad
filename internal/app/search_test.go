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

	assert.Nil(t, m.SearchMatches)
	assert.Equal(t, -1, m.CurrentMatch)
}

func TestUpdateSearchMatches_WithMatches(t *testing.T) {
	m := newTestModel(t)

	m.SearchQuery = "zone"
	m.UpdateSearchMatches()

	// The list becomes a matches-only view, so "Drone Zone" (the only
	// match) is the sole, and therefore first, item shown.
	require.Len(t, m.SearchMatches, 1)
	assert.Equal(t, 0, m.CurrentMatch)
	assert.Equal(t, 0, m.SearchMatches[0])
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
	assert.Len(t, m.SearchMatches, 1)
}

func TestUpdateSearchMatches_CaseInsensitive(t *testing.T) {
	m := newTestModel(t)

	m.SearchQuery = "GROOVE"
	m.UpdateSearchMatches()

	assert.Len(t, m.SearchMatches, 1)
}

func TestUpdateSearchMatches_NoMatches(t *testing.T) {
	m := newTestModel(t)

	m.SearchQuery = "xyzzy"
	m.UpdateSearchMatches()

	assert.Empty(t, m.SearchMatches)
	assert.Equal(t, -1, m.CurrentMatch)
}

func TestUpdateSearchMatches_MultipleMatches(t *testing.T) {
	m := newTestModel(t)

	// "ambient" appears in Groove Salad description and Drone Zone genre
	m.SearchQuery = "ambient"
	m.UpdateSearchMatches()

	assert.GreaterOrEqual(t, len(m.SearchMatches), 2)
	assert.Equal(t, 0, m.CurrentMatch)
	// List should be scrolled to first match
	assert.Equal(t, m.SearchMatches[0], m.List.Index())
}

func TestRefreshVisibleItems_KeepsSelectionWithinFilteredView(t *testing.T) {
	m := newTestModel(t)

	// "ambient" matches both Groove Salad's and Drone Zone's descriptions.
	m.SearchQuery = "ambient"
	m.UpdateSearchMatches()
	require.GreaterOrEqual(t, len(m.SearchMatches), 2, "test setup: need at least two matches")
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
	assert.Equal(t, movedIndex, m.CurrentMatch, "CurrentMatch must track the kept selection, not the top match")
}

func TestRefreshVisibleItems_FallsBackToTopMatchWhenKeepIDNotVisible(t *testing.T) {
	m := newTestModel(t)

	m.SearchQuery = "ambient"
	m.UpdateSearchMatches()
	require.NotEmpty(t, m.SearchMatches)

	// A channel outside the filtered view (e.g. one search query away, or
	// simply never in it) falls back to the top match, same as before.
	m.refreshVisibleItems("secretagent")

	assert.Equal(t, 0, m.CurrentMatch)
	assert.Equal(t, 0, m.List.Index())
}

func TestNextMatch_WrapsAround(t *testing.T) {
	m := newTestModel(t)

	m.SearchQuery = "ambient"
	m.UpdateSearchMatches()
	require := len(m.SearchMatches)
	if require < 2 {
		t.Skip("need at least two matches for wrap-around test")
	}

	// Advance to last match
	for i := 0; i < len(m.SearchMatches)-1; i++ {
		m.NextMatch()
	}
	assert.Equal(t, len(m.SearchMatches)-1, m.CurrentMatch)

	// One more should wrap to first
	m.NextMatch()
	assert.Equal(t, 0, m.CurrentMatch)
}

func TestPrevMatch_WrapsAround(t *testing.T) {
	m := newTestModel(t)

	m.SearchQuery = "ambient"
	m.UpdateSearchMatches()
	if len(m.SearchMatches) < 2 {
		t.Skip("need at least two matches for wrap-around test")
	}

	// Go backward from first match should wrap to last
	m.PrevMatch()
	assert.Equal(t, len(m.SearchMatches)-1, m.CurrentMatch)
}

func TestNextMatch_NoMatches(t *testing.T) {
	m := newTestModel(t)

	// Calling with no matches should not panic
	m.NextMatch()
	assert.Equal(t, -1, m.CurrentMatch)
}

func TestPrevMatch_NoMatches(t *testing.T) {
	m := newTestModel(t)

	m.PrevMatch()
	assert.Equal(t, -1, m.CurrentMatch)
}

func TestClearSearch(t *testing.T) {
	m := newTestModel(t)

	m.Searching = true
	m.SearchQuery = "groove"
	m.SearchMatches = []int{0}
	m.CurrentMatch = 0

	m.ClearSearch()

	assert.False(t, m.Searching)
	assert.Empty(t, m.SearchQuery)
	assert.Nil(t, m.SearchMatches)
	assert.Equal(t, -1, m.CurrentMatch)
}

func TestIsMatch(t *testing.T) {
	m := newTestModel(t)

	m.SearchMatches = []int{1, 3}

	assert.False(t, m.IsMatch(0))
	assert.True(t, m.IsMatch(1))
	assert.False(t, m.IsMatch(2))
	assert.True(t, m.IsMatch(3))
}

func TestIsMatch_NoMatches(t *testing.T) {
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
