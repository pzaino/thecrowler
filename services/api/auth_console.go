package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	auth "github.com/pzaino/thecrowler/pkg/auth"
	cmn "github.com/pzaino/thecrowler/pkg/common"
)

const (
	roleSuperAdmin = "superadmin"
	roleAdmin      = "admin"
	roleSuperUser  = "superuser"
	roleUser       = "user"
)

type authUserRequest struct {
	ID       int64    `json:"id,omitempty"`
	Username string   `json:"username"`
	Email    string   `json:"email,omitempty"`
	Password string   `json:"password,omitempty"`
	Disabled *bool    `json:"disabled,omitempty"`
	Roles    []string `json:"roles,omitempty"`
	Scopes   []string `json:"scopes,omitempty"`
}

type authUserResponse struct {
	ID       int64    `json:"id"`
	Username string   `json:"username"`
	Email    string   `json:"email,omitempty"`
	Disabled bool     `json:"disabled"`
	Roles    []string `json:"roles,omitempty"`
	Scopes   []string `json:"scopes,omitempty"`
}

type authRoleRequest struct {
	ID          int64    `json:"id,omitempty"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Scopes      []string `json:"scopes,omitempty"`
}
type authScopeRequest struct {
	ID          int64  `json:"id,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}
type authRoleResponse struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Scopes      []string `json:"scopes,omitempty"`
}
type authScopeResponse struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}
type authListResponse[T any] struct {
	Items []T `json:"items"`
}

type authGrantRequest struct {
	Roles, Scopes []string `json:"roles,omitempty"`
}

func withRoleMiddlewares(h http.HandlerFunc, allowed ...string) http.Handler {
	return withMiddlewares(requireRoles(h, allowed...))
}

func requireRoles(next http.Handler, allowed ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !auth.Enabled(config.API.Auth) {
			next.ServeHTTP(w, r)
			return
		}
		id, ok := auth.FromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if hasRole(id.Roles, roleSuperAdmin) || hasRole(id.Roles, allowed...) {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "forbidden", http.StatusForbidden)
	})
}

func hasRole(got []string, allowed ...string) bool {
	m := map[string]bool{}
	for _, r := range got {
		m[strings.ToLower(strings.TrimSpace(r))] = true
	}
	for _, r := range allowed {
		if m[strings.ToLower(strings.TrimSpace(r))] {
			return true
		}
	}
	return false
}

func authID(r *http.Request) (int64, error) {
	raw := r.PathValue("id")
	if raw == "" {
		raw = r.URL.Query().Get("id")
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}

func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func authUsersHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows, err := dbHandler.QueryContext(r.Context(), `SELECT user_id, username, COALESCE(email,''), disabled FROM Users WHERE deleted_at IS NULL ORDER BY user_id`)
		if err != nil {
			handleErrorAndRespond(w, err, nil, "list users", http.StatusInternalServerError, 0)
			return
		}
		defer rows.Close()
		out := []authUserResponse{}
		for rows.Next() {
			var u authUserResponse
			if rows.Scan(&u.ID, &u.Username, &u.Email, &u.Disabled) == nil {
				u.Roles, u.Scopes = loadUserGrantNames(r.Context(), u.ID)
				out = append(out, u)
			}
		}
		handleErrorAndRespond(w, rows.Err(), authListResponse[authUserResponse]{Items: out}, "list users", http.StatusInternalServerError, http.StatusOK)
	case http.MethodPost:
		var req authUserRequest
		if err := decodeJSON(r, &req); err != nil {
			handleErrorAndRespond(w, err, nil, "invalid user request", http.StatusBadRequest, 0)
			return
		}
		req.Username = strings.TrimSpace(req.Username)
		if req.Username == "" || req.Password == "" {
			handleErrorAndRespond(w, errors.New("username and password are required"), nil, "invalid user request", http.StatusBadRequest, 0)
			return
		}
		hash, err := auth.HashPassword(req.Password)
		if err != nil {
			handleErrorAndRespond(w, err, nil, "hash password", http.StatusInternalServerError, 0)
			return
		}
		disabled := false
		if req.Disabled != nil {
			disabled = *req.Disabled
		}
		var id int64
		err = dbHandler.QueryRow(`INSERT INTO Users (username, email, password_hash, disabled) VALUES ($1, NULLIF($2,''), $3, $4) RETURNING user_id`, req.Username, req.Email, hash, disabled).Scan(&id)
		if err == nil {
			err = replaceUserGrants(r.Context(), id, req.Roles, req.Scopes)
		}
		if err != nil {
			handleErrorAndRespond(w, err, nil, "create user", http.StatusInternalServerError, 0)
			return
		}
		roles, scopes := loadUserGrantNames(r.Context(), id)
		handleErrorAndRespond(w, nil, authUserResponse{ID: id, Username: req.Username, Email: req.Email, Disabled: disabled, Roles: roles, Scopes: scopes}, "", http.StatusInternalServerError, http.StatusCreated)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func authUserHandler(w http.ResponseWriter, r *http.Request) {
	id, err := authID(r)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "invalid user id", http.StatusBadRequest, 0)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u, err := getAuthUser(r.Context(), id)
	handleErrorAndRespond(w, err, u, "get user", http.StatusNotFound, http.StatusOK)
}

func authUserUpdateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req authUserRequest
	if err := decodeJSON(r, &req); err != nil {
		handleErrorAndRespond(w, err, nil, "invalid user request", http.StatusBadRequest, 0)
		return
	}
	id := req.ID
	if id <= 0 {
		handleErrorAndRespond(w, errors.New("id is required"), nil, "invalid user id", http.StatusBadRequest, 0)
		return
	}
	var err error
	if req.Password != "" {
		hash, _ := auth.HashPassword(req.Password)
		_, err = dbHandler.ExecContext(r.Context(), `UPDATE Users SET password_hash=$1, credential_version=credential_version+1, last_updated_at=CURRENT_TIMESTAMP WHERE user_id=$2 AND deleted_at IS NULL`, hash, id)
		if err != nil {
			handleErrorAndRespond(w, err, nil, "update password", http.StatusInternalServerError, 0)
			return
		}
	}
	if req.Username != "" || req.Email != "" || req.Disabled != nil {
		disabled := sql.NullBool{}
		if req.Disabled != nil {
			disabled = sql.NullBool{Bool: *req.Disabled, Valid: true}
		}
		_, err = dbHandler.ExecContext(r.Context(), `UPDATE Users SET username=COALESCE(NULLIF($1,''),username), email=COALESCE(NULLIF($2,''),email), disabled=COALESCE($3,disabled), last_updated_at=CURRENT_TIMESTAMP WHERE user_id=$4 AND deleted_at IS NULL`, req.Username, req.Email, disabled, id)
		if err != nil {
			handleErrorAndRespond(w, err, nil, "update user", http.StatusInternalServerError, 0)
			return
		}
	}
	if req.Roles != nil || req.Scopes != nil {
		if err = replaceUserGrants(r.Context(), id, req.Roles, req.Scopes); err != nil {
			handleErrorAndRespond(w, err, nil, "update user grants", http.StatusInternalServerError, 0)
			return
		}
	}
	u, err := getAuthUser(r.Context(), id)
	handleErrorAndRespond(w, err, u, "get user", http.StatusInternalServerError, http.StatusOK)
}

func authUserDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req authUserRequest
	if err := decodeJSON(r, &req); err != nil {
		handleErrorAndRespond(w, err, nil, "invalid user request", http.StatusBadRequest, 0)
		return
	}
	if req.ID <= 0 {
		handleErrorAndRespond(w, errors.New("id is required"), nil, "invalid user id", http.StatusBadRequest, 0)
		return
	}
	_, err := dbHandler.ExecContext(r.Context(), `UPDATE Users SET deleted_at=CURRENT_TIMESTAMP, disabled=TRUE WHERE user_id=$1 AND deleted_at IS NULL`, req.ID)
	handleErrorAndRespond(w, err, ConsoleResponse{Message: "user deleted"}, "delete user", http.StatusInternalServerError, http.StatusOK)
}

func authRolesHandler(w http.ResponseWriter, r *http.Request) {
	authNamedCollectionHandler[authRoleRequest, authRoleResponse](w, r, "AuthRoles", "role_id", func(req authRoleRequest) (string, string, []string) { return req.Name, req.Description, req.Scopes })
}
func authScopesHandler(w http.ResponseWriter, r *http.Request) {
	authNamedCollectionHandler[authScopeRequest, authScopeResponse](w, r, "AuthScopes", "scope_id", func(req authScopeRequest) (string, string, []string) { return req.Name, req.Description, nil })
}

