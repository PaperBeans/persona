package assistant

import (
	"encoding/json"

	"github.com/nwverse/persona/server/internal/compat"
)

type launcherViewJSON LauncherView

func (v LauncherView) MarshalJSON() ([]byte, error) {
	copy := launcherViewJSON(v)
	copy.Source = compat.LegacySource(copy.Source)
	return json.Marshal(copy)
}
func (v *LauncherView) UnmarshalJSON(raw []byte) error {
	copy := launcherViewJSON(*v)
	if err := json.Unmarshal(raw, &copy); err != nil {
		return err
	}
	copy.Source = compat.Source(copy.Source)
	*v = LauncherView(copy)
	return nil
}

type launcherWithHealthJSON launcherWithHealth

func (v launcherWithHealth) MarshalJSON() ([]byte, error) {
	copy := launcherWithHealthJSON(v)
	copy.Source = compat.LegacySource(copy.Source)
	return json.Marshal(copy)
}
