package core

import (
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// dynamic paths are destroyed only after an idle grace period; keep it short
	// in tests so instances created on demand do not outlive the scenario
	prevIdleCloseAfter := idleCloseAfter
	idleCloseAfter = 100 * time.Millisecond

	code := m.Run()

	idleCloseAfter = prevIdleCloseAfter
	os.Exit(code)
}
