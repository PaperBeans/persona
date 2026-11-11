//go:build !linux

package node

func fileReadIsSysfs(fileReadHandle) (bool, error) { return false, nil }
