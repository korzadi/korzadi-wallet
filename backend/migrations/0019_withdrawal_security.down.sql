-- Korzadi Wallet — Fase D (rollback)

DELETE FROM known_destinations WHERE status = 'pending';

ALTER TABLE known_destinations DROP CONSTRAINT IF EXISTS known_destinations_status_check;
ALTER TABLE known_destinations
    ADD CONSTRAINT known_destinations_status_check
    CHECK (status IN ('known', 'blocked', 'allowlisted'));

ALTER TABLE blockchain_withdrawals DROP COLUMN IF EXISTS limit_reservation_id;
