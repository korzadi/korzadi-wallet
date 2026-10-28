-- Korzadi — tickets de soporte enviados desde la landing pública.
-- No contiene datos de wallet ni de custodia; solo el mensaje de contacto.

CREATE TABLE IF NOT EXISTS support_tickets (
    id          BIGSERIAL PRIMARY KEY,
    name        VARCHAR(120) NOT NULL,
    email       VARCHAR(255) NOT NULL,
    subject     VARCHAR(120) NOT NULL,
    message     TEXT NOT NULL,
    ip          VARCHAR(64),
    user_agent  TEXT,
    status      VARCHAR(20) NOT NULL DEFAULT 'open'
                    CHECK (status IN ('open','in_progress','closed')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_support_tickets_created
    ON support_tickets (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_support_tickets_status
    ON support_tickets (status, created_at DESC);
