CREATE TABLE IF NOT EXISTS devices (
    id         UUID         PRIMARY KEY,
    name       VARCHAR(255) NOT NULL CHECK (btrim(name) <> ''),
    brand      VARCHAR(255) NOT NULL CHECK (btrim(brand) <> ''),
    state      VARCHAR(16)  NOT NULL CHECK (state IN ('available', 'in-use', 'inactive')),
    created_at TIMESTAMPTZ  NOT NULL,
    updated_at TIMESTAMPTZ  NOT NULL,
    version    BIGINT       NOT NULL DEFAULT 1
);

-- Listing is ordered by creation time; filters are by brand (case-insensitive) and state.
CREATE INDEX IF NOT EXISTS idx_devices_created_at  ON devices (created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_devices_brand_lower ON devices (lower(brand), created_at DESC);
CREATE INDEX IF NOT EXISTS idx_devices_state       ON devices (state, created_at DESC);

-- Defense in depth: the creation time can never be changed, even by direct SQL.
CREATE OR REPLACE FUNCTION devices_protect_created_at() RETURNS trigger AS $$
BEGIN
    IF NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'created_at is immutable' USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_devices_protect_created_at
    BEFORE UPDATE ON devices
    FOR EACH ROW EXECUTE FUNCTION devices_protect_created_at();
