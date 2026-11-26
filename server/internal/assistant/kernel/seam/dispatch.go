package seam

import (
	"errors"
	"net/http"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/domainkit/defect"
)

// dispatch serves a registered route under the kernel's catch site, the one place a defect raised by a module's
// route is recovered (server/internal/archguard/testdata/catch_sites.txt). A defect is reported to Defects and
// answered with 500 ("internal"), so the request fails alone and the manager goes on; any other panic,
// http.ErrAbortHandler included, goes on to net/http as it would without the kernel.
func (r *Registry) dispatch(serve http.HandlerFunc) http.HandlerFunc {
	defects := r.defects
	return func(w http.ResponseWriter, req *http.Request) {
		defer defect.Catch(func(d defect.Defect) {
			defects.Report(req, d)
			apierr.Write(w, http.StatusInternalServerError, errors.New(http.StatusText(http.StatusInternalServerError)))
		})
		serve(w, req)
	}
}
