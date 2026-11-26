package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/linktray"
)

// The tray says explain's two pairing failures in the user's language: pairError marks them, and leaves the rest.
func TestPairErrorMarksWhatTheTrayExplains(t *testing.T) {
	var pe *linktray.PairError
	err := pairError(errors.Join(refused("https://192.0.2.10:18662"), refused("https://hub.example.com:18662")))
	if !errors.As(err, &pe) || pe.Rejected || strings.Join(pe.Hosts, ",") != "192.0.2.10:18662,hub.example.com:18662" {
		t.Fatalf("unreachable: %#v", err)
	}
	err = pairError(errors.Join(refused("https://192.0.2.10:18662"), errors.New("https://hub.example.com:18662: hub 拒绝了：invalid token")))
	if !errors.As(err, &pe) || !pe.Rejected {
		t.Fatalf("rejected: %#v", err)
	}
	other := errors.New("hub 没给出链路凭据：升级 hub")
	if got := pairError(other); got != other {
		t.Fatalf("other: %#v", got)
	}
}
