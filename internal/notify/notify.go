// Package notify shows a desktop notification; failures are only logged.
package notify

import (
	"github.com/gen2brain/beeep"

	"dpi/internal/logx"
)

// Send blocks until the notification is handed to the OS; call it with `go` when that matters.
func Send(title, msg string) {
	if err := beeep.Notify(title, msg, ""); err != nil {
		logx.Debug.Printf("notification failed: %v", err)
	}
}
