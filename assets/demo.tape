Output assets/demo.gif

Set Shell "zsh"
Set Theme "Dracula"
Set FontSize 14
Set Width 1100
Set Height 650
Set TypingSpeed 50ms
Set PlaybackSpeed 0.8

Sleep 500ms
Type "export PATH=/Users/amsrahman/bin:/opt/homebrew/bin:$PATH && cd /Users/amsrahman/ocarina"
Enter
Sleep 300ms
Type "clear"
Enter
Sleep 500ms

Type "ocarina compose uvx mcp-server-time"
Sleep 300ms
Enter
Sleep 10s

Sleep 3s

Type "clear"
Enter
Sleep 500ms

Type "ocarina play examples/time-zones/meeting-planner.yaml"
Sleep 300ms
Enter
Sleep 12s

Sleep 5s
