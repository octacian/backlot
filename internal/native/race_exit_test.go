//go:build darwin || linux

package native

import (
	"os"
	"testing"
)

// Race-built processes sleep ~1s at exit by default; subprocess children inherit
// this. Detected races still print, so only races in a final second are lost.
func TestMain(m *testing.M) {
	if _, ok := os.LookupEnv("GORACE"); !ok {
		_ = os.Setenv("GORACE", "atexit_sleep_ms=0")
	}
	os.Exit(m.Run())
}
