// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package plugin provides the core plugin functionality for the application.
package plugin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/robertkrimen/otto"

	cmn "github.com/pzaino/thecrowler/pkg/common"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	gosnowflake "github.com/snowflakedb/gosnowflake/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const snowflakeDBMS = "snowflake"

func externalConfigString(config map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := config[key]; ok && value != nil {
			s := strings.TrimSpace(fmt.Sprintf("%v", value))
			if s != "" {
				return s
			}
		}
	}
	return ""
}

func externalConfigInt(config map[string]interface{}, key string) int {
	value, ok := config[key]
	if !ok || value == nil {
		return 0
	}

	switch v := value.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case uint:
		return int(v)
	case uint32:
		return int(v)
	case uint64:
		return int(v)
	case float32:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, err := v.Int64()
		if err == nil {
			return int(n)
		}
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return n
		}
	}

	return 0
}

func externalDBName(config map[string]interface{}) string {
	// db_name is canonical.
	// dbname preserves compatibility with existing plugin documentation.
	// database is convenient for Snowflake terminology.
	return externalConfigString(config, "db_name", "dbname", "database")
}

func externalDBCallContext(
	parent context.Context,
	config map[string]interface{},
) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}

	timeoutSeconds := externalConfigInt(config, "timeout")
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}

	// Do not allow an external DB operation to sit around indefinitely.
	if timeoutSeconds > 3600 {
		timeoutSeconds = 3600
	}

	return context.WithTimeout(
		parent,
		time.Duration(timeoutSeconds)*time.Second,
	)
}

func buildSnowflakeConfig(
	config map[string]interface{},
) (*gosnowflake.Config, error) {
	account := externalConfigString(config, "account", "account_identifier")
	if account == "" {
		return nil, fmt.Errorf("Snowflake account is required")
	}

	user := externalConfigString(config, "user", "username")
	if user == "" {
		return nil, fmt.Errorf("Snowflake user is required")
	}

	sfConfig := &gosnowflake.Config{
		Account:   account,
		User:      user,
		Password:  externalConfigString(config, "password"),
		Database:  externalDBName(config),
		Schema:    externalConfigString(config, "schema"),
		Warehouse: externalConfigString(config, "warehouse"),
		Role:      externalConfigString(config, "role"),
	}

	if host := externalConfigString(config, "host"); host != "" {
		sfConfig.Host = host
	}

	if port := externalConfigInt(config, "port"); port > 0 {
		sfConfig.Port = port
	}

	authenticator := strings.ToLower(
		externalConfigString(config, "authenticator"),
	)

	switch authenticator {
	case "", "snowflake", "password":
		if sfConfig.Password == "" {
			return nil, fmt.Errorf(
				"Snowflake password is required for password authentication",
			)
		}
		sfConfig.Authenticator = gosnowflake.AuthTypeSnowflake

	case "username_password_mfa":
		if sfConfig.Password == "" {
			return nil, fmt.Errorf(
				"Snowflake password is required for username/password MFA",
			)
		}
		sfConfig.Authenticator = gosnowflake.AuthTypeUsernamePasswordMFA

	case "oauth":
		token := externalConfigString(config, "token")
		if token == "" {
			return nil, fmt.Errorf(
				"Snowflake token is required for OAuth authentication",
			)
		}
		sfConfig.Authenticator = gosnowflake.AuthTypeOAuth
		sfConfig.Token = token

	case "programmatic_access_token", "pat":
		token := externalConfigString(config, "token")
		tokenFile := externalConfigString(config, "token_file_path")

		if token == "" && tokenFile == "" {
			return nil, fmt.Errorf(
				"Snowflake token or token_file_path is required for PAT authentication",
			)
		}

		sfConfig.Authenticator = gosnowflake.AuthTypePat
		sfConfig.Token = token
		sfConfig.TokenFilePath = tokenFile

	default:
		return nil, fmt.Errorf(
			"unsupported Snowflake authenticator %q",
			authenticator,
		)
	}

	return sfConfig, nil
}

