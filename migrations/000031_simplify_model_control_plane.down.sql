-- Irreversible by design: encrypted provider secrets and the obsolete four-layer hierarchy are
-- intentionally not reconstructed. Restore from a pre-migration backup if rollback is required.
DO $$
BEGIN
    RAISE EXCEPTION 'migration 31 is irreversible; restore a pre-migration backup instead';
END
$$;
