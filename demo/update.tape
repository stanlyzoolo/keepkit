# keepkit update demo — agterm has a newer release: the card shows installed vs
# latest, [u] detects the manager, the log streams into [3], refresh confirms.
# Render: vhs demo/update.tape  (outputs demo/update.gif)
# Note: this actually runs `brew upgrade agterm`.
Output demo/update.gif
Require keepkit

Set Shell "zsh"
Set FontSize 18
Set Width 1600
Set Height 960
Set Padding 16
Set Framerate 30
Set TypingSpeed 35ms

Hide
Type "unset TMUX TERM_PROGRAM KITTY_LISTEN_ON" Enter
Type "export HOMEBREW_NO_AUTO_UPDATE=1" Enter
Type "PS1='$ '" Enter
Type "clear" Enter
Show
Sleep 600ms

Type "keepkit" Sleep 300ms Enter
Sleep 3s

# find agterm: committing the search lands straight on its card
Type "/" Sleep 300ms
Type "agterm" Sleep 300ms
Enter Sleep 3s

# the card: installed 0.15.1, latest v0.16.1 with the ↑ marker
Type "u" Sleep 2.5s

# [u] detected the manager — run the shown command; the log streams into [3]
Enter Sleep 5s

# skip the long middle of the download, come back for the finished log
Hide
Sleep 150s
Show
Sleep 3s

# refresh the card: installed == latest now, the ↑ is gone
Type "r" Sleep 3s

Type "q"
Sleep 1s
