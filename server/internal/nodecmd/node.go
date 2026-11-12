package nodecmd

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/nwverse/persona/server/internal/clitext"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/node"
	"github.com/nwverse/persona/server/internal/nodeupdate"
	"github.com/nwverse/persona/server/internal/osproc"
	"github.com/nwverse/persona/server/internal/sysmon"
	"github.com/nwverse/persona/server/internal/tunnel"
)

// NodeUsage is the node part of the usage, also what `personad node -h` prints.
const NodeUsage = `  personad node --stdio     run the node protocol on stdin/stdout (started by the hub over ssh); with a resident
                           node running on this machine, relay to it instead
  personad node --attach    resident node (launchd, in the login session): serves what --stdio relays, so what it
                           starts can use the login keychain (docs/server/server.md「Persistent node」); set up for tailcat,
                           also reachable by the hub over tailcat
  personad node --attach --supervised
                           the same, run by something other than systemd or launchd that starts it again whenever it
                           exits (a container's entry script, supervisord, runit, s6): the hub can then update it
                           (docs/operations/link.md "Without systemd"). Not for nohup or cron @reboot: nothing restarts those
  personad node tailcat init --hub <key> [--derp-map-url <url>]
                           let the hub reach this machine over tailcat when it cannot ssh here; prints the address
                           for hub.json (docs/server/server.md「Tailcat hole punching」)
  personad node tailcat addr
                           print this machine's tailcat address again
  personad node doctor [--json] [--fix]
                           read-only check of this machine's node: node.sock / runtime.sock answer, the generated hook,
                           MCP and plugin files match this binary, the programs they run exist, claude's version, the
                           Mac LaunchAgent's ProcessType, and whether what claude reads at start can be read;
                           --fix rewrites Persona's own generated files, and on a Mac writes ProcessType=Interactive
                           into the LaunchAgent and reloads it (docs/integrations/claude-integration.md「Self-check」)
  personad node reset-uid   give this computer a new machine uid (~/.persona/machine-uid): for a virtual machine cloned
                           with another's home directory, which hubs would take for that one (docs/server/mesh.md)`

// checkNodeStdioArgs accepts what runs the node protocol on stdin/stdout: no argument (as before) or --stdio. Anything
// else is a mistyped subcommand, which would otherwise wait on stdin without a word.
func checkNodeStdioArgs(args []string) error {
	if len(args) == 0 || len(args) == 1 && args[0] == "--stdio" {
		return nil
	}
	return fmt.Errorf("不认识的 node 子命令 %q：可用的有 --stdio、--attach、tailcat、doctor、reset-uid（%s node -h 看用法）", strings.Join(args, " "), CommandName())
}

// UsageError is a usage mistake: main prints it as it is (no log timestamp) and exits 2 (clitext).
type UsageError = clitext.UsageError

// CommandName is the program's name in what it prints (clitext.CommandName).
func CommandName() string { return clitext.CommandName() }

// Named is a usage text (written with personad) with the program's name as it was run (clitext.Named).
func Named(text string) string { return clitext.Named(text) }

// IsHelpArg: -h or --help.
func IsHelpArg(a string) bool { return clitext.IsHelpArg(a) }

// noExtraArgs refuses arguments after a node subcommand that takes none: reset-uid cannot be undone, so a mistyped
// flag after it must not go ahead as if it were not there.
func noExtraArgs(cmd string, rest []string) error {
	if len(rest) == 0 {
		return nil
	}
	return fmt.Errorf("node %s 不带参数，多了 %q（%s node -h 看用法）", cmd, strings.Join(rest, " "), CommandName())
}

// exitNodeUsage is a usage mistake under `personad node`: the error, the node usage, exit 2.
func exitNodeUsage(err error) {
	fmt.Fprintln(os.Stderr, err)
	fmt.Fprintln(os.Stderr, Named(NodeUsage))
	os.Exit(2)
}

// nodeProcs is the most Ps (GOMAXPROCS) the node that serves a hub keeps unless the environment says otherwise. It
// sleeps most of the time, between a few timers and reads; on a machine with many cores every P Go keeps is one more to
// wake and search for work on each of those, and more threads and per-P caches held (on a 64-core host with 4 watched
// sessions: 28 threads against 12, and about 4 MB more resident than with 4).
const nodeProcs = 4

// capProcs lowers GOMAXPROCS to nodeProcs for `node --stdio` and `node --attach`, unless the GOMAXPROCS environment
// variable is set (that one wins, as it does for the runtime). A runtime.GOMAXPROCS call stops the runtime's own
// updates of it (from a container's or cgroup's CPU limit changing later), so it is made only when it lowers it: a
// machine or container with 4 CPUs or fewer keeps the runtime's value and its updates. The variable is not set, so the
// agents and programs the node starts keep their own defaults.
func capProcs() {
	if os.Getenv("GOMAXPROCS") != "" {
		return
	}
	if runtime.GOMAXPROCS(0) > nodeProcs {
		runtime.GOMAXPROCS(nodeProcs)
	}
}

