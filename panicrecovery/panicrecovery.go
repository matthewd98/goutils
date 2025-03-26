package panicrecovery

import (
	"fmt"
	"os"
	"runtime/debug"

	"go.uber.org/zap"
)

// Always defer this function at the very beginning of each new go-routine.
// Defer it directly, it cannot be called from another deferred function, because
// recover() below will stop working.
func RecoverAndLog() {
	if err := recover(); err != nil {
		log(err)
	}
}

// Similar to the above, but also can execute a function to do special cleanup when there is a panic.
func RecoverAndLogWithCleanup(cleanup func()) {
	if err := recover(); err != nil {
		log(err)
		cleanup()
	}
}

func log(err interface{}) {
	stack := debug.Stack()
	fmt.Fprintf(os.Stderr, "panic: %v\n%s", err, stack)
	// Note: replace with your own logger package
	zap.L().Error("Recovered from panic.", zap.ByteString("stackTrace", stack), zap.Any("panic", err))
}
