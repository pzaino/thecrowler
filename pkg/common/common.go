package common

import (
	"log"
)

// This function allow to set the current debug level
func SetDebugLevel(dbgLvl DbgLevel) {
	debugLevel = dbgLvl
}

// This function return the value of the current debug level
func GetDebugLevel() DbgLevel {
	return debugLevel
}

// DebugInfo is a function that prints debug information
func DebugMsg(dbgLvl DbgLevel, msg string, args ...interface{}) {
	if dbgLvl != DbgLvlFatal {
		if GetDebugLevel() >= dbgLvl {
			log.Printf(msg, args...)
		}
	} else {
		log.Fatalf(msg, args...)
	}
}
