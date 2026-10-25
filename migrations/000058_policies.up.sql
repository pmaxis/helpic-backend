-- The site's policies as an admin wrote them, a row per policy and language.
-- Without a row the text that comes with Helpic stands; deleting the row
-- puts it back.
CREATE TABLE policies (
    kind       TEXT NOT NULL CHECK (kind IN ('privacy', 'terms', 'cookies')),
    locale     TEXT NOT NULL CHECK (locale IN ('en', 'uk')),
    body       TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by UUID REFERENCES users (id) ON DELETE SET NULL,
    PRIMARY KEY (kind, locale)
);
