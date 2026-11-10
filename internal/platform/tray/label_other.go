//go:build !linux

package tray

// menuLabel returns text as the label of a menu item that shows it as
// written: text itself, here. Only the Linux menu reads anything into a
// label (see label_linux.go); a macOS menu item's title is plain text.
func menuLabel(text string) string {
	return text
}
