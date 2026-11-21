-- Disposable MCP authorization E2E fixture. Test-only credentials.
-- Always start from a clean mutation state so the fixture can be rerun against
-- a retained database volume without inheriting earlier proposals or audits.
TRUNCATE MCPMutationDispatchAttempts, MCPMutationApprovals, MCPMutationAudit,
         MCPMutationProposals RESTART IDENTITY CASCADE;

INSERT INTO Users (user_id, username, email, password_hash, disabled, deleted_at)
VALUES
    (42, 'mcp-e2e-operator', NULL, 'argon2id$4/q4w0I9qq2HQxbcBBsnoA$tA/CCuqmQ+EhzjlAfVUdw7nCtDKsn0YmuagXzv8bGG8', FALSE, NULL),
    (43, 'mcp-e2e-admin', NULL, 'argon2id$4/q4w0I9qq2HQxbcBBsnoA$tA/CCuqmQ+EhzjlAfVUdw7nCtDKsn0YmuagXzv8bGG8', FALSE, NULL)
ON CONFLICT (user_id) DO UPDATE SET password_hash = EXCLUDED.password_hash, disabled = FALSE, deleted_at = NULL;

INSERT INTO UserScopes (user_id, scope_id)
SELECT u.user_id, s.scope_id
FROM Users u
JOIN AuthScopes s ON s.name = 'events:*'
WHERE u.user_id IN (42, 43)
ON CONFLICT DO NOTHING;

INSERT INTO UserRoles (user_id, role_id)
SELECT 43, role_id FROM AuthRoles WHERE name = 'admin'
ON CONFLICT DO NOTHING;

INSERT INTO Sources (source_id, source_uid, url, name, status, disabled, usr_id)
VALUES (42, 'mcp-e2e-source-42', 'https://mcp-e2e.example/42', 'mcp-e2e', 'new', FALSE, 42)
ON CONFLICT (source_id) DO UPDATE SET usr_id = 42, disabled = FALSE;

INSERT INTO SourceOwnership (source_id, user_id, verified_at, verified_by_user_id, verified_by, verification_method)
VALUES (42, 42, CURRENT_TIMESTAMP, 42, 'mcp-e2e-fixture', 'admin_verification')
ON CONFLICT (source_id) DO UPDATE
SET user_id = 42, verified_at = CURRENT_TIMESTAMP,
    verified_by_user_id = 42, verified_by = 'mcp-e2e-fixture',
    verification_method = 'admin_verification';
