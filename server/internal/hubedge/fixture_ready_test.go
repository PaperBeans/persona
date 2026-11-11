//go:build unix

package hubedge

import "time"

// The HTTP accept loop starts before the control listener; a serving slot alone does not make Switch ready.
func (te *testEdge) waitControl(version string) {
	te.t.Helper()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(5 * time.Millisecond)
	defer poll.Stop()
	for {
		status, err := Status(te.control)
		if err == nil && status.Proto == Proto && status.Version == version {
			return
		}
		select {
		case <-deadline.C:
			te.t.Fatalf("control never became ready for %s; last status: %+v, error: %v", version, status, err)
		case <-poll.C:
		}
	}
}
