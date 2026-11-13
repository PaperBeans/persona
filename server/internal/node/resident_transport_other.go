//go:build !unix

package node

import "errors"

const ResidentHandoverSupported = false

type ResidentFiles struct{}

func inheritedResidentTransport(string) (*ResidentTransport, bool, error) { return nil, false, nil }
func (*ResidentTransport) PrepareExec() (*ResidentFiles, error) {
	return nil, errors.New("resident listener handover requires Unix")
}
func (*ResidentFiles) Close() {}
func (*ResidentFiles) Exec(string, []string, []string) error {
	return errors.New("resident listener handover requires Unix")
}
