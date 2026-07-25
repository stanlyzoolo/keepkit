# keepkit update demo — in-TUI update of an outdated tool (ttl), streaming log.
# Render: vhs demo/update.tape  (outputs demo/update.gif)
# Note: this actually runs `brew upgrade ttl`.
Output demo/update.gif
Require keepkit

Set Shell "zsh"
Set FontSize 16
Set Width 1200
Set Height 720
Set Padding 16
Set TypingSpeed 55ms

Hide
Type "PS1='$ '" Enter
Type "clear" Enter
Show
Sleep 800ms

Type "keepkit" Sleep 400ms Enter
Sleep 6s

# ttl is grouped at the top with the ↑ marker; jump to it and open its card [2]
Type "g" Sleep 1500ms
Type "2" Sleep 2800ms

# [u] detects the package manager and shows the command for confirmation
Type "u" Sleep 3000ms

# run it — output streams into panel [3] Update in real time
Enter Sleep 24s

# card title reverts, ↑ is gone; leave it on screen a moment
Sleep 3s
Type "q"
Sleep 1200ms
