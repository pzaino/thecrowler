package plugin

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/robertkrimen/otto"
	gosnowflake "github.com/snowflakedb/gosnowflake/v2"
)

func TestExternalConfigString(t *testing.T) {
	config := map[string]interface{}{
		"host":     "example.local",
		"username": "",
		"dbname":   "crowler",
	}

	if got := externalConfigString(config, "host", "localhost"); got != "example.local" {
		t.Fatalf("expected host to be used, got %q", got)
	}

	if got := externalConfigString(config, "missing", "dbname"); got != "crowler" {
		t.Fatalf("expected fallback to dbname, got %q", got)
	}

	if got := externalConfigString(map[string]interface{}{"empty": ""}, "empty", "fallback"); got != "" {
		t.Fatalf("expected empty value to be ignored, got %q", got)
	}
}

func TestExternalConfigInt(t *testing.T) {
	config := map[string]interface{}{
		"timeout": 45,
		"port":    float64(5432),
		"count":   "7",
	}

	if got := externalConfigInt(config, "timeout"); got != 45 {
		t.Fatalf("expected int value to be parsed, got %d", got)
	}

	if got := externalConfigInt(config, "port"); got != 5432 {
		t.Fatalf("expected float64 value to be parsed, got %d", got)
	}

	if got := externalConfigInt(config, "count"); got != 7 {
		t.Fatalf("expected string value to be parsed, got %d", got)
	}

	if got := externalConfigInt(map[string]interface{}{"missing": nil}, "missing"); got != 0 {
		t.Fatalf("expected missing value to default to zero, got %d", got)
	}
}

func TestExternalDBName(t *testing.T) {
	config := map[string]interface{}{"db_name": "primary_db"}
	if got := externalDBName(config); got != "primary_db" {
		t.Fatalf("expected db_name to be preferred, got %q", got)
	}

	config = map[string]interface{}{"dbname": "fallback_db"}
	if got := externalDBName(config); got != "fallback_db" {
		t.Fatalf("expected dbname fallback, got %q", got)
	}

	config = map[string]interface{}{"database": "database_fallback"}
	if got := externalDBName(config); got != "database_fallback" {
		t.Fatalf("expected database fallback, got %q", got)
	}
}

func TestExternalDBCallContext(t *testing.T) {
	ctx, cancel := externalDBCallContext(context.Background(), map[string]interface{}{"timeout": 45})
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected a deadline to be set")
	}

	remaining := time.Until(deadline)
	if remaining < 40*time.Second || remaining > 50*time.Second {
		t.Fatalf("expected about 45s timeout, got %v", remaining)
	}

	ctx2, cancel2 := externalDBCallContext(context.Background(), map[string]interface{}{"timeout": 4000})
	defer cancel2()

	deadline2, ok := ctx2.Deadline()
	if !ok {
		t.Fatal("expected a capped deadline to be set")
	}

	remaining2 := time.Until(deadline2)
	if remaining2 < 3590*time.Second || remaining2 > 3600*time.Second {
		t.Fatalf("expected timeout to be capped at 3600s, got %v", remaining2)
	}
}

func TestBuildSnowflakeConfig(t *testing.T) {
	config := map[string]interface{}{
		"account":       "acme",
		"user":          "alice",
		"password":      "secret",
		"db_name":       "analytics",
		"schema":        "public",
		"warehouse":     "wh",
		"role":          "sysadmin",
		"authenticator": "oauth",
		"token":         "abc123",
	}

	sfConfig, err := buildSnowflakeConfig(config)
	if err != nil {
		t.Fatalf("buildSnowflakeConfig returned an unexpected error: %v", err)
	}

	if sfConfig.Account != "acme" {
		t.Fatalf("expected account to be copied, got %q", sfConfig.Account)
	}
	if sfConfig.Database != "analytics" {
		t.Fatalf("expected database to be copied, got %q", sfConfig.Database)
	}
	if sfConfig.Authenticator != gosnowflake.AuthTypeOAuth {
		t.Fatalf("expected OAuth authenticator, got %v", sfConfig.Authenticator)
	}
	if sfConfig.Token != "abc123" {
		t.Fatalf("expected token to be copied, got %q", sfConfig.Token)
	}

	_, err = buildSnowflakeConfig(map[string]interface{}{"user": "alice"})
	if err == nil {
		t.Fatal("expected missing Snowflake account to fail")
	}
}

