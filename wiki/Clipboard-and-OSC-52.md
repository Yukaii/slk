# Clipboard and OSC 52

slk writes the system clipboard through Bubble Tea's OSC 52 terminal command;
copying does not call the native clipboard library or require CGO. Most modern
terminal emulators (alacritty, kitty, wezterm, foot, iterm2, recent
gnome-terminal) accept these writes by default. A few need explicit opt-in.

## Terminal-specific setup

- **tmux:** `set -g set-clipboard on` in your tmux config.
- **screen:** has no working OSC 52 path; consider switching to tmux.
- **kitty (older versions):** `clipboard_control write-clipboard` in
  `kitty.conf`.

## Diagnosing silent failures

If `Copied N chars` shows in the status bar but nothing lands in your
clipboard, your terminal is silently dropping the OSC 52 write. There is no
reliable round-trip to detect this from inside slk — the protocol doesn't
acknowledge writes. Check your terminal's clipboard documentation for an
opt-in setting.

Clipboard reads are separate: `Ctrl+V` smart-paste still uses the native
clipboard library on X11/macOS/Windows and `wl-paste` on Wayland. A native
reader initialization failure disables paste, but no longer disables OSC 52
copying.

## Related

- [[Terminal Compatibility|Terminal-Compatibility]] — per-terminal OSC 52 support summary
