# keepkit hero demo — the three panels in one pass: [space] groups the list by
# tag, [2] refreshes the card past its 24h cache, and [3] cycles its three
# sources (README, man page, --help) on the selected tool.
# Render: vhs demo/hero.tape  (outputs demo/hero.gif)
Output demo/hero.gif
Require keepkit

Set Shell "zsh"
Set FontSize 18
Set Width 1880
Set Height 960
Set Padding 16
Set Framerate 30
Set TypingSpeed 35ms

# clean, neutral prompt
Hide
Type "PS1='$ '" Enter
Type "clear" Enter
Show
Sleep 600ms

Type "keepkit" Sleep 300ms Enter
# versions, cards and the API gauge land asynchronously — let them all arrive
Sleep 7s

# [space] groups the list by tag: pending updates head it as their own section,
# then one section per tag. The selection stays on the tool it was on.
Space Sleep 3s

# walk the cursor across a section boundary and back
Type "j" Sleep 900ms
Type "j" Sleep 1.4s
Type "k" Sleep 700ms
Type "k" Sleep 1.5s

# [2] the card, [r] re-fetches release, repo card and README ignoring the cache.
# Twice, because the fetch itself is over in well under a second — what shows the
# work is the API gauge in the right corner stepping up three requests at a time.
Type "2" Sleep 1.5s
Type "r" Sleep 3.5s
Type "r" Sleep 4.5s

# [3] the README — the panel's default source
Type "3" Sleep 2s
Ctrl+D Sleep 1.6s
Ctrl+D Sleep 2s

# [M] the man page
Type "M" Sleep 5s
Ctrl+D Sleep 2.5s

# [H] the tool's own --help
Type "H" Sleep 6s

Type "q"
Sleep 1s
