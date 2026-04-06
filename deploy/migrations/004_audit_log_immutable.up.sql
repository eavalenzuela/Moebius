-- Enforce append-only invariant on audit_log at the database level.
-- Rules reject UPDATE, DELETE, and TRUNCATE — even if the application
-- has a bug or is compromised via SQL injection, audit entries cannot
-- be modified or removed through normal DML.

CREATE RULE audit_log_no_update AS ON UPDATE TO audit_log DO INSTEAD NOTHING;
CREATE RULE audit_log_no_delete AS ON DELETE TO audit_log DO INSTEAD NOTHING;

-- TRUNCATE cannot be blocked by a rule, so use a BEFORE TRUNCATE trigger.
-- The trigger raises an exception if anyone attempts TRUNCATE on audit_log.
CREATE OR REPLACE FUNCTION prevent_audit_log_truncate()
RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'TRUNCATE on audit_log is prohibited (append-only invariant)';
END;
$$;

CREATE TRIGGER no_truncate_audit_log
BEFORE TRUNCATE ON audit_log
FOR EACH STATEMENT
EXECUTE FUNCTION prevent_audit_log_truncate();
