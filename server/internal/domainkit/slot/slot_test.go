package slot_test

import (
	"testing"

	"github.com/nwverse/persona/server/internal/domainkit/defect"
	"github.com/nwverse/persona/server/internal/domainkit/slot"
)

func TestSlots(t *testing.T) {
	var first slot.Slot
	if first.Wire() != 0 || slot.NewSlot(0) != first || slot.NewSlot(41).Wire() != 41 {
		t.Error("a slot's Wire is not its index")
	}
	for _, bad := range []int{-1, 1 << 31} {
		func() {
			defer func() {
				if _, ok := recover().(defect.Defect); !ok {
					t.Errorf("NewSlot(%d) is no defect", bad)
				}
			}()
			slot.NewSlot(bad)
		}()
	}
}
