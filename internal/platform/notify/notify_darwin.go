//go:build darwin

package notify

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// notifyTimeout bounds the osascript call: a hung or blocked notification
// daemon must not leave the notifyPipeline goroutine (see
// internal/server/notify.go) stuck forever.
const notifyTimeout = 5 * time.Second

// Notifier shows desktop notifications via `osascript -e 'display
// notification ...'`. macOS's Notification Center has no D-Bus-style
// replace-by-id primitive, so unlike the Linux Notifier every call shows a
// fresh notification.
type Notifier struct {
	mu     sync.Mutex
	logged bool
}

// New returns a Notifier.
func New() *Notifier {
	return &Notifier{}
}

// Notify shows title as the notification's heading and body as its body.
// Failures (osascript missing, Notification Center permission denied, ...)
// are logged once and otherwise swallowed: a desktop notification is a
// nice-to-have, never worth taking the daemon down over.
func (n *Notifier) Notify(title, body string) {
	script := fmt.Sprintf("display notification %s with title %s",
		appleScriptQuote(body), appleScriptQuote(title))
	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()
	// #nosec G204 -- script is a fixed AppleScript template; title/body are
	// embedded as escaped string literals (appleScriptQuote), not shell
	// syntax, and osascript is invoked directly, without a shell, so there
	// is no injection vector through either argument.
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	if err := cmd.Run(); err != nil {
		n.mu.Lock()
		defer n.mu.Unlock()
		if !n.logged {
			n.logged = true
			log.Printf("desktop notification failed (further failures are not logged): %v", err)
		}
	}
}

// appleScriptQuote renders s as a double-quoted AppleScript string literal.
// It escapes backslashes and double quotes so untrusted stream metadata (an
// ICY artist or title can contain anything) cannot break out of the literal
// and inject further AppleScript, and flattens newlines to spaces since an
// AppleScript string literal cannot contain one unescaped.
func appleScriptQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return `"` + s + `"`
}
