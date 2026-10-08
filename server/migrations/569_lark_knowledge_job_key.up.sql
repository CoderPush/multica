CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS lark_knowledge_job_key ON lark_knowledge_job (installation_id, chat_id, job_key);
