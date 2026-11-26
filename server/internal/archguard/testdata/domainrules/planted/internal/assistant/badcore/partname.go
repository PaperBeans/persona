package badcore

import "github.com/nwverse/persona/server/internal/domainkit/fail"

func noMachine() Fail {
	return invalid{fail.NewInvalid(fail.NewCode("badcore.machine.empty"), fail.NewPart("machine_id"))} // core-part-name
}
