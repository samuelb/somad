//go:build linux

package tray

import "strings"

// menuLabel returns text as the label of a menu item that shows it as
// written. The Linux menu is a dbusmenu, where an underscore in a label
// marks the character after it as the item's access key and is not shown
// itself; two stand for one underscore. Track and channel names are not
// written with that in mind: "sub_mariner" showed as "submariner".
func menuLabel(text string) string {
	return strings.ReplaceAll(text, "_", "__")
}
