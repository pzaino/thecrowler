-- CROWler authentication and authorization schema.
CREATE TABLE IF NOT EXISTS Users (
    user_id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    username TEXT NOT NULL UNIQUE,
    email TEXT UNIQUE,
    password_hash TEXT,
    external_subject TEXT,
    external_issuer TEXT,
    disabled BOOLEAN NOT NULL DEFAULT FALSE,
    credential_version BIGINT NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS AuthRoles (
    role_id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT
);

CREATE TABLE IF NOT EXISTS AuthScopes (
    scope_id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT
);

CREATE TABLE IF NOT EXISTS UserRoles (
    user_id BIGINT NOT NULL REFERENCES Users(user_id) ON DELETE CASCADE,
    role_id BIGINT NOT NULL REFERENCES AuthRoles(role_id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, role_id)
);

CREATE TABLE IF NOT EXISTS UserScopes (
    user_id BIGINT NOT NULL REFERENCES Users(user_id) ON DELETE CASCADE,
    scope_id BIGINT NOT NULL REFERENCES AuthScopes(scope_id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, scope_id)
);

CREATE TABLE IF NOT EXISTS RoleScopes (
    role_id BIGINT NOT NULL REFERENCES AuthRoles(role_id) ON DELETE CASCADE,
    scope_id BIGINT NOT NULL REFERENCES AuthScopes(scope_id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, scope_id)
);

CREATE TABLE IF NOT EXISTS AuthRevokedTokens (
    token_id TEXT PRIMARY KEY,
    user_id BIGINT REFERENCES Users(user_id) ON DELETE SET NULL,
    revoked_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMPTZ NOT NULL,
    reason TEXT
);

CREATE INDEX IF NOT EXISTS idx_auth_revoked_tokens_expires_at ON AuthRevokedTokens(expires_at);
CREATE INDEX IF NOT EXISTS idx_users_external_identity ON Users(external_issuer, external_subject);

-- Default API authorization roles and scopes.
INSERT INTO AuthRoles (name, description) VALUES
    ('superadmin', 'Can administer all CROWler API, events, console, account, role, and scope endpoints.'),
    ('admin', 'Can access all services/events endpoints, all services/api endpoints, and console endpoints except account administration.'),
    ('superuser', 'Can access all services/events endpoints, all services/api non-console endpoints, and only the add-source console endpoint.'),
    ('user', 'Can access services/api non-console endpoints only.')
ON CONFLICT (name) DO UPDATE SET description = EXCLUDED.description;

INSERT INTO AuthScopes (name, description) VALUES
    ('api:*', 'Access all services/api endpoints.'),
    ('api:read', 'Access non-console services/api endpoints.'),
    ('api:console', 'Access services/api console endpoints.'),
    ('api:console:source:add', 'Add new sources through the services/api console.'),
    ('api:console:accounts', 'Administer local authentication and authorization records.'),
    ('events:*', 'Access all services/events endpoints.')
ON CONFLICT (name) DO UPDATE SET description = EXCLUDED.description;

INSERT INTO RoleScopes (role_id, scope_id)
SELECT r.role_id, s.scope_id
FROM AuthRoles r
JOIN AuthScopes s ON (
    (r.name = 'superadmin' AND s.name IN ('api:*', 'api:console', 'api:console:accounts', 'api:console:source:add', 'events:*')) OR
    (r.name = 'admin' AND s.name IN ('api:*', 'api:console', 'api:console:source:add', 'events:*')) OR
    (r.name = 'superuser' AND s.name IN ('api:read', 'api:console:source:add', 'events:*')) OR
    (r.name = 'user' AND s.name IN ('api:read'))
)
ON CONFLICT DO NOTHING;
