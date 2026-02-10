# Continuation Prompt for Claude Code

Please continue the conversation from where we left it off without asking the user any further questions. Continue with the tasks described below.

## Current State (ARM64 Diplomat + Kmazarin)

The system boots and runs on ARM64 with VirtIO GPU, VirtIO block, and VirtIO input devices. Both `dapope.elf` (desktop compositor with clock, cursor, keyboard) and `stdio.elf` (console window) launch successfully. The clock shows the correct time (PL031 RTC working), the mouse cursor tracks and responds to clicks, and the keyboard delivers raw evdev events.

**What works:**
- Clock displays current host time (PL031 RTC via synthetic DTB)
- Mouse cursor rendering, movement, and click detection
- MSI-X interrupts for both keyboard and mouse (BAR collision fix applied)
- Raw keyboard events are delivered to `dapope`'s `keyboardLoop()`

**What's missing:**
- Keyboard events are printed as raw key names: pressing `a` prints `A`, pressing shift+`a` also prints `A`
- No modifier tracking (Shift, Ctrl, Alt, CapsLock)
- No character translation (evdev keycode → printable character)
- ENTER prints a newline and SPACE prints a space, but everything else is just the key name

---

## Task: Implement Keyboard Mapping (evdev → characters)

### Goal

Add a keymap system so that raw evdev keycodes are translated into actual characters. Pressing `a` should produce `a`, Shift+`a` should produce `A`, Shift+`1` should produce `!`, etc. This is essential before dapope or stdio can do any real text input.

### Architecture Recommendation

Create a new `Keymap` type in `mazarin/input/keymap.go`. This is the right location because:
- `mazarin/input/` already owns `KeyEvent` and the keyboard event pipeline
- Both `dapope` and `stdio` (and any future priest) can use it
- It keeps dapope's `main.go` simple — just consume translated events

### Design

```go
// mazarin/input/keymap.go

// Keymap translates raw evdev keycodes into characters, tracking modifier state.
type Keymap struct {
    shift    bool  // Left or Right Shift held
    ctrl     bool  // Left or Right Ctrl held
    alt      bool  // Left or Right Alt held
    capsLock bool  // CapsLock toggled on
}

// Feed processes a raw KeyEvent, updates modifier state, and returns:
//   - ch: the translated character (0 if non-printable or modifier-only)
//   - action: a named action for non-character keys ("enter", "backspace", "tab",
//             "escape", "up", "down", "left", "right", "home", "end", "delete",
//             "pageup", "pagedown", "f1".."f12", or "" if ch is set)
// Only press and repeat events produce output; releases return (0, "").
func (k *Keymap) Feed(ev KeyEvent) (ch rune, action string)
```

### Evdev Keycode Tables (US QWERTY)

Two lookup tables, indexed by evdev keycode (0-127):

**Normal (no shift):**
| Code | Char | Code | Char | Code | Char |
|------|------|------|------|------|------|
| 2-11 | `1234567890` | 12 | `-` | 13 | `=` |
| 16-25 | `qwertyuiop` | 26 | `[` | 27 | `]` |
| 30-38 | `asdfghjkl` | 39 | `;` | 40 | `'` |
| 41 | `` ` `` | 43 | `\` | 44-50 | `zxcvbnm` |
| 51 | `,` | 52 | `.` | 53 | `/` | 57 | ` ` |

**Shifted:**
| Code | Char | Code | Char | Code | Char |
|------|------|------|------|------|------|
| 2-11 | `!@#$%^&*()` | 12 | `_` | 13 | `+` |
| 16-25 | `QWERTYUIOP` | 26 | `{` | 27 | `}` |
| 30-38 | `ASDFGHJKL` | 39 | `:` | 40 | `"` |
| 41 | `~` | 43 | `\|` | 44-50 | `ZXCVBNM` |
| 51 | `<` | 52 | `>` | 53 | `?` |

**CapsLock** only affects letters (codes 16-25, 30-38, 44-50): if CapsLock is on, letters are uppercased (Shift+CapsLock = lowercase, like real keyboards).

