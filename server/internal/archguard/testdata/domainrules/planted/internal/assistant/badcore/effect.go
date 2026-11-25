package badcore

func spawn() { go spawn() } // core-effect

func stop() { panic("stop") } // core-effect
