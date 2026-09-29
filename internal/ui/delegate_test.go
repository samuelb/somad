package ui

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"somad/internal/channels"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
)

// newTestList returns a list of channelItems at 80x24 drawn by a delegate
// whose rows show info(index).
func newTestList(channelItems []channels.Channel, info func(int) RowInfo) (list.Model, StyledDelegate) {
	items := make([]list.Item, len(channelItems))
	for i, ch := range channelItems {
		items[i] = Item{Channel: ch}
	}
	delegate := NewStyledDelegate(func(i int, _ Item) RowInfo { return info(i) }, nil)
	l := list.New(items, delegate, 80, 24)
	l.SetShowTitle(false)
	l.SetFilteringEnabled(false)
	return l, delegate
}

// plain shows every row without marks.
func plain(int) RowInfo { return RowInfo{} }

// render renders the row at index.
func render(l list.Model, d StyledDelegate, index int) string {
	var buf bytes.Buffer
	d.Render(&buf, l, index, l.Items()[index])
	return buf.String()
}

func testChannels() []channels.Channel {
	return []channels.Channel{
		{
			ID:          "groovesalad",
			Title:       "Groove Salad",
			Description: "A nicely chilled plate of ambient beats",
			Genre:       "ambient",
			Listeners:   "1000",
			Playlists: []channels.Playlist{
				{URL: "http://somafm.com/groovesalad.pls", Format: "mp3", Quality: "high"},
				{URL: "http://somafm.com/groovesalad.pls", Format: "aac", Quality: "low"},
			},
		},
		{
			ID:          "dronezone",
			Title:       "Drone Zone",
			Description: "Atmospheric texture and ambient space music",
			Genre:       "ambient|space",
			Listeners:   "500",
			Playlists: []channels.Playlist{
				{URL: "http://somafm.com/dronezone.pls", Format: "mp3", Quality: "high"},
			},
		},
		{
			ID:          "secretagent",
			Title:       "Secret Agent",
			Description: "The soundtrack for your spy movie marathon",
			Genre:       "lounge|spy",
			Listeners:   "750",
			Playlists: []channels.Playlist{
				{URL: "http://somafm.com/secretagent.pls", Format: "mp3", Quality: "high"},
			},
		},
	}
}

func TestDelegateRender_Normal(t *testing.T) {
	l, delegate := newTestList(testChannels(), plain)

	output := render(l, delegate, 1) // Drone Zone, not selected (index 0 is)

	assert.Contains(t, output, "Drone Zone")
	assert.Contains(t, output, "ambient · space", "the genre, pipes turned to dots")
	assert.Contains(t, output, "Atmospheric texture", "the description")
	assert.Contains(t, output, "500", "the listener count")
	assert.NotContains(t, output, "┃", "only the selected row has the bar")
	assert.Equal(t, 2, lipgloss.Height(output), "two lines per row")
}

func TestDelegateRender_Selected(t *testing.T) {
	l, delegate := newTestList(testChannels(), plain)

	output := render(l, delegate, 0) // index 0 is selected by default

	assert.Contains(t, output, "Groove Salad")
	for _, line := range strings.Split(output, "\n") {
		assert.True(t, strings.HasPrefix(line, " ┃ "), "the selection bar runs down both lines: %q", line)
	}
}

func TestDelegateRender_Playing(t *testing.T) {
	l, delegate := newTestList(testChannels(), func(i int) RowInfo { return RowInfo{Playing: i == 1} })

	output := render(l, delegate, 1) // Drone Zone is playing but not selected

	assert.Contains(t, output, "▶ Drone Zone", "the playing mark")
}

func TestDelegateRender_Tuning(t *testing.T) {
	l, delegate := newTestList(testChannels(), func(i int) RowInfo { return RowInfo{Tuning: "tuning in…"} })
	frame := 7
	delegate.Frame = &frame

	output := render(l, delegate, 1)

	assert.Contains(t, output, "Drone Zone  "+Spinner(7)+" tuning in…", "a spinner and the label follow the title")
	assert.NotContains(t, output, "ambient · space", "in place of the genre")
}

func TestDelegateRender_SearchMatch(t *testing.T) {
	l, delegate := newTestList(testChannels(), func(i int) RowInfo {
		if i == 2 {
			return RowInfo{TitleMatches: []int{0, 1, 2}}
		}
		return RowInfo{}
	})

	output := render(l, delegate, 2) // Secret Agent is a match but not selected

	assert.Contains(t, ansi.Strip(output), "Secret Agent")
}

func TestDelegateRender_Favorite(t *testing.T) {
	l, delegate := newTestList(testChannels(), func(i int) RowInfo { return RowInfo{Favorite: i == 1} })

	output := render(l, delegate, 1) // Drone Zone is favorite but not selected

	assert.Contains(t, output, "Drone Zone")
	assert.Contains(t, output, "♥ Drone Zone") // favorite indicator
}

