-- Enforce append-only invariant on audit_log at the database level.
-- Rules reject UPDATE, DELETE, and TRUNCATE — even if the application
-- has a bug or is compromised via SQL injection, audit entries cannot
-- be modified or removed through normal DML.

CREATE RULE audit_log_no_update AS ON UPDATE TO audit_log DO INSTEAD NOTHING;
CREATE RULE audit_log_no_delete AS ON DELETE TO audit_log DO INSTEAD NOTHING;

-- TRUNCATE cannot be blocked by a rule, so use an event trigger function.
-- The trigger raises an exception if anyone attempts TRUNCATE on audit_log.
CREATE OR REPLACE FUNCTION prevent_audit_log_truncate()
RETURNS event_trigger
LANGUAGE plpgsql AS $$
DECLARE
    obj record;
BEGIN
    FOR obj IN SELECT * FROM pg_event_trigger_ddl_commands()
    LOOP
        IF obj.object_identity = 'public.audit_log' THEN
            RAISE EXCEPTION 'TRUNCATE on audit_log is prohibited (append-only invariant)';
        END IF;
    END LOOP;
END;
$$;

CREATE EVENT TRIGGER no_truncate_audit_log
ON ddl_command_end
WHEN TAG IN ('TRUNCATE')
EXECUTE FUNCTION prevent_audit_log_truncate();
