package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantskills"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/rpc"
)

type skillImportTarget struct {
	machine, name, dir, scope, environment string
}

func (s *Service) skillImportTarget(ctx context.Context) (skillImportTarget, error) {
	machine := s.settings(ctx).MachineID
	d, ok := s.device(ctx, machine)
	if !ok || machine == "" || d.Home == "" || s.isWorkMachine(machine) {
		return skillImportTarget{}, s.missingAssistantMachine(ctx)
	}
	id := s.id
	if id == "" {
		id = assistantwire.AssistantID
	}
	scope := id + "\x00" + machine
	sum := sha256.Sum256([]byte(scope + "\x00" + s.assistantCwd(d)))
	return skillImportTarget{machine, d.Name, s.assistantRel(), scope, hex.EncodeToString(sum[:])}, nil
}

func (s *Service) skillImportRoutes() {
	s.handle("POST /api/v1/skills/import", s.importSkill)
}

func (s *Service) importSkill(w http.ResponseWriter, r *http.Request) {
	if !fullRole(r) {
		apierr.Write(w, http.StatusForbidden, errors.New(http.StatusText(http.StatusForbidden)))
		return
	}
	before, err := s.skillImportTarget(r.Context())
	if err != nil {
		apierr.Call(w, err)
		return
	}
	var in struct {
		assistantskills.ImportRequest
		Environment string `json:"environment,omitempty"`
	}
	// Base64 plus JSON framing fits below 6 MiB. Unknown fields reject browser-selected machines or paths.
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 6<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		status := http.StatusBadRequest
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			status = http.StatusRequestEntityTooLarge
		}
		apierr.WriteCode(w, status, "skill_import_request", errors.New("skill_import_request"))
		return
	}
	if decoder.Decode(new(any)) != io.EOF || len(in.Archive) > assistantskills.MaxImportZIP ||
		(in.Op != "preview" && in.Op != "install" && in.Op != "cancel" && in.Op != "undo") {
		apierr.WriteCode(w, http.StatusBadRequest, "skill_import_request", errors.New("skill_import_request"))
		return
	}
	// Settings writes cannot retarget this installation between authorization and the node's commit.
	s.prefs.change.Lock()
	defer s.prefs.change.Unlock()
	target, err := s.skillImportTarget(r.Context())
	if err != nil {
		apierr.Call(w, err)
		return
	}
	if target != before || (in.Op != "preview" && in.Environment != target.environment) {
		apierr.WriteCode(w, http.StatusConflict, "skill_environment_changed", errors.New("skill_environment_changed"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var out assistantskills.ImportResult
	params := map[string]any{"dir": target.dir, "scope": target.scope, "op": in.Op, "archive": in.Archive, "reference": in.Reference}
	if err := s.call(ctx, target.machine, "skills.import", params, &out); err != nil {
		var coded *rpc.Error
		isRPC := errors.As(err, &coded)
		if err.Error() == "unknown method skills.import" || (isRPC && (coded.Code == "unknown_method" || coded.Message == "unknown method skills.import")) {
			apierr.WriteCode(w, http.StatusConflict, "node_outdated", errors.New("node_outdated"))
		} else if isRPC && strings.HasPrefix(coded.Code, "skill_") {
			status := http.StatusUnprocessableEntity
			switch coded.Code {
			case "skill_conflict", "skill_import_expired", "skill_import_changed", "skill_environment_changed", "skill_undo_changed":
				status = http.StatusConflict
			case "skill_import_busy":
				status = http.StatusTooManyRequests
			case "skill_import_io":
				status = http.StatusInternalServerError
			case "skill_import_request":
				status = http.StatusBadRequest
			}
			apierr.WriteCode(w, status, coded.Code, errors.New(coded.Code))
		} else if errors.Is(err, apierr.ErrOffline) || (errors.As(err, &coded) && coded.Code != "") {
			apierr.Call(w, err)
		} else {
			// Keep transport/offline classification without leaking an arbitrary node error in this upload flow.
			apierr.Call(w, &rpc.Error{Code: apierr.NodeCode(err), Message: apierr.NodeCode(err)})
		}
		return
	}
	apierr.WriteJSON(w, struct {
		assistantskills.ImportResult
		Environment string `json:"environment"`
		Machine     string `json:"machine"`
	}{out, target.environment, target.name})
}
