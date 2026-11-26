package supervisionrole

import "github.com/nwverse/persona/server/internal/domainkit/fail"

// Fail is every failure supervisionrole returns. The zero Fail is nil; no operation returns it as a failure.
type Fail interface {
	fail.Fail
	supervisionroleFail()
}