func authNamedCollectionHandler[Q any, R any](w http.ResponseWriter, r *http.Request, table, idCol string, unpack func(Q) (string, string, []string)) {
	switch r.Method {
	case http.MethodGet:
		rows, err := dbHandler.QueryContext(r.Context(), fmt.Sprintf(`SELECT %s, name, COALESCE(description,'') FROM %s ORDER BY name`, idCol, table))
		if err != nil {
			handleErrorAndRespond(w, err, nil, "list auth records", 500, 0)
			return
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var id int64
			var n, d string
			rows.Scan(&id, &n, &d)
			m := map[string]any{"id": id, "name": n, "description": d}
			if table == "AuthRoles" {
				m["scopes"] = loadRoleScopes(r.Context(), id)
			}
			items = append(items, m)
		}
		handleErrorAndRespond(w, rows.Err(), map[string]any{"items": items}, "list auth records", 500, 200)
	case http.MethodPost:
		var req Q
		if err := decodeJSON(r, &req); err != nil {
			handleErrorAndRespond(w, err, nil, "invalid auth request", 400, 0)
			return
		}
		name, desc, scopes := unpack(req)
		var id int64
		err := dbHandler.QueryRow(fmt.Sprintf(`INSERT INTO %s (name, description) VALUES ($1,$2) RETURNING %s`, table, idCol), strings.TrimSpace(name), desc).Scan(&id)
		if err == nil && table == "AuthRoles" {
			err = replaceRoleScopes(r.Context(), id, scopes)
		}
		if err != nil {
			handleErrorAndRespond(w, err, nil, "create auth record", 500, 0)
			return
		}
		handleErrorAndRespond(w, nil, map[string]any{"id": id, "name": name, "description": desc, "scopes": scopes}, "", 500, 201)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func authRoleHandler(w http.ResponseWriter, r *http.Request) {
	authNamedItemHandler(w, r, "AuthRoles", "role_id", true)
}
func authScopeHandler(w http.ResponseWriter, r *http.Request) {
	authNamedItemHandler(w, r, "AuthScopes", "scope_id", false)
}
func authNamedItemHandler(w http.ResponseWriter, r *http.Request, table, idCol string, role bool) {
	id, err := authID(r)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "invalid id", 400, 0)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}

	var item map[string]any
	item, err = getAuthNamedItem(r.Context(), table, idCol, id, role)
	handleErrorAndRespond(w, err, item, "get auth record", 404, 200)
}

func authRoleUpdateHandler(w http.ResponseWriter, r *http.Request) {
	authNamedUpdateHandler[authRoleRequest](w, r, "AuthRoles", "role_id", true)
}
func authScopeUpdateHandler(w http.ResponseWriter, r *http.Request) {
	authNamedUpdateHandler[authScopeRequest](w, r, "AuthScopes", "scope_id", false)
}
func authNamedUpdateHandler[Q interface {
	authRoleRequest | authScopeRequest
}](w http.ResponseWriter, r *http.Request, table, idCol string, role bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req Q
	if err := decodeJSON(r, &req); err != nil {
		handleErrorAndRespond(w, err, nil, "invalid auth request", 400, 0)
		return
	}
	id, name, description, scopes := authNamedRequestFields(req)
	if id <= 0 {
		handleErrorAndRespond(w, errors.New("id is required"), nil, "invalid id", 400, 0)
		return
	}
	_, err := dbHandler.ExecContext(r.Context(), fmt.Sprintf(`UPDATE %s SET name=COALESCE(NULLIF($1,''),name), description=COALESCE($2,description) WHERE %s=$3`, table, idCol), name, description, id)
	if err == nil && role && scopes != nil {
		err = replaceRoleScopes(r.Context(), id, scopes)
	}
	handleErrorAndRespond(w, err, ConsoleResponse{Message: "updated"}, "update auth record", 500, 200)
}

func authRoleDeleteHandler(w http.ResponseWriter, r *http.Request) {
	authNamedDeleteHandler[authRoleRequest](w, r, "AuthRoles", "role_id")
}
func authScopeDeleteHandler(w http.ResponseWriter, r *http.Request) {
	authNamedDeleteHandler[authScopeRequest](w, r, "AuthScopes", "scope_id")
}
func authNamedDeleteHandler[Q interface {
	authRoleRequest | authScopeRequest
}](w http.ResponseWriter, r *http.Request, table, idCol string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req Q
	if err := decodeJSON(r, &req); err != nil {
		handleErrorAndRespond(w, err, nil, "invalid auth request", 400, 0)
		return
	}
	id, _, _, _ := authNamedRequestFields(req)
	if id <= 0 {
		handleErrorAndRespond(w, errors.New("id is required"), nil, "invalid id", 400, 0)
		return
	}
	_, err := dbHandler.ExecContext(r.Context(), fmt.Sprintf(`DELETE FROM %s WHERE %s=$1`, table, idCol), id)
	handleErrorAndRespond(w, err, ConsoleResponse{Message: "deleted"}, "delete auth record", 500, 200)
}

