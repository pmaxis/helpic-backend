-- The currency a freelancer bills in; all their prices and reports use it.
ALTER TABLE users ADD COLUMN currency TEXT NOT NULL DEFAULT 'USD';

-- A task's price in cents, set by its freelancer; NULL = no price.
ALTER TABLE tasks ADD COLUMN price_cents BIGINT CHECK (price_cents >= 0);

-- A draft report reads prices and the currency live from the tasks and the
-- freelancer; sending it freezes them here, so a sent report keeps its sums.
ALTER TABLE reports ADD COLUMN currency TEXT;
ALTER TABLE report_items ADD COLUMN price_cents BIGINT;