// residentArgs reads what may follow --attach: --supervised, at most once. Anything else is refused rather than
// ignored: a mistyped flag would leave the node quietly unable to take updates from the hub.
func residentArgs(args []string) (supervised bool, err error) {
	for _, a := range args {
		if a != "--supervised" || supervised {
			return false, fmt.Errorf("node --attach takes only --supervised, not %q (%s node -h for usage)", strings.Join(args, " "), CommandName())
		}
		supervised = true
	}
	return supervised, nil
}

// runResident is `personad node --attach`: it serves local/SSH relays and optional tailcat connections.
// Deployment and update requests drain idle work before exec retains the Unix listener and run lock.
// supervised describes an external supervisor other than systemd or launchd (nodeupdate.Options).
func runResident(ctx context.Context, b Build, supervised bool) error {
	stop := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	transport, err := node.OpenResidentTransport()
	if err != nil {
		return err
	}
	defer transport.Close()
	requests := make(chan string, 1)
	askRestart := func(reason string) {
		select {
		case requests <- reason:
		default: // repeated deploys coalesce while waiting for idle work
		}
	}
	updater := nodeupdate.New(b.Version, func() { askRestart("update") }, nodeupdate.Options{Supervised: supervised, RequireResidentHandover: node.ResidentHandoverSupported})
	defer updater.Close()
	if updater.RestartRequired() {
		check, err := residentReplacementInfo(ctx, updater.Executable(), b.Program)
		if err != nil {
			return err
		}
		files, err := prepareResidentRecovery(transport, check.ResidentHandover)
		if err != nil {
			return err
		}
		if files == nil {
			return syscall.Exec(updater.Executable(), os.Args, os.Environ())
		}
		defer files.Close()
		return files.Exec(updater.Executable(), os.Args, os.Environ())
	}
	node.SetResident(b.Program, b.Version)
	logger := log.New(os.Stderr, "", log.LstdFlags)
	tctx, tstop := context.WithCancel(context.Background())
	defer tstop()
	conns, stopped, err := node.ServeTailcat(tctx, logger, func() { askRestart("tailcat update") })
	if err != nil {
		logger.Printf("tailcat: %v (serving relays over ssh only)", err)
	}
	reload := &node.ResidentReload{}
	handover := make(chan *node.ResidentFiles, 1)
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		for {
			var reason string
			select {
			case reason = <-requests:
			case <-hup:
				reason = "deployment"
			case <-ctx.Done():
				return
			}
			if err := checkResidentReplacement(ctx, updater.Executable(), b.Program); err != nil {
				logger.Printf("resident reload: replacement did not pass preflight; keeping the current node")
				continue
			}
			logger.Printf("restart asked for (%s): draining idle work while attach remains available", reason)
			if err := reload.Prepare(ctx); err != nil {
				return
			}
			files, err := transport.PrepareExec()
			if err != nil {
				reload.Resume()
				logger.Printf("resident reload: cannot retain the attach listener; keeping the current node")
				continue
			}
			if ctx.Err() != nil {
				files.Close()
				return
			}
			announce, end := context.WithTimeout(ctx, time.Second)
			err = reload.Announce(announce)
			end()
			if err != nil {
				files.Close()
				reload.Resume()
				reload.CancelAnnouncement()
				logger.Printf("resident reload: handover was not acknowledged; keeping the current node")
				continue
			}
			handover <- files
			cancel() // only now: idle work is reaped and the listener/backlog and run lock have retained duplicates
			return
		}
	}()
	err = node.AttachWithTransport(ctx, func() (*node.Node, error) {
		n, err := node.New(b.Version)
		if err == nil {
			n.SetUpdater(updater)
			n.SetResidentReload(reload)
			n.Program = b.Program
		}
		return n, err
	}, conns, node.NewRuntimeHost(b.Version, ""), transport)
	cancel()
	<-workerDone
	tstop()
	<-stopped
	select {
	case files := <-handover:
		defer files.Close()
		if stop.Err() != nil {
			return nil // an explicit stop wins over a deployment
		}
		logger.Printf("restarting on %s with the retained attach listener", updater.Executable())
		return files.Exec(updater.Executable(), os.Args, os.Environ())
	default:
		return err
	}
}

