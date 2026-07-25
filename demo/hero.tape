# keepkit hero demo — track a repo, tag it, group by tag, run the tool from the
# list, then refresh the card while the GitHub API gauge counts the requests.
# Render: vhs demo/hero.tape  (outputs demo/hero.gif)
Output demo/hero.gif
Require keepkit

Set Shell "zsh"
Set FontSize 18
Set Width 1600
Set Height 960
Set Padding 16
Set Framerate 30
Set TypingSpeed 35ms

# clean, neutral prompt; force the in-window launch path regardless of the
# terminal vhs was started from
Hide
Type "unset TMUX TERM_PROGRAM KITTY_LISTEN_ON" Enter
Type "PS1='$ '" Enter
Type "clear" Enter
Show
Sleep 600ms

Type "keepkit" Sleep 300ms Enter
Sleep 3s

# track a new tool by GitHub URL
Type "t" Sleep 300ms
Type "https://github.com/Epistates/treemd" Sleep 300ms
Enter Sleep 2.5s

# tag it: [2] focuses the card, [t] edits the tag
Type "2" Sleep 300ms
Type "t" Sleep 300ms
Type "viewers" Sleep 300ms
Enter Sleep 1s

# back to the list, [space] groups by tag — treemd sits under its new header
Type "1" Sleep 300ms
Space Sleep 2s
Type "k" Sleep 300ms
Type "j" Sleep 1s

# run it right from keepkit: the prompt is prefilled with the tool name
Enter Sleep 300ms
Type " README.md" Sleep 300ms
Enter Sleep 3.5s

# a couple of moves inside treemd, then quit — keepkit resumes
Type "j" Sleep 300ms
Type "j" Sleep 1.2s
Type "q" Sleep 1.5s

# refresh the card a few times — the GitHub API gauge in the corner counts up
Type "2" Sleep 300ms
Type "r" Sleep 1.5s
Type "r" Sleep 1.5s
Type "r" Sleep 3s

Type "q"
Sleep 1s
