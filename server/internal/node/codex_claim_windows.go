package node

import (
	"errors"

	"golang.org/x/sys/windows"
)

// claimBusy: a rename of codex-start's file failed because another program has it open right now (an antivirus scanner,
// a search indexer): worth trying again, unlike a file that is gone.
func claimBusy(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
