Output assets/demo.gif

Set Shell "zsh"
Set FontSize 14
Set Width 1200
Set Height 650
Set Padding 24
Set Framerate 24
Set TypingSpeed 80ms
Set PlaybackSpeed 0.75

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

Hide
Type "export PATH=$HOME/bin:/opt/homebrew/bin:$PATH && export PS1='$ ' && cd /Users/amsrahman/ocarina"
Enter
Sleep 300ms
Type "unsetopt PROMPT_CR PROMPT_SP 2>/dev/null; true"
Enter
Sleep 300ms
Type "bash assets/demo-setup.sh"
Enter
Sleep 2s
Ctrl+L
Show

# --- scene 1: show the rondo so the viewer reads the tool + args ---
Sleep 1s
Type "cat examples/sqlite/demo-report.yaml"
Sleep 500ms
Enter
Sleep 6s

# --- scene 2: play it ---
Ctrl+L
Sleep 1s
Type "ocarina play examples/sqlite/demo-report.yaml"
Sleep 500ms
Enter
Sleep 30s

Sleep 8s
