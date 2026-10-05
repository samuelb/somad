# VHS script for demo.gif (https://github.com/charmbracelet/vhs).
# Record with `make demo`: it builds ./soma, points every soma directory at
# dist/demo so the recording never touches real favorites or a running
# daemon, and plays real audio for about half a minute.
#
# The tour: tune in to a channel from the list, filter to another and play
# it, mark it a favorite, peek at its history, clear the filter, then end
# on the spectrum visualizer behind the full list.

Output demo.gif

Set Shell "bash"
Set FontSize 14
Set Width 880
Set Height 618
Set Padding 16
Set Theme "Dark+"
Set TypingSpeed 60ms
# 20 frames a second keeps the moving bars from bloating the file.
Set Framerate 20

Type "soma"
Sleep 600ms
Enter
Sleep 2500ms

# Pick a channel from the list: it shows "tuning in…" until it plays.
Down
Sleep 400ms
Down
Sleep 400ms
Down
Sleep 600ms
Enter
Sleep 5s

# Filter to Groove Salad and play it.
Type "/"
Sleep 600ms
Type "groove"
Sleep 900ms
Enter
Sleep 700ms
Enter
Sleep 4s

# Favorite it, and look at what played on it recently.
Type "f"
Sleep 1200ms
Type "h"
Sleep 3500ms
Type "h"
Sleep 700ms

# Back to the full list, favorites first.
Escape
Sleep 1500ms

# The spectrum of the music, behind the list.
Type "v"
Sleep 7s

Type "q"
Sleep 1s
