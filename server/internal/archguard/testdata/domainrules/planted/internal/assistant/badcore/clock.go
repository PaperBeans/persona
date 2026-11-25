package badcore

import "time" // core-import: the frozen time edge of a legacy core

func stamp() int64 { return time.Now().UnixNano() } // core-clock-call
