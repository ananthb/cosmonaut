package daemon

import (
	"fmt"
	"log"
	"runtime"
	"strings"

	"golang.design/x/hotkey"

	"github.com/linuskendall/cosmonaut/internal/history"
)

// defaultHotkey returns the platform default hotkey string.
func defaultHotkey() string {
	if runtime.GOOS == "darwin" {
		return "Cmd+Shift+S"
	}
	return "Ctrl+Shift+S"
}

func (d *Daemon) startHotkeyListener() {
	hotkeyStr := defaultHotkey()
	if d.Cfg != nil && d.Cfg.Daemon != nil && d.Cfg.Daemon.Hotkey != "" {
		hotkeyStr = d.Cfg.Daemon.Hotkey
	}

	mods, key, err := parseHotkeyString(hotkeyStr)
	if err != nil {
		log.Printf("hotkey: invalid config %q: %v", hotkeyStr, err)
		return
	}

	hk := hotkey.New(mods, key)
	if err := hk.Register(); err != nil {
		log.Printf("hotkey: failed to register %q: %v", hotkeyStr, err)
		return
	}
	defer func() {
		if err := hk.Unregister(); err != nil {
			log.Printf("hotkey: unregister: %v", err)
		}
	}()

	log.Printf("hotkey: registered %s", hotkeyStr)

	for {
		select {
		case <-hk.Keydown():
			go d.hotkeyAction()
		case <-d.stopCh:
			return
		}
	}
}

// hotkeyAction determines what to do when the hotkey is pressed,
// based on the daemon.hotkeyAction config: "picker" (default),
// "previous" (most recent repo from history), or "default" (default target).
func (d *Daemon) hotkeyAction() {
	action := "picker"
	if d.Cfg != nil && d.Cfg.Daemon != nil && d.Cfg.Daemon.HotkeyAction != "" {
		action = d.Cfg.Daemon.HotkeyAction
	}

	switch action {
	case "previous":
		d.hotkeyActionPrevious()

	case "default":
		if d.Cfg != nil && d.Cfg.DefaultTarget != "" {
			if _, ok := d.Cfg.Targets[d.Cfg.DefaultTarget]; ok {
				d.showGUI(d.Cfg.DefaultTarget)
				return
			}
		}
		// No default target: fall through to picker.
		d.showGUI()

	default: // "picker"
		d.showGUI()
	}
}

// hotkeyActionPrevious launches the most recently used target from history,
// falling back to the picker if there's no history.
func (d *Daemon) hotkeyActionPrevious() {
	hist := history.Load()
	if len(hist.Entries) > 0 {
		repo := hist.Entries[len(hist.Entries)-1].Repository
		if name := d.targetNameForRepo(repo); name != "" {
			d.showGUI(name)
			return
		}
		// No config target: pass repo name directly so the child
		// process can skip the repo selector TUI.
		d.showGUI(repo)
		return
	}
	d.showGUI()
}

// parseHotkeyString converts a string like "Cmd+Shift+S" to hotkey modifiers and key.
func parseHotkeyString(s string) ([]hotkey.Modifier, hotkey.Key, error) {
	parts := strings.Split(s, "+")
	if len(parts) < 2 {
		return nil, 0, fmt.Errorf("expected modifier+key (e.g. Cmd+Shift+S)")
	}

	keyPart := strings.TrimSpace(parts[len(parts)-1])
	modParts := parts[:len(parts)-1]

	var mods []hotkey.Modifier
	for _, m := range modParts {
		mod, err := parseModifier(strings.TrimSpace(m))
		if err != nil {
			return nil, 0, err
		}
		mods = append(mods, mod)
	}

	key, err := parseKey(keyPart)
	if err != nil {
		return nil, 0, err
	}

	return mods, key, nil
}

// parseModifier is platform-specific (hotkey_modifiers_darwin.go / hotkey_modifiers_linux.go)
// because golang.design/x/hotkey defines different modifier constants per OS.

// keyMap maps lowercase key names to hotkey.Key constants.
// These constants are platform-specific virtual key codes, not ASCII.
var keyMap = map[string]hotkey.Key{
	"a": hotkey.KeyA, "b": hotkey.KeyB, "c": hotkey.KeyC, "d": hotkey.KeyD,
	"e": hotkey.KeyE, "f": hotkey.KeyF, "g": hotkey.KeyG, "h": hotkey.KeyH,
	"i": hotkey.KeyI, "j": hotkey.KeyJ, "k": hotkey.KeyK, "l": hotkey.KeyL,
	"m": hotkey.KeyM, "n": hotkey.KeyN, "o": hotkey.KeyO, "p": hotkey.KeyP,
	"q": hotkey.KeyQ, "r": hotkey.KeyR, "s": hotkey.KeyS, "t": hotkey.KeyT,
	"u": hotkey.KeyU, "v": hotkey.KeyV, "w": hotkey.KeyW, "x": hotkey.KeyX,
	"y": hotkey.KeyY, "z": hotkey.KeyZ,
	"0": hotkey.Key0, "1": hotkey.Key1, "2": hotkey.Key2, "3": hotkey.Key3,
	"4": hotkey.Key4, "5": hotkey.Key5, "6": hotkey.Key6, "7": hotkey.Key7,
	"8": hotkey.Key8, "9": hotkey.Key9,
	"space":  hotkey.KeySpace,
	"return": hotkey.KeyReturn, "enter": hotkey.KeyReturn,
	"escape": hotkey.KeyEscape, "esc": hotkey.KeyEscape,
	"tab":    hotkey.KeyTab,
	"delete": hotkey.KeyDelete, "backspace": hotkey.KeyDelete,
	"up": hotkey.KeyUp, "down": hotkey.KeyDown,
	"left": hotkey.KeyLeft, "right": hotkey.KeyRight,
	"f1": hotkey.KeyF1, "f2": hotkey.KeyF2, "f3": hotkey.KeyF3,
	"f4": hotkey.KeyF4, "f5": hotkey.KeyF5, "f6": hotkey.KeyF6,
	"f7": hotkey.KeyF7, "f8": hotkey.KeyF8, "f9": hotkey.KeyF9,
	"f10": hotkey.KeyF10, "f11": hotkey.KeyF11, "f12": hotkey.KeyF12,
}

func parseKey(s string) (hotkey.Key, error) {
	if k, ok := keyMap[strings.ToLower(s)]; ok {
		return k, nil
	}
	return 0, fmt.Errorf("unknown key %q", s)
}
