CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS lark_knowledge_source_key ON lark_knowledge_source (installation_id, chat_id, message_id);
