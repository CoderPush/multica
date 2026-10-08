CREATE INDEX CONCURRENTLY IF NOT EXISTS lark_knowledge_source_search ON lark_knowledge_source USING gin (to_tsvector('simple', body || ' ' || extracted)) WHERE available;
