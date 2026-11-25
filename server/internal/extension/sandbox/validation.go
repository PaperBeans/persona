package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/osproc"
)

const ValidationRunCommand = "__source-validator-run"

// ValidationProcess is a narrow native launch seam for trusted validators. The
// default requires a fresh, verified kernel memory/swap/CPU/pids scope. Only a
// tagged manager's exact opt-in can create the sealed development launch policy;
// child environment variables and source/manifest data cannot select it.
// The caller owns stdin/stdout, cancellation, reaping and the returned disposal.
func ValidationProcess(ctx context.Context, self, command string, args []string, memoryMB, cpuPercent int) (*exec.Cmd, func(), error) {
	if runtime.GOOS != "linux" || memoryMB <= 0 || cpuPercent <= 0 {
		return nil, nil, errors.New("independent validator resources unavailable")
	}
	if os.Getenv(DevelopmentResourceEnv) == "1" {
		if !developmentResourcesBuilt {
			return nil, nil, errors.New("development validator resources unavailable in this build")
		}
		// The fallback has a fixed native budget, never guest-selected limits.
		if memoryMB != 128 || cpuPercent != 50 {
			return nil, nil, errors.New("invalid development validator budget")
		}
		missing, err := validationNeedsDevelopment(ctx, self)
		if err != nil {
			return nil, nil, err
		}
		if missing {
			return developmentValidationProcess(ctx, self, command, args)
		}
	}
	self, err := filepath.EvalSymlinks(self)
	if err != nil {
		return nil, nil, err
	}
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, nil, err
	}
	systemd, err := exec.LookPath("systemd-run")
	if err != nil {
		return nil, nil, err
	}
	systemctl, err := exec.LookPath("systemctl")
	if err != nil {
		return nil, nil, err
	}
	unit := fmt.Sprintf("persona-ext-validator-%d.scope", time.Now().UnixNano())
	env := []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LANG=C"}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "XDG_RUNTIME_DIR=") || strings.HasPrefix(kv, "DBUS_SESSION_BUS_ADDRESS=") {
			env = append(env, kv)
		}
	}
	argv := []string{"--user", "--scope", "--quiet", "--collect", "--unit", unit,
		"-p", "MemoryMax=" + strconv.Itoa(memoryMB) + "M", "-p", "MemorySwapMax=0",
		"-p", "CPUQuota=" + strconv.Itoa(cpuPercent) + "%", "-p", "TasksMax=32", "--",
		self, ValidationRunCommand, unit, strconv.Itoa(memoryMB), strconv.Itoa(cpuPercent), self, command, bwrap}
	cmd := exec.CommandContext(ctx, systemd, append(argv, args...)...)
	cmd.Env, cmd.Dir = env, "/"
	dispose := func() {
		// A scope may move descendants out of the wrapper's process group. Target
		// only this unique scope as well, including after cancellation or OOM.
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		kill := exec.CommandContext(cleanup, systemctl, "--user", "kill", "--signal=KILL", unit)
		kill.Env, kill.Dir = env, "/"
		kill.Stdout, kill.Stderr = io.Discard, io.Discard
		kill.WaitDelay = 100 * time.Millisecond
		_ = kill.Run()
		stop := exec.CommandContext(cleanup, systemctl, "--user", "stop", unit)
		stop.Env, stop.Dir = env, "/"
		stop.Stdout, stop.Stderr = io.Discard, io.Discard
		stop.WaitDelay = 100 * time.Millisecond
		_ = stop.Run()
	}
	return cmd, dispose, nil
}

func validationNeedsDevelopment(ctx context.Context, self string) (bool, error) {
	unit := fmt.Sprintf("persona-ext-validator-probe-%d.scope", time.Now().UnixNano())
	cmd := exec.CommandContext(ctx, "systemd-run", "--user", "--scope", "--quiet", "--collect", "--unit", unit,
		"-p", "MemoryMax=128M", "-p", "MemorySwapMax=0", "-p", "CPUQuota=50%", "-p", "TasksMax=32", "--",
		self, ValidationRunCommand, unit, "128", "50", self, "--validation-resource-probe", "unused")
	cmd.Env, cmd.Dir = validationManagerEnv(), "/"
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.SysProcAttr = osproc.GroupAttr()
	cmd.Cancel = func() error { return osproc.StopGroup(cmd.Process.Pid, true) }
	err := cmd.Run()
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	return err != nil, nil
}

func validationManagerEnv() []string {
	env := []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LANG=C"}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "XDG_RUNTIME_DIR=") || strings.HasPrefix(kv, "DBUS_SESSION_BUS_ADDRESS=") {
			env = append(env, kv)
		}
	}
	return env
}

// No package mounts, artifacts, proxy sockets, config, broad system directories,
// network bridge, writable home or native authority. Only the trusted executable,
// exact ELF dependencies and this validator's read-only resource scope are
// readable; source arrives on bounded stdin.
func ValidationNamespace(self, command string, args []string, resources []Mount) ([]string, error) {
	deps, err := ELFDependencies(self, map[string]bool{})
	if err != nil {
		return nil, err
	}
	ns := []string{"--die-with-parent", "--new-session", "--unshare-user", "--unshare-ipc", "--unshare-pid", "--unshare-net", "--unshare-uts", "--cap-drop", "ALL", "--clearenv",
		"--ro-bind", "/proc", "/proc", "--dev", "/dev", "--ro-bind", self, SelfPath}
	for _, dep := range deps {
		ns = append(ns, "--ro-bind", dep, dep)
	}
	for _, mount := range resources {
		if mount.DataFD > 0 {
			ns = append(ns, "--ro-bind-data", strconv.Itoa(mount.DataFD), mount.Target)
		} else {
			ns = append(ns, "--ro-bind", mount.Source, mount.Target)
		}
	}
	ns = append(ns, "--setenv", "HOME", "/nonexistent", "--setenv", "GOMAXPROCS", "2", "--remount-ro", "/", "--chdir", "/", "--", SelfPath, command)
	return append(ns, args...), nil
}

