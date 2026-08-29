-- Name-based model cleanup was unsafe across workspaces. Migration 000031 preserves every
-- referenced configuration while collapsing the control plane to one immutable version table.
SELECT 1;
