package util

import (
	"log"
	"runtime"
)

// GoSafe runs fn in a new goroutine, recovering from any panic so that
// fire-and-forget work cannot crash the process. Recovered panics are logged
// via the standard logger together with a short stack snippet.
func GoSafe(fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				buf := make([]byte, 4096)
				n := runtime.Stack(buf, false)
				log.Printf("GoSafe: recovered panic: %v\n%s", r, buf[:n])
			}
		}()
		fn()
	}()
}