func TestDelegateRender_PlayingFavorite(t *testing.T) {
	l, delegate := newTestList(testChannels(), func(i int) RowInfo { return RowInfo{Playing: true, Favorite: true} })

	output := render(l, delegate, 1)

	assert.Contains(t, output, "♥ ▶ Drone Zone", "the heart first, then the playing mark")
}

func TestDelegateRender_InvalidItem(t *testing.T) {
	l, delegate := newTestList(testChannels(), plain)

	// Pass an item that isn't our `Item` type — should return without writing
	var buf bytes.Buffer
	delegate.Render(&buf, l, 0, mockListItem{})

	assert.Empty(t, buf.String())
}

func TestDelegateRender_FitsTheWidth(t *testing.T) {
	long := testChannels()
	long[0].Title = strings.Repeat("Very Long Channel Title ", 6)
	long[0].Description = strings.Repeat("An endless description ", 10)
	long[0].Genre = "ambient|electronic|downtempo|chill"
	for _, width := range []int{40, 60, 80, 120} {
		for _, info := range []RowInfo{{Playing: true, Favorite: true}, {Tuning: "tuning in…"}} {
			l, delegate := newTestList(long, func(int) RowInfo { return info })
			l.SetSize(width, 24)

			output := render(l, delegate, 0)

			for _, line := range strings.Split(output, "\n") {
				assert.LessOrEqual(t, lipgloss.Width(line), width, "width %d: %q", width, ansi.Strip(line))
			}
			assert.Contains(t, output, "…", "width %d: overlong text is truncated", width)
		}
	}
}

func TestLineBreaks_KeepTheRuneCount(t *testing.T) {
	// Search match indexes into the raw description must still line up
	// with the flattened one the row shows.
	raw := "foo\r\nbar\nbaz\r"
	flat := lineBreaks.Replace(raw)

	assert.Equal(t, "foo  bar baz ", flat)
	assert.Equal(t, utf8.RuneCountInString(raw), utf8.RuneCountInString(flat))
}

func TestContiguous(t *testing.T) {
	assert.Equal(t, []int{2, 3, 4}, contiguous([]int{2, 3, 4}), "a run of adjacent characters is highlighted")
	assert.Equal(t, []int{7}, contiguous([]int{7}))
	assert.Nil(t, contiguous([]int{2, 9, 20}), "a scattered fuzzy match is not")
	assert.Nil(t, contiguous(nil))
}

// TestRenderHeader_ListenersAlignsWithRowCounts checks the right-aligned
// "Listeners" header ends in the same column as the counts under it, for
// every row style (selected, playing, normal) and both header titles.
func TestRenderHeader_ListenersAlignsWithRowCounts(t *testing.T) {
	// endCol returns the display column just past the last occurrence of
	// sub in the first line of s containing it.
	endCol := func(t *testing.T, s, sub string) int {
		t.Helper()
		for _, line := range strings.Split(ansi.Strip(s), "\n") {
			if i := strings.LastIndex(line, sub); i >= 0 {
				return ansi.StringWidth(line[:i+len(sub)])
			}
		}
		t.Fatalf("%q not found in %q", sub, s)
		return 0
	}

	for _, width := range []int{60, 80, 120} {
		for _, favoritesOnly := range []bool{false, true} {
			l, delegate := newTestList(testChannels(), func(i int) RowInfo { return RowInfo{Playing: i == 1} })
			l.SetSize(width, 24)
			header := endCol(t, RenderHeader(width, favoritesOnly, 3), "Listeners")

			for i, name := range []string{"selected", "playing", "normal"} {
				count := testChannels()[i].Listeners
				assert.Equal(t, header, endCol(t, render(l, delegate, i), count),
					"width %d, favoritesOnly %v: header must end where the %s row's count does", width, favoritesOnly, name)
			}
			assert.Equal(t, width-rightMargin, header, "a margin column stays blank at the right edge")
		}
	}
}

func TestRenderHeader_NamesTheViewAndCount(t *testing.T) {
	all := ansi.Strip(RenderHeader(80, false, 46))
	favorites := ansi.Strip(RenderHeader(80, true, 3))

	assert.Contains(t, all, "SomaFM")
	assert.Contains(t, all, "Stations · 46")
	assert.Contains(t, favorites, "Favorites · 3")
}

// mockListItem is a list.Item that is not our `Item` type.
type mockListItem struct{}

func (m mockListItem) FilterValue() string { return "" }

func TestItemMethods(t *testing.T) {
	ch := channels.Channel{
		Title:       "Groove Salad",
		Description: "Ambient beats",
		Listeners:   "1234",
	}
	i := Item{Channel: ch}

	assert.Equal(t, "Groove Salad", i.Title())
	assert.Equal(t, "Ambient beats", i.Description())
	assert.Equal(t, "Groove Salad", i.FilterValue())
	assert.Equal(t, "1234", i.Listeners())
}