// VerifyValidationResources includes the task controller in both pre-parse
// gates. An accepted TasksMax property alone is not kernel enforcement.
func VerifyValidationResources(memoryMB, cpuPercent int, unit string) error {
	if err := VerifyResources(memoryMB, cpuPercent, unit); err != nil {
		return err
	}
	groups, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return err
	}
	mounts, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	return verifyValidationTasks(string(groups), string(mounts), unit, os.ReadFile)
}

func verifyValidationTasks(groups, mounts, unit string, read func(string) ([]byte, error)) error {
	var group string
	v2 := false
	for _, line := range strings.Split(groups, "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		if containsController(parts[1], "pids") {
			group, v2 = parts[2], false
			break
		}
		if parts[0] == "0" && parts[1] == "" {
			group, v2 = parts[2], true
		}
	}
	if filepath.Base(group) != unit {
		return errors.New("validator task controller is not independent")
	}
	for _, line := range strings.Split(mounts, "\n") {
		pair := strings.SplitN(line, " - ", 2)
		if len(pair) != 2 {
			continue
		}
		left, right := strings.Fields(pair[0]), strings.Fields(pair[1])
		if len(left) < 5 || len(right) < 3 || v2 && right[0] != "cgroup2" || !v2 && (right[0] != "cgroup" || !containsController(right[2], "pids")) {
			continue
		}
		rel, err := filepath.Rel(left[3], group)
		if err != nil || !filepath.IsLocal(rel) {
			return errors.New("validator task controller outside mount")
		}
		b, err := read(filepath.Join(left[4], rel, "pids.max"))
		if err != nil {
			return err
		}
		n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		if err != nil || n <= 0 || n > 32 {
			return errors.New("validator task limit unavailable or exceeds budget")
		}
		return nil
	}
	return errors.New("validator task controller unavailable")
}

// RunValidation verifies the scope in its own child before building the native
// sandbox. The parser re-verifies the same kernel files inside the namespace;
// a bare parser CLI invocation cannot skip independent resource enforcement.
func RunValidation(args []string) int {
	if len(args) > 0 && (args[0] == developmentValidationPolicy || args[0] == developmentValidationSupervisor || args[0] == "development-validator-exec-v1") {
		return runDevelopmentValidation(args)
	}
	if len(args) < 6 {
		return 2
	}
	unit := args[0]
	memory, e1 := strconv.Atoi(args[1])
	cpu, e2 := strconv.Atoi(args[2])
	if e1 != nil || e2 != nil || VerifyValidationResources(memory, cpu, unit) != nil {
		return 1
	}
	if args[4] == "--validation-resource-probe" {
		return 0 // trusted resource probe, no namespace or parser is launched
	}
	resources, err := validationResourceMounts(unit)
	if err != nil {
		return 1
	}
	ns, err := ValidationNamespace(args[3], args[4], append([]string{unit}, args[6:]...), resources)
	if err != nil {
		return 1
	}
	if err := osproc.Exec(args[5], append([]string{args[5]}, ns...), []string{"PATH=/usr/bin:/bin"}); err != nil {
		return 1
	}
	return 0
}

// Bind only the validator's own cgroup directory, never a shared controller
// root. This is kernel-owned resource metadata, not configuration or a socket.
func validationResourceMounts(unit string) ([]Mount, error) {
	groups, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return nil, err
	}
	mounts, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	return validationResourceDirs(string(groups), string(mounts), unit)
}
func validationResourceDirs(groups, mounts, unit string) ([]Mount, error) {
	var out []Mount
	for _, group := range strings.Split(groups, "\n") {
		parts := strings.SplitN(group, ":", 3)
		if len(parts) != 3 {
			continue
		}
		v2 := parts[0] == "0" && parts[1] == ""
		if !v2 && !containsController(parts[1], "memory") && !containsController(parts[1], "cpu") && !containsController(parts[1], "pids") {
			continue
		}
		if filepath.Base(parts[2]) != unit {
			return nil, errors.New("validator controller is not independent")
		}
		found := false
		for _, line := range strings.Split(mounts, "\n") {
			pair := strings.SplitN(line, " - ", 2)
			if len(pair) != 2 {
				continue
			}
			left, right := strings.Fields(pair[0]), strings.Fields(pair[1])
			if len(left) < 5 || len(right) < 3 {
				continue
			}
			if v2 && right[0] != "cgroup2" || !v2 && (right[0] != "cgroup" || !(containsController(parts[1], "memory") && containsController(right[2], "memory") || containsController(parts[1], "cpu") && containsController(right[2], "cpu") || containsController(parts[1], "pids") && containsController(right[2], "pids"))) {
				continue
			}
			rel, err := filepath.Rel(left[3], parts[2])
			if err != nil || !filepath.IsLocal(rel) {
				return nil, errors.New("validator controller outside mount")
			}
			dir := filepath.Join(left[4], rel)
			out = append(out, Mount{Source: dir, Target: dir})
			found = true
			break
		}
		if !found {
			return nil, errors.New("validator controller mount unavailable")
		}
	}
	if len(out) == 0 {
		return nil, errors.New("validator resource mounts unavailable")
	}
	return out, nil
}
