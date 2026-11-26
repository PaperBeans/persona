package badapp

import (
	"reflect"
	"unsafe" // out-unsafe

	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
)

func peek(s fleetcount.State) uintptr {
	_ = reflect.ValueOf(s) // out-unsafe
	return unsafe.Sizeof(s)
}
