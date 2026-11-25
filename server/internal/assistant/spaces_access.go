package assistant

import (
	"errors"
	"net/http"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/extwire"
)

// spacesWriter accepts the user or an extension with the explicit spaces
// grant. The route's scope has already been checked by the extension manager;
// this boundary never treats an arbitrary extension role as full user access.
func spacesWriter(w http.ResponseWriter, r *http.Request) bool {
	if fullRole(r) || r.Header.Get(RoleHeader) == roleExtension && r.Header.Get(ExtensionHeader) != "" {
		return true
	}
	apierr.Write(w, http.StatusForbidden, errors.New("only the user or an extension granted "+extwire.ScopeSpaces+" changes this"))
	return false
}