func authNamedRequestFields(req any) (int64, string, string, []string) {
	switch v := req.(type) {
	case authRoleRequest:
		return v.ID, v.Name, v.Description, v.Scopes
	case authScopeRequest:
		return v.ID, v.Name, v.Description, nil
	default:
		return 0, "", "", nil
	}
}

func getAuthNamedItem(ctx context.Context, table, idCol string, id int64, role bool) (map[string]any, error) {
	var outID int64
	var name, description string
	err := dbHandler.QueryRow(fmt.Sprintf(`SELECT %s, name, COALESCE(description,'') FROM %s WHERE %s=$1`, idCol, table, idCol), id).Scan(&outID, &name, &description)
	if err != nil {
		return nil, err
	}
	item := map[string]any{"id": outID, "name": name, "description": description}
	if role {
		item["scopes"] = loadRoleScopes(ctx, id)
	}
	return item, nil
}

func getAuthUser(_ context.Context, id int64) (authUserResponse, error) {
	var u authUserResponse
	err := dbHandler.QueryRow(`SELECT user_id, username, COALESCE(email,''), disabled FROM Users WHERE user_id=$1 AND deleted_at IS NULL`, id).Scan(&u.ID, &u.Username, &u.Email, &u.Disabled)
	if err != nil {
		return u, err
	}
	u.Roles, u.Scopes = loadUserGrantNames(nil, id)
	return u, nil
}
func loadUserGrantNames(_ context.Context, id int64) ([]string, []string) {
	return loadNames(`SELECT r.name FROM AuthRoles r JOIN UserRoles ur ON ur.role_id=r.role_id WHERE ur.user_id=$1 ORDER BY r.name`, id), loadNames(`SELECT s.name FROM AuthScopes s JOIN UserScopes us ON us.scope_id=s.scope_id WHERE us.user_id=$1 ORDER BY s.name`, id)
}
func loadRoleScopes(_ context.Context, id int64) []string {
	return loadNames(`SELECT s.name FROM AuthScopes s JOIN RoleScopes rs ON rs.scope_id=s.scope_id WHERE rs.role_id=$1 ORDER BY s.name`, id)
}
func loadNames(q string, id int64) []string {
	rows, err := dbHandler.QueryContext(context.Background(), q, id)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if rows.Scan(&s) == nil {
			out = append(out, s)
		}
	}
	return out
}

func replaceUserGrants(_ context.Context, userID int64, roles, scopes []string) error {
	_, err := dbHandler.Exec(`DELETE FROM UserRoles WHERE user_id=$1`, userID)
	if err != nil {
		return err
	}
	for _, name := range roles {
		if _, err = dbHandler.Exec(`INSERT INTO UserRoles (user_id, role_id) SELECT $1, role_id FROM AuthRoles WHERE name=$2 ON CONFLICT DO NOTHING`, userID, strings.TrimSpace(name)); err != nil {
			return err
		}
	}
	_, err = dbHandler.Exec(`DELETE FROM UserScopes WHERE user_id=$1`, userID)
	if err != nil {
		return err
	}
	for _, name := range scopes {
		if _, err = dbHandler.Exec(`INSERT INTO UserScopes (user_id, scope_id) SELECT $1, scope_id FROM AuthScopes WHERE name=$2 ON CONFLICT DO NOTHING`, userID, strings.TrimSpace(name)); err != nil {
			return err
		}
	}
	return nil
}
func replaceRoleScopes(_ context.Context, roleID int64, scopes []string) error {
	_, err := dbHandler.Exec(`DELETE FROM RoleScopes WHERE role_id=$1`, roleID)
	if err != nil {
		return err
	}
	for _, name := range scopes {
		if _, err = dbHandler.Exec(`INSERT INTO RoleScopes (role_id, scope_id) SELECT $1, scope_id FROM AuthScopes WHERE name=$2 ON CONFLICT DO NOTHING`, roleID, strings.TrimSpace(name)); err != nil {
			return err
		}
	}
	return nil
}