// runNodeTailcat is `personad node tailcat init|addr`.
func runNodeTailcat(ctx context.Context, args []string) error {
	if len(args) == 0 {
		exitNodeUsage(fmt.Errorf("node tailcat 要跟 init 或 addr（%s node -h 看用法）", CommandName()))
	}
	switch args[0] {
	case "addr":
		if err := noExtraArgs("tailcat addr", args[1:]); err != nil {
			exitNodeUsage(err)
		}
		addr, err := node.TailcatAddr()
		if err != nil {
			return err
		}
		fmt.Println(addr)
		return nil
	case "init":
		fs := flag.NewFlagSet("tailcat init", flag.ContinueOnError)
		hubKey := fs.String("hub", "", "the hub's `key`: what "+CommandName()+" hub tailcat-key prints on the hub's machine")
		derpMap := fs.String("derp-map-url", "", "a DERP map of your own relays instead of Tailscale's public ones")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() > 0 {
			exitNodeUsage(fmt.Errorf("node tailcat init 只认 --hub 和 --derp-map-url，多了 %q（%s node -h 看用法）", strings.Join(fs.Args(), " "), CommandName()))
		}
		if *hubKey == "" {
			return fmt.Errorf("--hub is required: run `%s hub tailcat-key` on the hub's machine", CommandName())
		}
		ictx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		k, err := tunnel.InitNodeKey(ictx, tunnel.NodeKeyPath(), *hubKey, *derpMap)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "set up %s (DERP region %d). In the hub's hub.json, this machine's entry gets\n"+
			"  \"tailcat_addr\": the line below (a secret: it lets the hub's key in)\n"+
			"then restart the resident node here (scripts/deploy/node-launchd.sh install) and the hub.\n", tunnel.NodeKeyPath(), k.RegionID)
		fmt.Println(k.Addr())
		return nil
	}
	exitNodeUsage(fmt.Errorf("不认识的 node tailcat 子命令 %q：可用的有 init、addr（%s node -h 看用法）", args[0], CommandName()))
	return nil
}

// sysmonMaxCount caps `sysmon [n]` at a day of 10-second samples: a bigger n never ends in practice, and the
// largest ints overflowed n+1 in the summary.
const sysmonMaxCount = 8640

// sysmonCount reads `sysmon [n]`: how many samples after the first, 3 when not given.
func sysmonCount(args []string) (int, error) {
	if len(args) == 0 {
		return 3, nil
	}
	n, err := strconv.Atoi(args[0])
	if len(args) > 1 || err != nil || n < 0 {
		return 0, fmt.Errorf("usage: %s sysmon [n]   (n: how many 10-second samples after the first, 0 or more)", CommandName())
	}
	if n > sysmonMaxCount {
		return 0, fmt.Errorf("%s sysmon: n is at most %d (a day of 10-second samples), not %d", CommandName(), sysmonMaxCount, n)
	}
	return n, nil
}

// sysmonSamples calls sample 1 + n times, one `every` apart, and says how many it took: fewer when ctx ends
// (Ctrl-C), which also cuts the wait in between short.
func sysmonSamples(ctx context.Context, n int, every time.Duration, sample func()) int {
	taken := 0
	for i := 0; i <= n && ctx.Err() == nil; i++ {
		if i > 0 {
			t := time.NewTimer(every)
			select {
			case <-ctx.Done():
				t.Stop()
				return taken
			case <-t.C:
			}
		}
		sample()
		taken++
	}
	return taken
}

// runSysmon shows what a node would report (docs/server/machines.md), and the CPU time it took: how the collection cost
// in the docs was measured.
func runSysmon(ctx context.Context, n int) {
	enc := json.NewEncoder(os.Stdout)
	start := time.Now()
	_ = enc.Encode(sysmon.ReadSystem(ctx))
	fmt.Printf("system info: %v\n", time.Since(start).Round(time.Millisecond))
	c := sysmon.NewCollector(ctx)
	defer c.Close()
	cpu0 := osproc.CPUTime()
	began := time.Now()
	var took time.Duration
	taken := sysmonSamples(ctx, n, 10*time.Second, func() {
		t := time.Now()
		s := c.Sample(t)
		took += time.Since(t)
		_ = enc.Encode(s)
	})
	if taken == 0 {
		fmt.Println("0 samples: stopped before the first")
		return
	}
	cpu := osproc.CPUTime() - cpu0
	wall := time.Since(began)
	fmt.Printf("%d samples: %v each; %s CPU %v over %v (%.3f%%), not counting iostat / vm_stat on macOS\n",
		taken, (took / time.Duration(taken)).Round(time.Microsecond), CommandName(), cpu.Round(time.Millisecond), wall.Round(time.Second),
		100*cpu.Seconds()/wall.Seconds())
}

// resetMachineUID makes a new ~/.persona/machine-uid; the node picks it up when it starts again.
func resetMachineUID() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	uid, err := node.ResetMachineUID(identity.DataDir(home))
	if err != nil {
		return err
	}
	fmt.Println(uid)
	fmt.Fprintln(os.Stderr, "已换成新的机器 uid。重启这台电脑上的常驻 node（scripts/deploy/node-launchd.sh install 会重启它），hub 下次连上时就用新的。")
	return nil
}
