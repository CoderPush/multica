-- Source evidence and private processing state are deliberately separate.
-- No rows are created until an operator configures the single-chat policy.
CREATE TABLE lark_knowledge_source (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    installation_id uuid NOT NULL,
    chat_id text NOT NULL,
    message_id text NOT NULL,
    revision text NOT NULL,
    message jsonb NOT NULL,
    body text NOT NULL DEFAULT '',
    extracted text NOT NULL DEFAULT '',
    digest text NOT NULL DEFAULT '',
    available boolean NOT NULL DEFAULT true,
    checked_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE lark_knowledge_event (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    installation_id uuid NOT NULL,
    event_key text NOT NULL,
    payload jsonb NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE lark_knowledge_file (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    installation_id uuid NOT NULL,
    chat_id text NOT NULL,
    digest text NOT NULL,
    filename text NOT NULL,
    original bytea NOT NULL,
    extracted text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE lark_knowledge_job (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    installation_id uuid NOT NULL,
    chat_id text NOT NULL,
    job_key text NOT NULL,
    source_id uuid NOT NULL,
    revision text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('file','question','ack')),
    stage text NOT NULL DEFAULT 'received',
    state jsonb NOT NULL DEFAULT '{}',
    attempts integer NOT NULL DEFAULT 0,
    available_at timestamptz NOT NULL DEFAULT now(),
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE lark_knowledge_state (
    installation_id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    history_through timestamptz,
    last_reconciled_at timestamptz,
    next_reconcile_at timestamptz NOT NULL DEFAULT now(),
    last_error text NOT NULL DEFAULT '',
    model_day date NOT NULL DEFAULT CURRENT_DATE,
    model_calls integer NOT NULL DEFAULT 0
);
CREATE TABLE lark_knowledge_assessment (
    file_id uuid PRIMARY KEY,
    result jsonb NOT NULL,
    issue_id uuid,
    created_at timestamptz NOT NULL DEFAULT now()
);