func openSnowflakeDB(config map[string]interface{}) (*sql.DB, error) {
	sfConfig, err := buildSnowflakeConfig(config)
	if err != nil {
		return nil, err
	}

	// Prefer Config + Connector over hand-building a DSN. Among other things,
	// this avoids having to perform our own escaping of credentials and
	// connection parameters.
	connector := gosnowflake.NewConnector(
		gosnowflake.SnowflakeDriver{},
		*sfConfig,
	)

	return sql.OpenDB(connector), nil
}

func openExternalSQLDB(config map[string]interface{}) (*sql.DB, error) {
	dbType := strings.ToLower(
		externalConfigString(config, "db_type"),
	)
	if dbType == "" {
		dbType = postgresDBMS
	}

	host := externalConfigString(config, "host")
	user := externalConfigString(config, "user", "username")
	password := externalConfigString(config, "password")
	dbname := externalDBName(config)
	port := externalConfigInt(config, "port")

	switch dbType {
	case postgresDBMS:
		if host == "" {
			host = "localhost"
		}
		if port == 0 {
			port = 5432
		}

		sslmode := externalConfigString(config, "sslmode")
		if sslmode == "" {
			sslmode = "disable"
		}

		dsn := fmt.Sprintf(
			"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
			host,
			port,
			user,
			password,
			dbname,
			sslmode,
		)

		return sql.Open(postgresDBMS, dsn)

	case mysqlDBMS:
		if host == "" {
			host = "localhost"
		}
		if port == 0 {
			port = 3306
		}

		dsn := fmt.Sprintf(
			"%s:%s@tcp(%s:%d)/%s",
			user,
			password,
			host,
			port,
			dbname,
		)

		return sql.Open(mysqlDBMS, dsn)

	case sqliteDBMS:
		if dbname == "" {
			return nil, fmt.Errorf("SQLite database path is required")
		}
		return sql.Open("sqlite3", dbname)

	case snowflakeDBMS:
		return openSnowflakeDB(config)

	default:
		return nil, fmt.Errorf(
			"unsupported SQL database type: %s",
			dbType,
		)
	}
}

func externalDBSQLArgs(value otto.Value) ([]interface{}, error) {
	if !value.IsDefined() || value.IsNull() {
		return nil, nil
	}

	exported, err := value.Export()
	if err != nil {
		return nil, fmt.Errorf("exporting SQL arguments: %w", err)
	}

	args, ok := exported.([]interface{})
	if !ok {
		return nil, fmt.Errorf(
			"SQL arguments must be provided as an array",
		)
	}

	return args, nil
}

func externalDBReadRows(
	rows *sql.Rows,
) ([]map[string]interface{}, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	results := make([]map[string]interface{}, 0)

	for rows.Next() {
		values := make([]interface{}, len(columns))
		pointers := make([]interface{}, len(columns))

		for i := range values {
			pointers[i] = &values[i]
		}

		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}

		row := make(map[string]interface{}, len(columns))

		for i, column := range columns {
			value := values[i]

			if b, ok := value.([]byte); ok {
				row[column] = string(b)
			} else {
				row[column] = value
			}
		}

		results = append(results, row)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return results, nil
}

