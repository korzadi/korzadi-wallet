BEGIN;

CREATE TABLE p2p_offers (
  id UUID PRIMARY KEY,
  user_id UUID NOT NULL REFERENCES users(id),
  wallet_account_id UUID NOT NULL REFERENCES wallet_accounts(id),
  asset_code VARCHAR(10) NOT NULL,
  fiat_currency VARCHAR(10) NOT NULL,
  price_per_unit NUMERIC(38,18) NOT NULL CHECK (price_per_unit > 0),
  min_amount NUMERIC(38,18) NOT NULL CHECK (min_amount > 0),
  max_amount NUMERIC(38,18) NOT NULL CHECK (max_amount > 0),
  total_amount NUMERIC(38,18) NOT NULL CHECK (total_amount > 0),
  available_amount NUMERIC(38,18) NOT NULL CHECK (available_amount >= 0),
  reserved_amount NUMERIC(38,18) NOT NULL DEFAULT 0 CHECK (reserved_amount >= 0),
  side VARCHAR(10) NOT NULL CHECK (side IN ('buy','sell')),
  payment_methods JSONB NOT NULL DEFAULT '[]',
  country_restrictions JSONB DEFAULT '[]',
  payment_time_limit_minutes INT NOT NULL DEFAULT 30 CHECK (payment_time_limit_minutes > 0),
  instructions TEXT,
  terms TEXT,
  status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active','paused','cancelled','closed')),
  version INT NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_p2p_offers_status_side ON p2p_offers(status, side, asset_code, fiat_currency);
CREATE INDEX idx_p2p_offers_user ON p2p_offers(user_id);

CREATE TABLE p2p_orders (
  id UUID PRIMARY KEY,
  offer_id UUID NOT NULL REFERENCES p2p_offers(id),
  buyer_id UUID NOT NULL REFERENCES users(id),
  seller_id UUID NOT NULL REFERENCES users(id),
  asset_code VARCHAR(10) NOT NULL,
  fiat_currency VARCHAR(10) NOT NULL,
  amount NUMERIC(38,18) NOT NULL CHECK (amount > 0),
  price_per_unit NUMERIC(38,18) NOT NULL CHECK (price_per_unit > 0),
  fiat_amount NUMERIC(38,18) NOT NULL CHECK (fiat_amount > 0),
  commission_rate NUMERIC(38,18) NOT NULL DEFAULT 100 CHECK (commission_rate >= 0),
  commission_amount NUMERIC(38,18) NOT NULL DEFAULT 0 CHECK (commission_amount >= 0),
  commission_fiat NUMERIC(38,18) NOT NULL DEFAULT 0 CHECK (commission_fiat >= 0),
  payment_method VARCHAR(50),
  payment_instructions TEXT,
  status VARCHAR(20) NOT NULL DEFAULT 'created' CHECK (status IN ('created','escrow_locked','payment_marked','released','cancelled','disputed','expired')),
  idempotency_key VARCHAR(128) NOT NULL,
  escrow_reservation_id UUID,
  expires_at TIMESTAMPTZ NOT NULL,
  paid_at TIMESTAMPTZ,
  released_at TIMESTAMPTZ,
  cancelled_at TIMESTAMPTZ,
  dispute_id UUID,
  version INT NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT uq_p2p_orders_idem UNIQUE (idempotency_key)
);

CREATE INDEX idx_p2p_orders_buyer ON p2p_orders(buyer_id);
CREATE INDEX idx_p2p_orders_seller ON p2p_orders(seller_id);
CREATE INDEX idx_p2p_orders_status ON p2p_orders(status);
CREATE INDEX idx_p2p_orders_expires ON p2p_orders(expires_at) WHERE status IN ('created','escrow_locked','payment_marked');

CREATE TABLE p2p_order_events (
  id UUID PRIMARY KEY,
  order_id UUID NOT NULL REFERENCES p2p_orders(id),
  actor_id UUID REFERENCES users(id),
  event_type VARCHAR(50) NOT NULL,
  from_status VARCHAR(20),
  to_status VARCHAR(20),
  metadata JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_p2p_order_events_order ON p2p_order_events(order_id, created_at);

CREATE TABLE p2p_messages (
  id UUID PRIMARY KEY,
  order_id UUID NOT NULL REFERENCES p2p_orders(id),
  sender_id UUID NOT NULL REFERENCES users(id),
  message TEXT,
  attachment_path TEXT,
  attachment_name TEXT,
  attachment_mime VARCHAR(100),
  attachment_size INT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_p2p_messages_order ON p2p_messages(order_id, created_at);

CREATE TABLE p2p_disputes (
  id UUID PRIMARY KEY,
  order_id UUID NOT NULL REFERENCES p2p_orders(id) UNIQUE,
  opened_by UUID NOT NULL REFERENCES users(id),
  reason VARCHAR(100) NOT NULL,
  description TEXT,
  status VARCHAR(20) NOT NULL DEFAULT 'open' CHECK (status IN ('open','under_review','resolved_release','resolved_refund','closed')),
  resolver_id UUID REFERENCES users(id),
  resolution_reason TEXT,
  resolved_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE p2p_dispute_evidence (
  id UUID PRIMARY KEY,
  dispute_id UUID NOT NULL REFERENCES p2p_disputes(id),
  uploader_id UUID NOT NULL REFERENCES users(id),
  attachment_path TEXT NOT NULL,
  attachment_name TEXT NOT NULL,
  attachment_mime VARCHAR(100),
  attachment_size INT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMIT;
