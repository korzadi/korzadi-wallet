-- Korzadi Wallet — Fase D: controles de seguridad en los retiros.
--
-- 1) blockchain_withdrawals.limit_reservation_id: la reserva de límites (Bloque 8)
--    que respalda cada retiro, para confirmarla al completarse o liberarla si falla/se cancela.
-- 2) known_destinations admite el estado 'pending' (dirección vista por primera vez,
--    en período de espera) además de known/blocked/allowlisted.
-- 3) Backfill: las direcciones a las que ya se completó un retiro pasan a 'known',
--    para que los usuarios actuales no queden en espera con direcciones que ya usaron.

ALTER TABLE blockchain_withdrawals ADD COLUMN IF NOT EXISTS limit_reservation_id UUID;

DO $$
DECLARE c text;
BEGIN
    FOR c IN
        SELECT conname FROM pg_constraint
        WHERE conrelid = 'known_destinations'::regclass AND contype = 'c'
          AND pg_get_constraintdef(oid) ILIKE '%status%'
    LOOP
        EXECUTE format('ALTER TABLE known_destinations DROP CONSTRAINT %I', c);
    END LOOP;
END $$;

ALTER TABLE known_destinations
    ADD CONSTRAINT known_destinations_status_check
    CHECK (status IN ('pending', 'known', 'blocked', 'allowlisted'));

-- sha256 hex de la dirección normalizada (minúsculas, sin espacios): idéntico a security.HashDestination.
INSERT INTO known_destinations
    (user_id, asset_code, network, destination_hash, status, first_seen_at, last_used_at, use_count)
SELECT user_id, asset_code, network,
       encode(sha256(convert_to(lower(btrim(to_address)), 'UTF8')), 'hex'),
       'known', min(created_at), max(completed_at), count(*)
FROM blockchain_withdrawals
WHERE status = 'completed'
GROUP BY 1, 2, 3, 4
ON CONFLICT (user_id, asset_code, network, destination_hash) DO NOTHING;