func addJSAPIExternalDBExec(
	pluginCtx context.Context,
	vm *otto.Otto,
) error {
	return vm.Set(
		"externalDBExec",
		func(call otto.FunctionCall) otto.Value {
			configStr, err := call.Argument(0).ToString()
			if err != nil {
				return returnError(
					vm,
					fmt.Sprintf("invalid external DB configuration: %v", err),
				)
			}

			statement, err := call.Argument(1).ToString()
			if err != nil || strings.TrimSpace(statement) == "" {
				return returnError(
					vm,
					"externalDBExec requires a SQL statement",
				)
			}

			var config map[string]interface{}
			if err := json.Unmarshal(
				[]byte(configStr),
				&config,
			); err != nil {
				return returnError(
					vm,
					fmt.Sprintf("invalid external DB configuration: %v", err),
				)
			}

			args, err := externalDBSQLArgs(call.Argument(2))
			if err != nil {
				return returnError(vm, err.Error())
			}

			db, err := openExternalSQLDB(config)
			if err != nil {
				return returnError(
					vm,
					fmt.Sprintf("opening external database: %v", err),
				)
			}
			defer db.Close() //nolint:errcheck

			ctx, cancel := externalDBCallContext(
				pluginCtx,
				config,
			)
			defer cancel()

			execResult, err := db.ExecContext(
				ctx,
				statement,
				args...,
			)
			if err != nil {
				return returnError(
					vm,
					fmt.Sprintf("executing external SQL statement: %v", err),
				)
			}

			var rowsAffected interface{}
			if n, err := execResult.RowsAffected(); err == nil {
				rowsAffected = n
			}

			result, err := vm.ToValue(
				map[string]interface{}{
					"status":        "success",
					"rows_affected": rowsAffected,
				},
			)
			if err != nil {
				return returnError(
					vm,
					fmt.Sprintf("converting SQL result: %v", err),
				)
			}

			return result
		},
	)
}

func buildMongoDBURI(
	dbType string,
	host string,
	port int,
	user string,
	password string,
) (string, error) {
	dbType = strings.ToLower(strings.TrimSpace(dbType))
	host = strings.TrimSpace(host)

	if host == "" {
		return "", fmt.Errorf("MongoDB host is required")
	}

	var auth string
	if user != "" && password != "" {
		auth = url.UserPassword(user, password).String() + "@"
	}

	switch dbType {
	case "mongodb":
		if port <= 0 {
			port = 27017
		}

		return fmt.Sprintf(
			"mongodb://%s%s:%d",
			auth,
			host,
			port,
		), nil

	case "mongodb+srv":
		return fmt.Sprintf(
			"mongodb+srv://%s%s",
			auth,
			host,
		), nil

	default:
		return "", fmt.Errorf(
			"unsupported MongoDB connection type: %s",
			dbType,
		)
	}
}

/* example use with SnowFlake:
// @name: push_to_snowflake
// @description: Pushes collected CROWler data to Snowflake.
// @type: engine_plugin
// @version: 1.0.0

var sf = {
    db_type: "snowflake",
    account: "myorg-myaccount",
    user: "CROWLER_SERVICE",
    password: "SECRET_FROM_RUNTIME",
    db_name: "CROWLER",
    schema: "CUSTOM_INGEST",
    warehouse: "CROWLER_WH",
    role: "CROWLER_INGEST",
    authenticator: "snowflake",
    timeout: 30
};

Or with a Snowflake Programmatic Access Token:

{
    db_type: "snowflake",
    account: "myorg-myaccount",
    user: "CROWLER_SERVICE",
    token: "SECRET_FROM_RUNTIME",
    db_name: "CROWLER",
    schema: "CUSTOM_INGEST",
    warehouse: "CROWLER_WH",
    role: "CROWLER_INGEST",
    authenticator: "programmatic_access_token",
    timeout: 30
}

if (!sf) {
    result = {
        status: "error",
        error: "Missing Snowflake configuration"
    };
} else {
    var payload = JSON.stringify(params.json_data);

    var statement =
        "INSERT INTO CROWLER_RAW_EVENTS " +
        "(SOURCE_ID, EVENT_TYPE, EVENT_TIMESTAMP, PAYLOAD) " +
        "SELECT ?, ?, CURRENT_TIMESTAMP(), PARSE_JSON(?)";

    result = externalDBExec(
        JSON.stringify(sf),
        statement,
        [
            params.source_id || 0,
            params.event_type || "crawl_completed",
            payload
        ]
    );
}
*/

