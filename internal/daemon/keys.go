package daemon

import (
	"strings"
	"unicode/utf8"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

	"github.com/Facets-cloud/bx/internal/exit"
)

// keyNames maps a DOM key name ("Enter", "ArrowDown", "F5") to the rune
// that chromedp's kb package uses for it.
var keyNames = func() map[string]rune {
	m := map[string]rune{"Space": ' '}
	for r, k := range kb.Keys {
		if k.Key != "" && utf8.RuneCountInString(k.Key) > 1 {
			m[k.Key] = r
		}
	}
	return m
}()

var modifierNames = map[string]chromedp.Modifier{
	"control": chromedp.ModifierCtrl, "ctrl": chromedp.ModifierCtrl,
	"shift": chromedp.ModifierShift,
	"alt":   chromedp.ModifierAlt, "option": chromedp.ModifierAlt,
	"meta": chromedp.ModifierMeta, "cmd": chromedp.ModifierMeta, "command": chromedp.ModifierMeta,
}

// keyCombo is a parsed key such as "Control+Shift+R".
type keyCombo struct {
	key  rune
	mods []chromedp.Modifier
}

// parseCombo reads "Enter", "a", "Control+A" or "Meta+Shift+ArrowLeft".
// Modifier names are case-insensitive; key names are as in the DOM
// (KeyboardEvent.key), or a single character.
func parseCombo(s string) (keyCombo, error) {
	parts := strings.Split(s, "+")
	// "Control++" means Control and the "+" key.
	if strings.HasSuffix(s, "++") {
		parts = append(parts[:len(parts)-2], "+")
	}
	var c keyCombo
	for i, p := range parts {
		if i < len(parts)-1 {
			m, ok := modifierNames[strings.ToLower(p)]
			if !ok {
				return c, exit.New(exit.Validation, "unknown modifier %q in %q (use Control, Shift, Alt or Meta)", p, s)
			}
			c.mods = append(c.mods, m)
			continue
		}
		switch {
		case utf8.RuneCountInString(p) == 1:
			c.key, _ = utf8.DecodeRuneInString(p)
		case keyNames[p] != 0:
			c.key = keyNames[p]
		default:
			return c, exit.New(exit.Validation, "unknown key %q (use a DOM key name such as Enter, Tab, Escape, ArrowDown, F5, or one character)", p)
		}
	}
	return c, nil
}

// macCommands are the editing commands macOS binds to Meta shortcuts.
// Chromium runs them for synthetic key events only when the event names
// the command, so Meta+A selects all only with "selectAll" attached.
var macCommands = map[string]string{
	"a": "selectAll", "c": "copy", "x": "cut", "v": "paste", "z": "undo", "Z": "redo",
}

// commands returns the editing commands for this combo, if any.
func (c keyCombo) commands() []string {
	if len(c.mods) == 0 || c.mods[0] != chromedp.ModifierMeta {
		return nil
	}
	k := strings.ToLower(string(c.key))
	shift := false
	for _, m := range c.mods[1:] {
		if m != chromedp.ModifierShift {
			return nil
		}
		shift = true
	}
	if shift && k == "z" {
		k = "Z"
	} else if shift {
		return nil
	}
	if cmd, ok := macCommands[k]; ok {
		return []string{cmd}
	}
	return nil
}

func (c keyCombo) action() chromedp.Action[chromedp.Void] {
	cmds := c.commands()
	return chromedp.KeyEvent(string(c.key), chromedp.KeyModifiers(c.mods...), func(p *input.DispatchKeyEventParams) {
		if p.Type == input.DispatchKeyEventTypeKeyDown || p.Type == input.DispatchKeyEventTypeRawKeyDown {
			p.Commands = cmds
		}
	})
}
