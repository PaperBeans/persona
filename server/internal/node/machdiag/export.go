package machdiag

import "context"

// What the disk usage map (../diskmap, docs/server/machine-usage-map.md) reads of the catalog: which folders are
// caches it can offer the existing cleanup for, and whether their toolchains run.

// CacheFolders are the catalog's caches on goos for home, by id: each a folder inside the home directory. One a
// system does not have, or a variable moved out of home, is left out.
func CacheFolders(goos, home string, getenv func(string) string) map[string]string {
	out := map[string]string{}
	for _, c := range resolveCaches(goos, home, getenv) {
		out[c.spec.id] = c.path
	}
	return out
}

// CachesInUse says, for every catalog cache, whether a process of its toolchain runs now on this computer. When the
// processes cannot be read every cache counts as in use, as the diagnosis says.
func CachesInUse(ctx context.Context) map[string]bool {
	s := host()
	procs, err := s.processes(ctx)
	running := processNames(procs, err)
	out := make(map[string]bool, len(catalog))
	for _, spec := range catalog {
		out[spec.id] = inUse(spec, running)
	}
	return out
}