/* example usage for externalDBQuery in JS:

// Postgres and MySQL example (replace db_type with "mysql" for MySQL)
let config = JSON.stringify({
	db_type: "postgres",
	host: "localhost",
	port: 5432,
	user: "dbUser",
	password: "dbPassword",
	dbname: "dbName"
});

let result = externalDBQuery(config, "SELECT * FROM users");
console.log(result);

// SQLite example
let config = JSON.stringify({
	db_type: "sqlite",
	dbname: "/path/to/db.sqlite"
});

let result = externalDBQuery(config, "SELECT * FROM users");
console.log(result);

// MongoDB example
let config = JSON.stringify({
	db_type: "mongodb",
	host: "localhost",
	port: 27017,
	user: "dbUser",
	password: "dbPassword",
	dbname: "dbName"
});

let query = JSON.stringify({
	collection: "users",
	action: "find",
	filter: { name: "John", age: { "$gt": 25 }, date: { "$gte": ISODate("2021-01-01") } },
});

let result = externalDBQuery(config, query);
console.log(result);
*/

// addJSAPIExternalDBQuery adds a new function "externalDBQuery" to the Otto VM,
// allowing engine plugins to query external databases (PostgreSQL, MySQL, SQLite,
// MongoDB, Neo4J) without interfering with the built-in runQuery function.
func addJSAPIExternalDBQuery(
	pluginCtx context.Context,
	vm *otto.Otto,
) error {
	// Register externalDBQuery to the JS API.
	// Usage in JavaScript:
	//    var config = JSON.stringify({
	//         db_type: "postgres",// required
	//         host: "127.0.0.1",  // required for all but sqlite
	//         port: 5432,         // optional
	//         user: "dbuser",     // optional
	//         password: "secret", // optional
	//         dbname: "mydb",     // required
	//         sslmode: "disable"  // optional
	//    });
	//    var result = externalDBQuery(config, "SELECT * FROM mytable");
	//    console.log(result);
	return vm.Set("externalDBQuery", func(call otto.FunctionCall) otto.Value {
		// Get configuration and query from arguments.
		configStr, err := call.Argument(0).ToString()
		if err != nil {
			return otto.UndefinedValue()
		}
		query, err := call.Argument(1).ToString()
		if err != nil {
			return otto.UndefinedValue()
		}

		// Parse configuration JSON.
		var config map[string]interface{}
		if err := json.Unmarshal([]byte(configStr), &config); err != nil {
			return otto.UndefinedValue()
		}

		// Determine the database type.
		dbTypeRaw, ok := config["db_type"]
		if !ok {
			// Default to postgres if not specified, or you may choose to error out.
			dbTypeRaw = postgresDBMS
		}
		dbType := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", dbTypeRaw)))

		// Extract connection parameters.
		var host string
		if config["host"] != nil {
			host = strings.TrimSpace(fmt.Sprintf("%v", config["host"]))
		} else {
			host = "localhost"
		}
		var port int
		if config["port"] != nil {
			portF64, _ := config["port"].(float64)
			port = int(portF64)
		} else {
			port = 0
		}
		var user string
		if config["user"] != nil {
			user = strings.TrimSpace(fmt.Sprintf("%v", config["user"]))
		}
		if user == "" {
			if config["username"] != nil {
				user = strings.TrimSpace(fmt.Sprintf("%v", config["username"]))
			}
		}
		var password string
		if config["password"] != nil {
			password = strings.TrimSpace(fmt.Sprintf("%v", config["password"]))
		}
		dbname := externalDBName(config)

		// Switch among supported databases.
		switch dbType {
		// Relational databases:
		case postgresDBMS, mysqlDBMS, sqliteDBMS, snowflakeDBMS:
			db, err := openExternalSQLDB(config)
			if err != nil {
				return returnError(
					vm,
					fmt.Sprintf(
						"Error attempting to connect to '%s' database: %v",
						dbType,
						err,
					),
				)
			}
			defer db.Close() //nolint:errcheck

			args, err := externalDBSQLArgs(call.Argument(2))
			if err != nil {
				return returnError(vm, err.Error())
			}

			ctx, cancel := externalDBCallContext(
				pluginCtx,
				config,
			)
			defer cancel()

			rows, err := db.QueryContext(
				ctx,
				query,
				args...,
			)
			if err != nil {
				return returnError(
					vm,
					fmt.Sprintf(
						"Error querying '%s' database: %v",
						dbType,
						err,
					),
				)
			}
			defer rows.Close() //nolint:errcheck

			results, err := externalDBReadRows(rows)
			if err != nil {
				return returnError(
					vm,
					fmt.Sprintf(
						"Error processing '%s' database results: %v",
						dbType,
						err,
					),
				)
			}

			jsResult, err := vm.ToValue(results)
			if err != nil {
				return returnError(
					vm,
					fmt.Sprintf(
						"Error converting '%s' database results: %v",
						dbType,
						err,
					),
				)
			}

			return jsResult

		// MongoDB support.
		case "mongodb", "mongodb+srv":
			const mongoSelect = "find"

			// Build MongoDB URI. If authentication is needed:
			mongoURI, err := buildMongoDBURI(
				dbType,
				host,
				port,
				user,
				password,
			)
			if err != nil {
				return returnError(vm, err.Error())
			}

			ctx, cancel := context.WithTimeout(
				context.Background(),
				10*time.Second,
			)
			defer cancel()

			client, err := mongo.Connect(
				ctx,
				options.Client().ApplyURI(mongoURI),
			)
			if err != nil {
				return returnError(
					vm,
					fmt.Sprintf(
						"Error attempting to connect to '%s' db: %v",
						dbname,
						err,
					),
				)
			}
			defer client.Disconnect(ctx) //nolint:errcheck // We can't check error here it's a defer

			// Process the query object: { action: "find", filter: { name: "John" } }
			var queryJSON map[string]interface{}
			if err := json.Unmarshal([]byte(query), &queryJSON); err != nil {
				return returnError(vm, fmt.Sprintf("Error attempting to use '%s' db: %v", dbname, err))
			}

			// Extract collection name from the query object (Required field).
			var collectionName string
			noCollection := false
			if queryJSON["collection"] != nil {
				collectionName = strings.TrimSpace(fmt.Sprintf("%v", queryJSON["collection"]))
				if collectionName == "" {
					noCollection = true
				}
			} else {
				noCollection = true
			}
			if noCollection {
				return returnError(vm, fmt.Sprintf("MongoDB database '%s' requires a non-empty 'collection' field", dbname))
			}
			coll := client.Database(dbname).Collection(collectionName)

			// Extract requested action and filter.
			actionRaw, ok := queryJSON["action"]
			if !ok || actionRaw == nil {
				// If the action is not provided, default to a find action.
				actionRaw = mongoSelect
			}
			actionStr := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", actionRaw)))

			var jsResult otto.Value
			switch actionStr {
			case mongoSelect: // find
				// Extract the filter from the query object.
				if queryJSON["filter"] == nil {
					// If the filter is not provided, default to an empty filter.
					queryJSON["filter"] = map[string]interface{}{}
				}

				filter, err := externalMongoFilter(queryJSON["filter"])
				if err != nil {
					return returnError(vm, err.Error())
				}
				cmn.DebugMsg(cmn.DbgLvlDebug5, "[MONGODB] MongoDB filter BSON Object: %v", filter)
				cursor, err := coll.Find(ctx, filter)
				if err != nil {
					return returnError(vm, fmt.Sprintf("Error attempting to use '%s' db: %v", dbname, err))
				}
				defer cursor.Close(ctx) // nolint:errcheck // We can't check error here it's a defer

				var results []bson.M
				if err = cursor.All(ctx, &results); err != nil {
					return returnError(vm, fmt.Sprintf("Error attempting to use cursor on '%s' db: %v", dbname, err))
				}
				jsResult, err = vm.ToValue(results)
				if err != nil {
					return returnError(vm, fmt.Sprintf("Error attempting to convert MongoDB results to a JS object: %v", err))
				}

			case "insertone": // insertOne
				if queryJSON["document"] == nil {
					return returnError(vm, "Missing 'document' field for insertOne operation")
				}
				doc, ok := queryJSON["document"].(map[string]interface{})
				if !ok {
					return returnError(vm, "Invalid format for 'document' field in insertOne operation")
				}
				result, err := coll.InsertOne(ctx, doc)
				if err != nil {
					return returnError(vm, fmt.Sprintf("Error inserting document: %v", err))
				}
				jsResult, _ = vm.ToValue(map[string]interface{}{"inserted_id": result.InsertedID})

			case "insertmany": // insertMany
				if queryJSON["documents"] == nil {
					return returnError(vm, "Missing 'documents' field for insertMany operation")
				}
				docs, ok := queryJSON["documents"].([]interface{})
				if !ok {
					return returnError(vm, "Invalid format for 'documents' field in insertMany operation")
				}
				result, err := coll.InsertMany(ctx, docs)
				if err != nil {
					return returnError(vm, fmt.Sprintf("Error inserting multiple documents: %v", err))
				}
				jsResult, _ = vm.ToValue(map[string]interface{}{"inserted_ids": result.InsertedIDs})

			case "updateone": // updateOne
				if queryJSON["filter"] == nil || queryJSON["update"] == nil {
					return returnError(vm, "Missing 'filter' or 'update' field for updateOne operation")
				}
				filter, err := externalMongoFilter(queryJSON["filter"])
				if err != nil {
					return returnError(
						vm,
						fmt.Sprintf(
							"Invalid filter for updateOne operation: %v",
							err,
						),
					)
				}
				update, ok := queryJSON["update"].(map[string]interface{})
				if !ok {
					return returnError(vm, "Invalid format for 'update' field in updateOne operation")
				}
				result, err := coll.UpdateOne(ctx, filter, bson.M{"$set": update})
				if err != nil {
					return returnError(vm, fmt.Sprintf("Error updating document: %v", err))
				}
				jsResult, _ = vm.ToValue(map[string]interface{}{
					"matched_count":  result.MatchedCount,
					"modified_count": result.ModifiedCount,
				})

			case "updatemany": // updateMany
				if queryJSON["filter"] == nil || queryJSON["update"] == nil {
					return returnError(vm, "Missing 'filter' or 'update' field for updateMany operation")
				}
				filter, err := externalMongoFilter(queryJSON["filter"])
				if err != nil {
					return returnError(
						vm,
						fmt.Sprintf(
							"Invalid filter for updateMany operation: %v",
							err,
						),
					)
				}
				update, ok := queryJSON["update"].(map[string]interface{})
				if !ok {
					return returnError(vm, "Invalid format for 'update' field in updateMany operation")
				}
				result, err := coll.UpdateMany(ctx, filter, bson.M{"$set": update})
				if err != nil {
					return returnError(vm, fmt.Sprintf("Error updating multiple documents: %v", err))
				}
				jsResult, _ = vm.ToValue(map[string]interface{}{
					"matched_count":  result.MatchedCount,
					"modified_count": result.ModifiedCount,
				})

			case "deleteone": // deleteOne
				if queryJSON["filter"] == nil {
					return returnError(vm, "Missing 'filter' field for deleteOne operation")
				}
				filter, err := externalMongoFilter(queryJSON["filter"])
				if err != nil {
					return returnError(
						vm,
						fmt.Sprintf(
							"Invalid filter for deleteOne operation: %v",
							err,
						),
					)
				}
				result, err := coll.DeleteOne(ctx, filter)
				if err != nil {
					return returnError(vm, fmt.Sprintf("Error deleting document: %v", err))
				}
				jsResult, _ = vm.ToValue(map[string]interface{}{"deleted_count": result.DeletedCount})

			case "deletemany": // deleteMany
				if queryJSON["filter"] == nil {
					return returnError(vm, "Missing 'filter' field for deleteMany operation")
				}
				filter, err := externalMongoFilter(queryJSON["filter"])
				if err != nil {
					return returnError(
						vm,
						fmt.Sprintf(
							"Invalid filter for deleteMany operation: %v",
							err,
						),
					)
				}
				result, err := coll.DeleteMany(ctx, filter)
				if err != nil {
					return returnError(vm, fmt.Sprintf("Error deleting multiple documents: %v", err))
				}
				jsResult, _ = vm.ToValue(map[string]interface{}{"deleted_count": result.DeletedCount})

			default:
				return returnError(vm, fmt.Sprintf("Unsupported action in the query object: '%s'", actionStr))
			}
			return jsResult

		// Neo4J support using NewDriverWithContext.
		case "neo4j":
			if port == 0 {
				port = 7687
			}
			// Use the neo4j:// protocol (or bolt:// if needed)
			uri := fmt.Sprintf("neo4j://%s:%d", host, port)
			ctx := context.Background()
			driver, err := neo4j.NewDriverWithContext(uri, neo4j.BasicAuth(user, password, ""), nil)
			if err != nil {
				return returnError(vm, fmt.Sprintf("Error attempting to connect to neo4j '%s' db: %v", dbname, err))
			}
			defer driver.Close(ctx) // nolint:errcheck // We can't check error here it's a defer

			// Create a session.
			session := driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
			defer session.Close(ctx) // nolint:errcheck // We can't check error here it's a defer

			// Execute the Cypher query.
			records, err := session.Run(ctx, query, nil)
			if err != nil {
				return returnError(vm, fmt.Sprintf("Error executing Cypher query on '%s' db: %v", dbname, err))
			}

			var results []map[string]interface{}
			for records.Next(ctx) {
				record := records.Record()
				recMap := make(map[string]interface{})
				for _, key := range record.Keys {
					if value, found := record.Get(key); found {
						recMap[key] = value
					}
				}
				results = append(results, recMap)
			}
			if err = records.Err(); err != nil {
				return returnError(vm, fmt.Sprintf("Error processing Cypher query results on '%s' db: %v", dbname, err))
			}
			jsResult, err := vm.ToValue(results)
			if err != nil {
				return returnError(vm, fmt.Sprintf("Error converting Neo4J results to a JS object: %v", err))
			}
			return jsResult

		default:
			return returnError(vm, fmt.Sprintf("Unsupported database type: %s", dbType))
		}
	})
}

