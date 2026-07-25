# keepkit hero demo — overview: list, card, README + help nav, search, hotkeys overlay.
# Render: vhs demo/hero.tape  (outputs demo/hero.gif)
Output demo/hero.gif
Require keepkit

Set Shell "zsh"
Set FontSize 16
Set Width 1200
Set Height 720
Set Padding 16
Set TypingSpeed 55ms

# clean, neutral prompt for the intro line
Hide
Type "PS1='$ '" Enter
Type "clear" Enter
Show
Sleep 800ms

Type "keepkit" Sleep 400ms Enter
Sleep 4s

# browse the tracker list
Type "j" Sleep 850ms
Type "j" Sleep 850ms
Type "j" Sleep 850ms
Type "j" Sleep 1100ms
Type "k" Sleep 1100ms

# focus the brief card [2]
Type "2" Sleep 2600ms

# focus the docs panel [3]: it opens on the rendered README, scroll it a bit
Type "3" Sleep 2000ms
Type "j" Sleep 700ms
Type "j" Sleep 1400ms

# switch to --help with [h] and walk entries with j/k (the entry spotlight)
Type "h" Sleep 1500ms
Type "j" Sleep 700ms
Type "j" Sleep 700ms
Type "j" Sleep 1400ms

# back to tools [1], live search narrows the list by name
Type "1" Sleep 800ms
Type "/" Sleep 700ms
Type "g" Sleep 900ms
Type "l" Sleep 2400ms
Escape Sleep 1200ms

# hotkeys overlay [?]
Type "?" Sleep 3500ms
Escape Sleep 800ms

Type "q"
Sleep 1200ms
