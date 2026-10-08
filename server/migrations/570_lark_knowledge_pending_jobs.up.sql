CREATE INDEX CONCURRENTLY IF NOT EXISTS lark_knowledge_pending_jobs ON lark_knowledge_job (installation_id, available_at) WHERE stage NOT IN ('complete', 'quarantined', 'cancelled');
