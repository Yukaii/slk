# Terminal Compatibility

slk works in any modern terminal, but the experience scales with what your
terminal supports. This table summarizes the important capabilities; pick
something from the top of the list for the richest experience.

| Terminal              | Inline images        | True-pixel avatars | OSC 8 links | OSC 52 clipboard | Notes                                                       |
|-----------------------|----------------------|--------------------|-------------|------------------|-------------------------------------------------------------|
| **kitty**             | kitty graphics       | yes                | yes         | yes              | Best overall experience. Older versions may need `clipboard_control write-clipboard`. |
| **ghostty**           | kitty graphics       | yes                | yes         | yes              | Recommended.                                                |
| **WezTerm** (recent)  | kitty graphics       | yes                | yes         | yes              |                                                             |
| **foot** (Wayland)    | sixel                | half-block         | yes         | yes              | Best Wayland-native option.                                 |
| **iTerm2 ≥ 3.5**      | half-block           | half-block         | yes         | yes              | Implements kitty graphics but not unicode placeholders, so slk falls back to half-block. |
| **Alacritty**         | half-block           | half-block         | yes (≥0.11) | yes              | Fast and reliable, but no inline images.                    |
| **gnome-terminal** (recent) | half-block     | half-block         | yes         | yes              |                                                             |
| **mlterm**            | sixel                | half-block         | partial     | partial          |                                                             |
| **xterm** (`-ti vt340`) | sixel              | half-block         | yes         | yes              | Detected at startup via DA1; plain `xterm` without the sixel-capable emulation is half-block. |
| **other sixel terminals** | sixel            | half-block         | varies      | varies           | DomTerm, toyterm, contour, WezTerm and friends are picked up by the startup DA1 probe. |
| **screen**            | half-block           | half-block         | no          | no               | No working OSC 52 path; consider switching to tmux.         |

## How the image protocol is picked

With `image_protocol = "auto"` (the default) slk checks, in order:

1. kitty graphics, when the terminal announces itself as kitty/ghostty —
   confirmed at startup with a graphics-protocol probe.
2. sixel, for terminals slk recognizes by name (foot, mlterm, WezTerm,
   iTerm2) **or** that answer the startup DA1 query (`CSI c`) with
   attribute `4`. This catches sixel terminals slk has never heard of,
   including xterm built with sixel support, DomTerm and toyterm.
3. half-block (`▀`) otherwise.

If your terminal renders images as chunky half-block mosaics but
`img2sixel` works in it, run slk with `SLK_DEBUG=1` and look for the
`sixel probe:` line in the debug log. Set `image_protocol = "sixel"`
explicitly if the terminal answers DA1 without advertising sixel.

## Inside tmux

slk forces half-block for inline images regardless of the
outer terminal — pixel-protocol pass-through inside tmux is unreliable. OSC 52
clipboard requires `set -g set-clipboard on` in your tmux config.

## Overriding the image protocol

You can override slk's image-protocol pick via the `[appearance] image_protocol`
config key (`auto` / `kitty` / `sixel` / `halfblock` / `off`). See
[[Configuration]] for details.

## Related

- [[Clipboard and OSC 52|Clipboard-and-OSC-52]] — getting copy/paste to land
- [[Tradeoffs and Non-Goals|Tradeoffs-and-Non-Goals]] — image rendering caveats (animated GIFs, unfurls, threads pane sixel)
