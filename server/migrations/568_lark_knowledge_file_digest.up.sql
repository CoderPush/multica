CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS lark_knowledge_file_digest ON lark_knowledge_file (installation_id, chat_id, digest);
