//go:build !windows

package node

// claimBusy: a rename of codex-start's file failed because another program has it open right now. Only Windows refuses
// a rename for that.
func claimBusy(error) bool { return false }
