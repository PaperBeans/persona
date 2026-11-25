package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ProbeStrict tests real namespaces/mounts and independent memory/CPU delegation,
// including cgroup v1. A working systemd bus or accepted unit properties is not proof.
var strictProbes sync.Map // executable -> once-only capability probe; starts verify their fresh scope again

type strictProbeResult struct {
	once sync.Once
	err  error
}

func ProbeStrict(self string) error {
	value, _ := strictProbes.LoadOrStore(self, &strictProbeResult{})
	result := value.(*strictProbeResult)
	result.once.Do(func() { result.err = probeStrict(self) })
	return result.err
}

// ResourceUnavailable distinguishes missing independent resource controls from
// missing namespaces. Only the former may use the development-only fallback.
type ResourceUnavailable struct{ Cause error }

func (e *ResourceUnavailable) Error() string {
	return "independent resource enforcement unavailable: " + e.Cause.Error()
}
func (e *ResourceUnavailable) Unwrap() error { return e.Cause }

func probeStrict(self string) error {
	if err := ProbeNamespaces(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	unit := fmt.Sprintf("persona-ext-probe-%d.scope", time.Now().UnixNano())
	out, err := exec.CommandContext(ctx, "systemd-run", "--user", "--scope", "--quiet", "--collect", "--unit", unit,
		"-p", "MemoryMax=128M", "-p", "MemorySwapMax=0", "-p", "CPUQuota=20%", "--", self, RunCommand, "--resource-check", "128", "20", unit, "--", "/bin/true").CombinedOutput()
	if err != nil {
		return &ResourceUnavailable{Cause: errors.New(firstLine(out, err))}
	}
	return nil
}

// ProbeNamespaces verifies the unchanged filesystem/network boundary separately.
func ProbeNamespaces() error {
	s := Probe()
	if s.OS != "linux" || !s.Available() {
		return errors.New("independent Linux sandbox unavailable: " + s.Why)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dir, err := os.MkdirTemp("", "persona-strict-probe-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	hidden := filepath.Join(dir, "hidden")
	if err := os.WriteFile(hidden, []byte("private"), 0o600); err != nil {
		return err
	}
	ns, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		return err
	}
	args := []string{"--unshare-user", "--unshare-ipc", "--unshare-pid", "--unshare-net", "--unshare-uts", "--cap-drop", "ALL"}
	for _, d := range systemDirs {
		args = append(args, "--ro-bind-try", d, d)
	}
	args = append(args, "--ro-bind", "/proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp", "--remount-ro", "/", "--", "/bin/sh", "-c",
		`test ! -e "$1" && test "$(readlink /proc/self/ns/net)" != "$2" && ! touch /persona-probe-write`, "probe", hidden, ns)
	if out, err := exec.CommandContext(ctx, s.Bwrap, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("namespace/read-only enforcement unavailable: %s", firstLine(out, err))
	}
	return nil
}

// VerifyResources is executed INSIDE the fresh scope, BEFORE bubblewrap or the engine.
// It verifies effective kernel files and membership in this exact scope, never just
// systemd's cached property values. No guest has started when it fails.
func VerifyResources(memoryMB, cpuPercent int, unit string) error {
	if memoryMB <= 0 || cpuPercent <= 0 || !strings.HasPrefix(unit, "persona-ext-") || !strings.HasSuffix(unit, ".scope") || strings.ContainsAny(unit, "/\\") {
		return errors.New("invalid independent resource scope")
	}
	cg, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return err
	}
	mounts, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	return verifyResourceFiles(string(cg), string(mounts), memoryMB, cpuPercent, unit, os.ReadFile)
}

func verifyResourceFiles(cgroups, mounts string, memoryMB, cpuPercent int, unit string, read func(string) ([]byte, error)) error {
	paths := map[string]string{}
	for _, line := range strings.Split(cgroups, "\n") {
		v := strings.SplitN(line, ":", 3)
		if len(v) != 3 {
			continue
		}
		if v[0] == "0" && v[1] == "" {
			paths["v2"] = v[2]
		}
		for _, c := range strings.Split(v[1], ",") {
			paths[c] = v[2]
		}
	}
	file := func(controller, name string) (string, error) {
		cg := paths[controller]
		if filepath.Base(cg) != unit {
			return "", fmt.Errorf("%s controller is not delegated to this extension scope", controller)
		}
		for _, line := range strings.Split(mounts, "\n") {
			parts := strings.SplitN(line, " - ", 2)
			if len(parts) != 2 {
				continue
			}
			left, right := strings.Fields(parts[0]), strings.Fields(parts[1])
			if len(left) < 5 || len(right) < 3 {
				continue
			}
			if (controller == "v2" && right[0] != "cgroup2") || (controller != "v2" && (right[0] != "cgroup" || !containsController(right[2], controller))) {
				continue
			}
			rel, err := filepath.Rel(left[3], cg)
			if err != nil || !filepath.IsLocal(rel) {
				return "", errors.New("resource cgroup is outside its mount")
			}
			return filepath.Join(left[4], rel, name), nil
		}
		return "", fmt.Errorf("%s resource mount unavailable", controller)
	}
	number := func(c, name string) (int64, error) {
		p, err := file(c, name)
		if err != nil {
			return 0, err
		}
		b, err := read(p)
		if err != nil {
			return 0, err
		}
		return strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	}
	limit := int64(memoryMB) << 20
	if paths["v2"] != "" {
		m, err := number("v2", "memory.max")
		if err != nil || m <= 0 || m > limit {
			return errors.New("independent memory.max unavailable or exceeds budget")
		}
		swap, err := number("v2", "memory.swap.max")
		if err != nil || swap != 0 {
			return errors.New("independent swap denial unavailable")
		}
		p, err := file("v2", "cpu.max")
		if err != nil {
			return err
		}
		b, err := read(p)
		if err != nil {
			return err
		}
		v := strings.Fields(string(b))
		if len(v) != 2 {
			return errors.New("cpu.max unavailable")
		}
		q, e1 := strconv.ParseInt(v[0], 10, 64)
		period, e2 := strconv.ParseInt(v[1], 10, 64)
		if e1 != nil || e2 != nil || q <= 0 || period <= 0 || q*100 > period*int64(cpuPercent) {
			return errors.New("independent CPU quota unavailable or exceeds budget")
		}
		return nil
	}
	m, err := number("memory", "memory.limit_in_bytes")
	if err != nil {
		return err
	}
	if m <= 0 || m > limit {
		return errors.New("independent memory limit exceeds budget")
	}
	memsw, err := number("memory", "memory.memsw.limit_in_bytes")
	if err != nil || memsw > m || memsw <= 0 {
		return errors.New("independent swap denial unavailable")
	}
	q, err := number("cpu", "cpu.cfs_quota_us")
	if err != nil {
		return err
	}
	period, err := number("cpu", "cpu.cfs_period_us")
	if err != nil {
		return err
	}
	if q <= 0 || period <= 0 || q*100 > period*int64(cpuPercent) {
		return errors.New("independent CPU quota unavailable or exceeds budget")
	}
	return nil
}
func containsController(s, c string) bool {
	for _, v := range strings.Split(s, ",") {
		if v == c {
			return true
		}
	}
	return false
}
