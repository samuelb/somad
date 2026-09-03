# VHS script for demo.gif (https://github.com/charmbracelet/vhs).
# Record with `make demo`: it builds ./soma, points every soma directory at
# dist/demo so the recording never touches real favorites or a running
# daemon, and plays real audio for a few seconds.

Output demo.gif

Set Shell "bash"
Set FontSize 14
Set Width 880
Set Height 618
Set Padding 16
Set Theme "Catppuccin Mocha"
Set TypingSpeed 60ms

Type "soma"
Sleep 600ms
Enter
Sleep 2500ms

Down
Sleep 500ms
Down
Sleep 500ms
Down
Sleep 700ms
Enter
Sleep 6s

Type "/"
Sleep 600ms
Type "groove"
Sleep 900ms
Enter
Sleep 800ms
Enter
Sleep 5s

Type "f"
Sleep 2s

Type "q"
Sleep 1500ms