// Recursive function to convert $date fields into bson "DateTime"
// this is a support function for the MongoDB support in externalDBQuery
func convertBsonDatesRecursive(obj interface{}) interface{} {
	switch v := obj.(type) {
	case map[string]interface{}:
		// Convert a direct "$date" field into BSON primitive.DateTime
		if dateStr, exists := v["$date"]; exists {
			if dateISO, ok := dateStr.(string); ok {
				parsedTime, err := time.Parse(time.RFC3339, dateISO)
				if err == nil {
					return primitive.DateTime(parsedTime.UnixMilli()) // Convert to BSON DateTime
				}
			}
		}

		// Check whether this document contains MongoDB operators.
		// If it does, represent the entire document as bson.D so that
		// operators remain ordered without dropping ordinary fields.
		hasOperator := false
		for key := range v {
			if strings.HasPrefix(key, "$") {
				hasOperator = true
				break
			}
		}

		if hasOperator {
			bsonDoc := bson.D{}

			for key, val := range v {
				bsonDoc = append(
					bsonDoc,
					bson.E{
						Key:   key,
						Value: convertBsonDatesRecursive(val),
					},
				)
			}

			return bsonDoc
		}

		bsonMap := bson.M{}
		for key, val := range v {
			bsonMap[key] = convertBsonDatesRecursive(val)
		}

		return bsonMap
	case []interface{}:
		// Process arrays
		for i, val := range v {
			v[i] = convertBsonDatesRecursive(val)
		}
	}
	return obj
}

func externalMongoFilter(value interface{}) (interface{}, error) {
	filterMap, ok := value.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf(
			"invalid format for 'filter' field: expected an object",
		)
	}

	filter := convertBsonDatesRecursive(filterMap)

	switch filter.(type) {
	case bson.M, bson.D:
		return filter, nil
	default:
		return nil, fmt.Errorf(
			"invalid MongoDB filter after BSON conversion: %T",
			filter,
		)
	}
}