func TestExternalDBSQLArgs(t *testing.T) {
	vm := otto.New()

	argsValue, err := vm.ToValue([]interface{}{"hello", 42})
	if err != nil {
		t.Fatalf("failed to create otto value: %v", err)
	}

	args, err := externalDBSQLArgs(argsValue)
	if err != nil {
		t.Fatalf("externalDBSQLArgs returned an unexpected error: %v", err)
	}
	if len(args) != 2 || args[0] != "hello" || args[1] != 42 {
		t.Fatalf("unexpected SQL args: %#v", args)
	}

	undefinedValue := otto.UndefinedValue()
	args, err = externalDBSQLArgs(undefinedValue)
	if err != nil {
		t.Fatalf("externalDBSQLArgs should allow undefined values: %v", err)
	}
	if args != nil {
		t.Fatalf("expected nil args for undefined value, got %#v", args)
	}
}

func TestExternalDBReadRows(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite database: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatalf("failed to create sqlite table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, name) VALUES (42, 'alice')`); err != nil {
		t.Fatalf("failed to insert sqlite row: %v", err)
	}

	rows, err := db.Query(`SELECT id, name FROM users`)
	if err != nil {
		t.Fatalf("Query returned an unexpected error: %v", err)
	}
	defer rows.Close()

	results, err := externalDBReadRows(rows)
	if err != nil {
		t.Fatalf("externalDBReadRows returned an unexpected error: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected a single row, got %d", len(results))
	}

	row := results[0]
	if row["id"] != int64(42) {
		t.Fatalf("expected id 42, got %#v", row["id"])
	}
	if row["name"] != "alice" {
		t.Fatalf("expected name alice, got %#v", row["name"])
	}
}

func TestExternalDBJSRegistrationAndErrorPath(t *testing.T) {
	vm := otto.New()

	if err := addJSAPIExternalDBExec(context.Background(), vm); err != nil {
		t.Fatalf("addJSAPIExternalDBExec returned an unexpected error: %v", err)
	}
	if err := addJSAPIExternalDBQuery(context.Background(), vm); err != nil {
		t.Fatalf("addJSAPIExternalDBQuery returned an unexpected error: %v", err)
	}

	value, err := vm.Call("externalDBExec", nil, `{"db_type":"unsupported"}`, "SELECT 1")
	if err != nil {
		t.Fatalf("calling externalDBExec failed: %v", err)
	}
	obj := value.Object()
	if obj == nil {
		t.Fatal("expected externalDBExec to return an object")
	}
	messageValue, err := obj.Get("error")
	if err != nil {
		t.Fatalf("expected externalDBExec to return an error object: %v", err)
	}
	message, err := messageValue.ToString()
	if err != nil {
		t.Fatalf("failed to read error message: %v", err)
	}
	if !strings.Contains(message, "unsupported") {
		t.Fatalf("expected an unsupported database error, got %q", message)
	}

	value, err = vm.Call("externalDBQuery", nil, `{"db_type":"unsupported"}`, "SELECT 1")
	if err != nil {
		t.Fatalf("calling externalDBQuery failed: %v", err)
	}
	obj = value.Object()
	if obj == nil {
		t.Fatal("expected externalDBQuery to return an object")
	}
	messageValue, err = obj.Get("error")
	if err != nil {
		t.Fatalf("expected externalDBQuery to return an error object: %v", err)
	}
	message, err = messageValue.ToString()
	if err != nil {
		t.Fatalf("failed to read error message: %v", err)
	}
	if !strings.Contains(message, "unsupported") {
		t.Fatalf("expected an unsupported database error, got %q", message)
	}
}

func TestBuildMongoDBURI(t *testing.T) {
	tests := []struct {
		name     string
		dbType   string
		host     string
		port     int
		user     string
		password string
		want     string
	}{
		{
			name:   "mongodb default port",
			dbType: "mongodb",
			host:   "localhost",
			want:   "mongodb://localhost:27017",
		},
		{
			name:     "mongodb authenticated",
			dbType:   "mongodb",
			host:     "db.example.com",
			port:     27018,
			user:     "crowler",
			password: "secret",
			want:     "mongodb://crowler:secret@db.example.com:27018",
		},
		{
			name:     "mongodb escaped credentials",
			dbType:   "mongodb",
			host:     "db.example.com",
			user:     "user@example.com",
			password: "pa:ss/word",
			want:     "mongodb://user%40example.com:pa%3Ass%2Fword@db.example.com:27017",
		},
		{
			name:   "mongodb srv",
			dbType: "mongodb+srv",
			host:   "cluster.example.com",
			port:   27017,
			want:   "mongodb+srv://cluster.example.com",
		},
		{
			name:     "mongodb srv authenticated",
			dbType:   "mongodb+srv",
			host:     "cluster.example.com",
			port:     27017,
			user:     "crowler",
			password: "secret",
			want:     "mongodb+srv://crowler:secret@cluster.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildMongoDBURI(
				tt.dbType,
				tt.host,
				tt.port,
				tt.user,
				tt.password,
			)
			if err != nil {
				t.Fatalf("buildMongoDBURI returned error: %v", err)
			}

			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}
