package kb

// The read path of recording_title.go: Core, with or without the recording module (docs/plans/plan-kb-recording-port.md,
// section 1; docs/architecture/core-and-modules.md "Recording").

import (
	"encoding/json"
)

// metaWithNameSource is a recording's meta.json (detail.meta) with name_source, where its name comes from (#8873);
// as it is when it is named only by when it started.
func metaWithNameSource(raw []byte) []byte {
	var r Recording
	if json.Unmarshal(raw, &r) != nil {
		return raw
	}
	src := r.NameSource()
	var meta map[string]json.RawMessage
	if src == "" || json.Unmarshal(raw, &meta) != nil || meta == nil {
		return raw
	}
	meta["name_source"], _ = json.Marshal(src)
	out, err := json.Marshal(meta)
	if err != nil {
		return raw
	}
	return out
}
