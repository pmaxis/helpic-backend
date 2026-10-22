-- The National Bank of Ukraine's official rates: hryvnias for one unit of a
-- currency, on a day. Fetched when first needed and kept, since a day's
-- official rate never changes.
CREATE TABLE exchange_rates (
    currency   TEXT NOT NULL,
    rate_on    DATE NOT NULL,
    uah        NUMERIC(20, 6) NOT NULL CHECK (uah > 0),
    fetched_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (currency, rate_on)
);
