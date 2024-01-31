package common

// enum for the debug level
type DbgLevel int

const (
	DbgLvlNone = iota
	DbgLvlInfo
	DbgLvlDebug
	DbgLvlError
	DbgLvlFatal
)

var (
	// DebugLevel is the debug level for logging
	debugLevel DbgLevel
)
