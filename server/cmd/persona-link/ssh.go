package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/link"
)

// sshPairFlags holds the optional SSH tunnel flags on `pair` and `install --pair`.
type sshPairFlags struct {
	server    string
	target    string
	always    bool
	alwaysSet bool
}

func (f *sshPairFlags) consume(args []string, i int) (next int, ok bool, err error) {
	switch args[i] {
	case "--ssh-always":
		f.always, f.alwaysSet = true, true
		return i, true, nil
	case "--no-ssh-always":
		f.always, f.alwaysSet = false, true
		return i, true, nil
	}
	if v, n, ok, err := flagValue(args, i, "--ssh"); ok || err != nil {
		f.server = v
		return n, ok, err
	}
	if v, n, ok, err := flagValue(args, i, "--ssh-target"); ok || err != nil {
		f.target = v
		return n, ok, err
	}
	return i, false, nil
}

func (f sshPairFlags) any() bool {
	return f.server != "" || f.target != "" || f.alwaysSet
}

func (f sshPairFlags) build(rawLink string) (*link.SSH, error) {
	if !f.any() {
		return nil, nil
	}
	if strings.TrimSpace(f.server) == "" {
		return nil, errors.New("用了 --ssh-target 或 --ssh-always 时要一起填 --ssh <服务器>")
	}
	always := f.always
	if !f.alwaysSet {
		_, defAlways, ok := link.PairSSHDefaults(rawLink)
		if ok {
			always = defAlways
		}
	}
	return &link.SSH{Server: f.server, Target: f.target, Always: always}, nil
}

type sshStatusJSON struct {
	SSH           *link.SSH `json:"ssh"`
	DefaultTarget string    `json:"default_target,omitempty"`
}

type sshTestJSON struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty"`
	Server  string `json:"server,omitempty"`
	Target  string `json:"target,omitempty"`
	Message string `json:"message"`
}

func sshCmd(e *env, args []string) error {
	var server, target, rawLink string
	var off, testOnly, asJSON, always, alwaysSet, targetSet, serverSet bool
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--off":
			off = true
		case a == "--test":
			testOnly = true
		case a == "--json":
			asJSON = true
		case a == "--always":
			always, alwaysSet = true, true
		case a == "--no-always":
			always, alwaysSet = false, true
		default:
			if v, next, ok, err := flagValue(args, i, "--target"); ok || err != nil {
				if err != nil {
					return err
				}
				target, targetSet, i = v, true, next
				continue
			}
			if v, next, ok, err := flagValue(args, i, "--link"); ok || err != nil {
				if err != nil {
					return err
				}
				rawLink, i = v, next
				continue
			}
			if strings.HasPrefix(a, "-") || serverSet {
				return errUsage
			}
			server, serverSet = a, true
		}
	}

	if off && (serverSet || targetSet || alwaysSet || testOnly || rawLink != "") {
		return errUsage
	}

	cfgPath := e.configPath()
	c, readErr := link.Read(cfgPath)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}

	// `persona-link ssh [--json]`: show current tunnel.
	if !off && !testOnly && !serverSet && !targetSet && !alwaysSet {
		if rawLink != "" {
			return errUsage
		}
		var defTarget string
		if c != nil {
			defTarget = link.DefaultSSHTarget(c.Endpoints)
		}
		if asJSON {
			enc := json.NewEncoder(e.stdout)
			enc.SetIndent("", "  ")
			var cur *link.SSH
			if c != nil {
				cur = c.SSH
			}
			return enc.Encode(sshStatusJSON{SSH: cur, DefaultTarget: defTarget})
		}
		if c == nil || c.SSH == nil {
			fmt.Fprintln(e.stdout, "没有设 SSH 隧道（persona-link ssh <服务器> 设一个）。")
			return nil
		}
		fmt.Fprintf(e.stdout, "SSH 隧道：%s → %s（%s）\n", c.SSH.Server, c.SSH.Target, sshRouteText(c.SSH.Always))
		return nil
	}

	if off {
		if _, err := link.SetSSH(cfgPath, nil); err != nil {
			return err
		}
		pokeRunning(cfgPath)
		if asJSON {
			enc := json.NewEncoder(e.stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(sshStatusJSON{SSH: nil})
		}
		fmt.Fprintln(e.stdout, "已移除 SSH 隧道。")
		if link.Running(cfgPath) {
			fmt.Fprintln(e.stdout, "在运行的 persona-link 正在切回直连。")
		}
		return nil
	}

	// Fill missing fields from existing saved tunnel when editing or testing.
	if !serverSet && c != nil && c.SSH != nil {
		server = c.SSH.Server
	}
	if !targetSet && rawLink == "" && c != nil && c.SSH != nil && (!serverSet || server == c.SSH.Server) {
		target = c.SSH.Target
	}
	if !alwaysSet {
		switch {
		case c != nil && c.SSH != nil && (!serverSet || server == c.SSH.Server):
			always = c.SSH.Always
		case rawLink != "":
			if _, defAlways, ok := link.PairSSHDefaults(rawLink); ok {
				always = defAlways
			}
		case c != nil:
			always = link.AllLoopback(c.Endpoints)
		}
	}

	if testOnly {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		norm, err := link.TestSSH(ctx, link.SSH{Server: server, Target: target, Always: always}, rawLink, c, e.sshBin)
		if asJSON {
			rep := sshTestJSON{OK: err == nil}
			if norm != nil {
				rep.Server, rep.Target = norm.Server, norm.Target
			} else {
				rep.Server, rep.Target = strings.TrimSpace(server), strings.TrimSpace(target)
			}
			if err == nil {
				rep.Message = fmt.Sprintf("经 SSH 连上了 %s 上的 hub。", rep.Target)
			} else {
				var se *link.SSHError
				if errors.As(err, &se) {
					rep.Code = se.Code
					if se.Server != "" {
						rep.Server = se.Server
					}
					if se.Target != "" {
						rep.Target = se.Target
					}
				}
				rep.Message = err.Error()
			}
			enc := json.NewEncoder(e.stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(rep)
			if err != nil {
				return exitCode(1)
			}
			return nil
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(e.stdout, "经 SSH 连上了 %s 上的 hub。\n", norm.Target)
		return nil
	}

	if rawLink != "" {
		return errUsage
	}
	if strings.TrimSpace(server) == "" {
		return errors.New("要填 SSH 服务器地址：persona-link ssh <服务器>")
	}
	saved, err := link.SetSSH(cfgPath, &link.SSH{Server: server, Target: target, Always: always})
	if err != nil {
		return err
	}
	pokeRunning(cfgPath)
	if asJSON {
		enc := json.NewEncoder(e.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(sshStatusJSON{SSH: saved})
	}
	fmt.Fprintf(e.stdout, "已设好 SSH 隧道：%s → %s（%s）。\n", saved.Server, saved.Target, sshRouteText(saved.Always))
	if link.Running(cfgPath) {
		fmt.Fprintln(e.stdout, "在运行的 persona-link 正在按新设置重连。")
	}
	return nil
}

func sshRouteText(always bool) string {
	if always {
		return "总是走 SSH"
	}
	return "先试直连，不通再走 SSH"
}
