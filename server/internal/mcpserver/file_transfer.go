package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// fileTransferTool is the tool that copies files between the user's computers (assistant/file_transfer.go). The
// paths on this computer may be relative to the agent's working directory, which only this process knows: send's
// path and fetch's dest are made absolute here. The other computer's paths are left as they are.
const fileTransferTool = "file_transfer"

// fileTransferArgs is args with this computer's path absolute; args that cannot be read, or name no relative path
// here, are passed on unchanged (the manager refuses what it cannot take).
func fileTransferArgs(args json.RawMessage, cwd string) json.RawMessage {
	var a map[string]any
	if cwd == "" || json.Unmarshal(args, &a) != nil {
		return args
	}
	key := ""
	switch a["direction"] {
	case "send":
		key = "path"
	case "fetch":
		key = "dest"
	default:
		return args
	}
	p, _ := a[key].(string)
	p = strings.TrimSpace(p)
	if p == "" || p == "~" || strings.HasPrefix(p, "~/") || filepath.IsAbs(p) {
		return args
	}
	a[key] = filepath.Join(cwd, p)
	out, err := json.Marshal(a)
	if err != nil {
		return args
	}
	return out
}

// workingDir is this process's working directory, the agent's ("" when it cannot be read).
func workingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}