func registerAuthConsoleRoutes(tags []string) {
	http.Handle("/v1/console/auth/users", withRoleMiddlewares(authUsersHandler, roleSuperAdmin))
	cmn.RegisterAPIRoute("/v1/console/auth/users", []string{"GET", "POST"}, "List and create local authentication users (superadmin)", tags, true, false, 200, authUserRequest{}, nil, authListResponse[authUserResponse]{})

	http.Handle("GET /v1/console/auth/users/{id}", withRoleMiddlewares(authUserHandler, roleSuperAdmin))
	cmn.RegisterAPIRoute("/v1/console/auth/users/{id}", []string{"GET"}, "Read a local authentication user (superadmin)", tags, true, false, 200, nil, StdAPIIDQuery{}, authUserResponse{})
	http.Handle("POST /v1/console/auth/users/update", withRoleMiddlewares(authUserUpdateHandler, roleSuperAdmin))
	cmn.RegisterAPIRoute("/v1/console/auth/users/update", []string{"POST"}, "Update a local authentication user (superadmin)", tags, true, false, 200, authUserRequest{}, nil, authUserResponse{})
	http.Handle("POST /v1/console/auth/users/delete", withRoleMiddlewares(authUserDeleteHandler, roleSuperAdmin))
	cmn.RegisterAPIRoute("/v1/console/auth/users/delete", []string{"POST"}, "Delete a local authentication user (superadmin)", tags, true, false, 200, authUserRequest{}, nil, ConsoleResponse{})
	http.Handle("/v1/console/auth/roles", withRoleMiddlewares(authRolesHandler, roleSuperAdmin))
	cmn.RegisterAPIRoute("/v1/console/auth/roles", []string{"GET", "POST"}, "List and create authorization roles (superadmin)", tags, true, false, 200, authRoleRequest{}, nil, authListResponse[authRoleResponse]{})

	http.Handle("GET /v1/console/auth/roles/{id}", withRoleMiddlewares(authRoleHandler, roleSuperAdmin))
	cmn.RegisterAPIRoute("/v1/console/auth/roles/{id}", []string{"GET"}, "Read an authorization role (superadmin)", tags, true, false, 200, nil, StdAPIIDQuery{}, authRoleResponse{})
	http.Handle("POST /v1/console/auth/roles/update", withRoleMiddlewares(authRoleUpdateHandler, roleSuperAdmin))
	cmn.RegisterAPIRoute("/v1/console/auth/roles/update", []string{"POST"}, "Update an authorization role (superadmin)", tags, true, false, 200, authRoleRequest{}, nil, ConsoleResponse{})
	http.Handle("POST /v1/console/auth/roles/delete", withRoleMiddlewares(authRoleDeleteHandler, roleSuperAdmin))
	cmn.RegisterAPIRoute("/v1/console/auth/roles/delete", []string{"POST"}, "Delete an authorization role (superadmin)", tags, true, false, 200, authRoleRequest{}, nil, ConsoleResponse{})
	http.Handle("/v1/console/auth/scopes", withRoleMiddlewares(authScopesHandler, roleSuperAdmin))
	cmn.RegisterAPIRoute("/v1/console/auth/scopes", []string{"GET", "POST"}, "List and create authorization scopes (superadmin)", tags, true, false, 200, authScopeRequest{}, nil, authListResponse[authScopeResponse]{})

	http.Handle("GET /v1/console/auth/scopes/{id}", withRoleMiddlewares(authScopeHandler, roleSuperAdmin))
	cmn.RegisterAPIRoute("/v1/console/auth/scopes/{id}", []string{"GET"}, "Read an authorization scope (superadmin)", tags, true, false, 200, nil, StdAPIIDQuery{}, authScopeResponse{})
	http.Handle("POST /v1/console/auth/scopes/update", withRoleMiddlewares(authScopeUpdateHandler, roleSuperAdmin))
	cmn.RegisterAPIRoute("/v1/console/auth/scopes/update", []string{"POST"}, "Update an authorization scope (superadmin)", tags, true, false, 200, authScopeRequest{}, nil, ConsoleResponse{})
	http.Handle("POST /v1/console/auth/scopes/delete", withRoleMiddlewares(authScopeDeleteHandler, roleSuperAdmin))
	cmn.RegisterAPIRoute("/v1/console/auth/scopes/delete", []string{"POST"}, "Delete an authorization scope (superadmin)", tags, true, false, 200, authScopeRequest{}, nil, ConsoleResponse{})
}
