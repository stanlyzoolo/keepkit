# keepkit update demo — protobuf is two releases behind: the card shows v34.0
# against v35.1, [enter] detects the package manager and asks to confirm the
# command, and the manager's output streams into panel [3] until it reports the
# outcome and the version it verified afterwards.
# Render: vhs demo/update.tape  (outputs demo/update.gif)
# Note: this actually runs `brew upgrade protobuf`.
Output demo/update.gif
Require keepkit

Set Shell "zsh"
Set FontSize 18
Set Width 1880
Set Height 960
Set Padding 16
Set Framerate 30
Set TypingSpeed 35ms

# keep brew's own chatter out of the log: no auto-update pass, no env hints
Hide
Type "export HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_ENV_HINTS=1" Enter
Type "PS1='$ '" Enter
Type "clear" Enter
Show
Sleep 600ms

Type "keepkit" Sleep 300ms Enter
Sleep 7s

# [1] find protobuf — committing the search lands straight on its card
Type "/" Sleep 400ms
Type "protobuf" Sleep 700ms
Enter Sleep 4s

# [2] installed v34.0 against v35.1 ↑ — enter detects the manager and shows the
# exact command it is about to run
Enter Sleep 4s

# confirm: `brew upgrade protobuf` starts, its output streams into [3]
Enter Sleep 1.5s
Type "3" Sleep 12s

# Skip the long middle of the download and install. Deliberately longer than the
# upgrade needs: overshooting only costs recording time, while undershooting ends
# the GIF mid-install — and the update is gone, so there is no second take.
Hide
Sleep 240s
Show

# [3] update finished — the outcome line, the manager, the elapsed time and the
# version keepkit re-detected afterwards
Sleep 7s

Type "q"
Sleep 1s
