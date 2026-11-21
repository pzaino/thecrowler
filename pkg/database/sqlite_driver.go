//go:build cgo

package database

// SQLite support is provided by the CGO-backed go-sqlite3 driver. Keeping the
// import behind the cgo build tag lets CGO-disabled builds omit the driver
// while CGO-enabled production builds expose the SQLite runtime and the
// dbDeploy maintenance command.
import _ "github.com/mattn/go-sqlite3"
