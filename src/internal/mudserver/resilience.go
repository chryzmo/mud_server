package mudserver

import (
	"log"
	"runtime/debug"
)

// recoverPanic must be deferred directly. A player may only be supplied on the
// game loop; connection goroutines must not read player state after admission.
// Don't log raw commands or parameters: login input can contain credentials.
// Recovery keeps the loop alive but cannot roll back partially changed state.
func recoverPanic(scope string, player *MudPlayer) {
	if value := recover(); value != nil {
		log.Printf("panic in %s: %v\n%s", scope, value, debug.Stack())
		if player != nil {
			player.SendError(codeInternal, "Something went wrong. Please try again.")
		}
	}
}

// runEvent isolates each queued event and each tick/save invocation. Recovering
// around run itself would end the loop instead of moving on to the next event.
func runEvent(scope string, event func()) {
	defer recoverPanic(scope, nil)
	event()
}
