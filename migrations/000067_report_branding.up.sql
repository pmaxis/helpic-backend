-- How a freelancer's reports look to their clients: a logo, an accent color
-- and their business details (name, tax number, bank account…), written as
-- they want them printed.
--
-- A logo is never changed in place: a new one is a new row, so a sent report
-- keeps showing the one it was sent with. A logo neither its owner nor any
-- report points at is deleted.
CREATE TABLE brand_logos (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    content_type TEXT NOT NULL CHECK (content_type IN ('image/png', 'image/jpeg')),
    data         BYTEA NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX brand_logos_owner_idx ON brand_logos (owner_id);

-- The freelancer's current look. brand_color is "#rrggbb", NULL for none.
ALTER TABLE users
    ADD COLUMN brand_logo_id    UUID REFERENCES brand_logos (id) ON DELETE SET NULL,
    ADD COLUMN brand_color      TEXT CHECK (brand_color ~ '^#[0-9a-f]{6}$'),
    ADD COLUMN business_details TEXT NOT NULL DEFAULT '';

-- A draft reads the look live from its freelancer; sending it keeps the look
-- here, as it does the prices, so a sent report reads the same later.
ALTER TABLE reports
    ADD COLUMN brand_logo_id    UUID REFERENCES brand_logos (id) ON DELETE SET NULL,
    ADD COLUMN brand_color      TEXT,
    ADD COLUMN business_details TEXT;

CREATE INDEX reports_brand_logo_idx ON reports (brand_logo_id) WHERE brand_logo_id IS NOT NULL;
