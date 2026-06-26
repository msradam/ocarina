Output assets/demo.gif

Set Shell "zsh"
Set FontSize 13
Set Width 1100
Set Height 650
Set Padding 24
Set Framerate 24
Set PlaybackSpeed 1

Set Theme {
  "name": "Everforest Dark",
  "black": "#475258",
  "red": "#e67e80",
  "green": "#a7c080",
  "yellow": "#dbbc7f",
  "blue": "#7fbbb3",
  "purple": "#d699b6",
  "cyan": "#83c092",
  "white": "#d3c6aa",
  "brightBlack": "#859289",
  "brightRed": "#e67e80",
  "brightGreen": "#a7c080",
  "brightYellow": "#dbbc7f",
  "brightBlue": "#7fbbb3",
  "brightPurple": "#d699b6",
  "brightCyan": "#83c092",
  "brightWhite": "#d3c6aa",
  "background": "#2d353b",
  "foreground": "#d3c6aa",
  "selectionBackground": "#503946",
  "cursorColor": "#d3c6aa"
}

# setup — hidden
Hide
Type "export PATH=$HOME/bin:$PATH && export PS1='❯ ' && cd /Users/amsrahman/ocarina"
Enter
Sleep 500ms
Ctrl+L
Show

# --- scene 1: show what a cassette looks like ---
Sleep 1.5s
Type "bat --theme=gruvbox-dark --paging=never examples/time-zones.yaml"
Sleep 600ms
Enter
Sleep 5s

# let it breathe
Sleep 3s

# --- scene 2: play that cassette ---
Ctrl+L
Sleep 1s
Type "ocarina play examples/time-zones.yaml"
Sleep 600ms
Enter
Sleep 9s

# let results breathe
Sleep 3s

# --- scene 3: assertions in CI ---
Ctrl+L
Sleep 1s
Type "ocarina play examples/mcp-smoke-test.yaml"
Sleep 600ms
Enter
Sleep 7s

Sleep 3s
