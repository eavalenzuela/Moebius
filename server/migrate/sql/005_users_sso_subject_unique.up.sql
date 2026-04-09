-- Enforce uniqueness of users.sso_subject so the OIDC middleware lookup
-- (`WHERE sso_subject = $1`) cannot return an ambiguous row when more than
-- one user happens to share the same subject — e.g. if a future multi-IdP
-- deployment provisions overlapping subjects, or if an operator inserts
-- duplicates by mistake. Partial index on NOT NULL so users without an SSO
-- mapping can still coexist.
CREATE UNIQUE INDEX users_sso_subject_unique
    ON users(sso_subject)
    WHERE sso_subject IS NOT NULL;
