package identity

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/nwverse/persona/server/internal/lazyre"
)

const machineUIDFile = "machine-uid"

// machineUIDPattern matches the UUIDv4 format written by node.LoadMachineUID (~/.persona/machine-uid).
var machineUIDPattern = lazyre.New(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// ReadMachineUID reads the host's local machine UID from <dir>/machine-uid without creating or modifying it.
// Returns "" when the file does not exist, cannot be read, or does not contain a valid UUIDv4.
func ReadMachineUID(dir string) string {
	if dir == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(dir, machineUIDFile))
	if err != nil {
		return ""
	}
	uid := strings.TrimSpace(string(b))
	if !machineUIDPattern.MatchString(uid) {
		return ""
	}
	return uid
}
