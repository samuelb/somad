package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
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

	assert.Len(t, m.SearchMatches, 1)
	assert.Equal(t, 0, m.CurrentMatch)
	// "Drone Zone" is index 1 in the list
	assert.Equal(t, 1, m.SearchMatches[0])
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
