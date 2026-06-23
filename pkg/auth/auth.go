package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

type ctxKey struct{}
type Identity struct {
	Subject, Username, Issuer, TokenID string
	Roles, Scopes                      []string
	ExpiresAt                          time.Time
}
type Claims struct {
	Sub, Username, Iss, JTI string
	Roles, Scopes           []string
	Exp, Iat                int64
}
type Store interface {
	QueryRow(query string, args ...interface{}) *sql.Row
	Exec(query string, args ...interface{}) (sql.Result, error)
}

type queryStore interface {
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
}

var ErrUnauthorized = errors.New("unauthorized")

func ContextWithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}
func FromContext(ctx context.Context) (Identity, bool) {
	v, ok := ctx.Value(ctxKey{}).(Identity)
	return v, ok
}
func Enabled(c cfg.AuthConfig) bool {
	return c.Enabled && strings.ToLower(strings.TrimSpace(c.Mode)) != "disabled"
}
func Login(ctx context.Context, db Store, c cfg.AuthConfig, username, password string) (string, error) {
	if !Enabled(c) || !c.Local.Enabled {
		return "", ErrUnauthorized
	}
	var id, hash string
	var disabled bool
	err := db.QueryRow(`SELECT CAST(user_id AS TEXT), password_hash, disabled FROM Users WHERE username = $1 AND deleted_at IS NULL`, username).Scan(&id, &hash, &disabled)
	if err != nil || disabled || !VerifyPassword(password, hash) {
		return "", ErrUnauthorized
	}
	roles, scopes := LoadGrants(db, id)
	return IssueToken(c, Identity{Subject: id, Username: username, Issuer: "crowler", Roles: roles, Scopes: scopes}, "")
}

func LoadGrants(db Store, userID string) ([]string, []string) {
	qs, ok := db.(queryStore)
	if !ok || qs == nil {
		return []string{}, []string{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	roles := []string{}
	if rows, err := qs.QueryContext(ctx, `SELECT r.name FROM AuthRoles r JOIN UserRoles ur ON ur.role_id = r.role_id WHERE ur.user_id = $1 ORDER BY r.name`, userID); err == nil {
		for rows.Next() {
			var role string
			if rows.Scan(&role) == nil {
				roles = append(roles, role)
			}
		}
		_ = rows.Close()
	}
	scopes := []string{}
	if rows, err := qs.QueryContext(ctx, `
		SELECT DISTINCT s.name
		FROM AuthScopes s
		LEFT JOIN UserScopes us ON us.scope_id = s.scope_id AND us.user_id = $1
		LEFT JOIN RoleScopes rs ON rs.scope_id = s.scope_id
		LEFT JOIN UserRoles ur ON ur.role_id = rs.role_id AND ur.user_id = $1
		WHERE us.user_id IS NOT NULL OR ur.user_id IS NOT NULL
		ORDER BY s.name`, userID); err == nil {
		for rows.Next() {
			var scope string
			if rows.Scan(&scope) == nil {
				scopes = append(scopes, scope)
			}
		}
		_ = rows.Close()
	}
	return roles, scopes
}

func IssueToken(c cfg.AuthConfig, id Identity, jti string) (string, error) {
	if jti == "" {
		jti = randomID()
	}
	ttl := time.Duration(c.TokenTTL) * time.Second
	if ttl <= 0 {
		ttl = time.Hour
	}
	iss := c.Issuer
	if iss == "" {
		iss = "crowler"
	}
	cl := Claims{Sub: id.Subject, Username: id.Username, Iss: iss, JTI: jti, Roles: id.Roles, Scopes: id.Scopes, Iat: time.Now().Unix(), Exp: time.Now().Add(ttl).Unix()}
	b, _ := json.Marshal(cl)
	h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	p := base64.RawURLEncoding.EncodeToString(b)
	sig := sign(h+"."+p, c.HMACSecret)
	return h + "." + p + "." + sig, nil
}
func ValidateToken(ctx context.Context, db Store, c cfg.AuthConfig, token string) (Identity, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Identity{}, ErrUnauthorized
	}
	if !hmac.Equal([]byte(sign(parts[0]+"."+parts[1], c.HMACSecret)), []byte(parts[2])) {
		return Identity{}, ErrUnauthorized
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Identity{}, ErrUnauthorized
	}
	var cl Claims
	if json.Unmarshal(payload, &cl) != nil {
		return Identity{}, ErrUnauthorized
	}
	if time.Now().Unix() >= cl.Exp {
		return Identity{}, ErrUnauthorized
	}
	if cl.Iss != "" && c.Issuer != "" && cl.Iss != c.Issuer {
		return Identity{}, ErrUnauthorized
	}
	if db != nil && cl.JTI != "" {
		var revoked bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM AuthRevokedTokens WHERE token_id = $1 AND expires_at > NOW())`, cl.JTI).Scan(&revoked); err == nil && revoked {
			return Identity{}, ErrUnauthorized
		}
	}
	return Identity{Subject: cl.Sub, Username: cl.Username, Issuer: cl.Iss, TokenID: cl.JTI, Roles: cl.Roles, Scopes: cl.Scopes, ExpiresAt: time.Unix(cl.Exp, 0)}, nil
}
func Middleware(c cfg.AuthConfig, db Store, scopes ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !Enabled(c) {
				next.ServeHTTP(w, r)
				return
			}
			tok := bearer(r)
			if tok == "" {
				tok = r.URL.Query().Get("access_token")
			}
			id, err := ValidateToken(r.Context(), db, c, tok)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if !hasScopes(id.Scopes, scopes) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r.WithContext(ContextWithIdentity(r.Context(), id)))
		})
	}
}
func bearer(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}
func hasScopes(got, need []string) bool {
	m := map[string]bool{}
	for _, s := range got {
		m[s] = true
	}
	for _, s := range need {
		if !m[s] && !m["*"] {
			return false
		}
	}
	return true
}
func sign(s, secret string) string {
	if secret == "" {
		secret = "change-me"
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(s))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	sum := argon2.IDKey([]byte(pw), salt, 1, 64*1024, 4, 32)
	return fmt.Sprintf("argon2id$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(sum)), nil
}
func VerifyPassword(pw, hash string) bool {
	p := strings.Split(hash, "$")
	if len(p) != 3 || p[0] != "argon2id" {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(p[1])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(p[2])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, 1, 64*1024, 4, 32)
	return hmac.Equal(got, want)
}
