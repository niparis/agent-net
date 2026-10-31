package tea

// Bubble Tea's upstream init queried the terminal background for every
// command in a binary that imports Bubble Tea, including non-TUI commands.
// The app performs that query in tui.Run, immediately before Bubble Tea takes
// control of the terminal.