**Modifier keys** (update state on press/release, never produce a character):
- 29 = LCTRL, 97 = RCTRL
- 42 = LSHIFT, 54 = RSHIFT
- 56 = LALT, 100 = RALT
- 58 = CAPSLOCK (toggle on press only, not release)

**Action keys** (produce named actions, not characters):
- 1 = escape, 14 = backspace, 15 = tab, 28 = enter, 96 = kpenter
- 103 = up, 108 = down, 105 = left, 106 = right
- 102 = home, 107 = end, 104 = pageup, 109 = pagedown
- 110 = insert, 111 = delete
- 59-68 = f1-f10, 87 = f11, 88 = f12

### Integration with KeyEvent

Optionally, extend `KeyEvent` in `mazarin/input/input.go` to carry the translated character:

```go
type KeyEvent struct {
    Key     string // Human-readable key name ("A", "ENTER", etc.)
    Code    uint16 // Raw evdev keycode
    Pressed bool   // true = press/repeat, false = release
    Repeat  bool   // true = auto-repeat
    Char    rune   // Translated character (0 if non-printable) — NEW
    Action  string // Named action ("enter", "backspace", etc.) — NEW
}
```

Then in `keyboardLoop()`, run the keymap before sending to the channel:

```go
var km Keymap
// ... in loop:
ch, action := km.Feed(ke)
ke.Char = ch
ke.Action = action
```

This way, consumers of the `Keyboard()` channel get pre-translated events.

### Changes to dapope's keyboardLoop

`flock/cmd/dapope/main.go:keyboardLoop()` (line 80) should change from:

```go
// Current: prints raw key names
if ev.Type == EV_KEY && ev.Value == 1 {
    switch ev.Code {
    case 28: fmt.Println()
    case 57: fmt.Print(" ")
    default: fmt.Print(keyName(ev.Code))
    }
}
```

To something like:

```go
// New: use Keymap for character translation
var km input.Keymap
// ... in loop:
for i := 0; i < n; i++ {
    ev := buf.Events[i]
    if ev.Type != EV_KEY {
        continue
    }
    ke := input.KeyEvent{Code: ev.Code, Pressed: ev.Value != 0, Repeat: ev.Value == 2}
    ch, action := km.Feed(ke)
    if !ke.Pressed && !ke.Repeat {
        continue // skip releases
    }
    if ch != 0 {
        fmt.Print(string(ch))
    } else if action == "enter" {
        fmt.Println()
    } else if action == "backspace" {
        fmt.Print("\b \b") // visual backspace
    }
}
```

### Testing

After implementing, build and run:
```bash
export GOTOOLCHAIN=auto GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
$GO tool task run-diplomat-arm64 TIMEOUT=120
```

Verify:
- Pressing `a` prints `a` (lowercase)
- Pressing Shift+`a` prints `A` (uppercase)
- Pressing `1` prints `1`, Shift+`1` prints `!`
- CapsLock toggles uppercase for letters
- ENTER produces newline, SPACE produces space, BACKSPACE erases
- Ctrl, Alt, function keys don't produce stray characters

### Stretch goals (not required now)
- Ctrl+C could eventually send a signal/kill to the active priest
- Keyboard input routed into stdio for text entry (currently stdio reads from serial UART)
- Multiple keymap layouts (AZERTY, QWERTZ, Dvorak)

---

## Key Files Reference

| File | Purpose |
|------|---------|
| `mazarin/input/input.go` | `KeyEvent`, `Keyboard()` channel API, `keyboardLoop()` |
| `mazarin/input/keymap.go` | **NEW** — `Keymap` type, US QWERTY tables, `Feed()` method |
| `flock/cmd/dapope/main.go` | `keyboardLoop()` at line 80 — update to use `Keymap` |
| `shared/hid/hid.go` | `SoftIRQReturn` event struct definition |

## Build Environment

```bash
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
```

Serial log safety: NEVER read `/tmp/diplomat-arm64-serial.log` directly — use `$GO tool safe-serial-read`.
