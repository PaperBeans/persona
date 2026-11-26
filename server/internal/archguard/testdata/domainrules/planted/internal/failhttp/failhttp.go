// Package failhttp writes a domain failure as an HTTP answer: invalid input 400 with the part that was wrong, a
// refusal or a conflict 409, something missing 404.
//
//persona:layer adapter
package failhttp

import (
	"encoding/json"
	"net/http"

	"github.com/nwverse/persona/server/internal/domainkit/fail"
)

// Write answers w with f.
func Write(w http.ResponseWriter, f fail.Fail) {
	var a answer
	f.Accept(&a)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(a.status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": a.code, "part": a.part})
}

// answer is a failure's status, code and part.
type answer struct {
	status     int
	code, part string
}

func (a *answer) VisitInvalid(code fail.Code, part fail.Part) {
	a.status, a.code, a.part = http.StatusBadRequest, code.Wire(), part.Wire()
}
func (a *answer) VisitRefused(code fail.Code)  { a.status, a.code = http.StatusConflict, code.Wire() }
func (a *answer) VisitMissing(code fail.Code)  { a.status, a.code = http.StatusNotFound, code.Wire() }
func (a *answer) VisitConflict(code fail.Code) { a.status, a.code = http.StatusConflict, code.Wire() }
