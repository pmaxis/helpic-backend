-- An ID from a request as a UUID, or NULL when it isn't one, so a lookup by a
-- malformed ID finds nothing instead of failing. Only the canonical form, as
-- the IDs Helpic hands out are. Comparing id = as_uuid($1) keeps the index on
-- id usable, where id::text = $1 read the whole table.
CREATE FUNCTION as_uuid(s TEXT) RETURNS UUID LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE WHEN s ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN s::uuid END
$$;

-- as_uuid for each ID of a list; the malformed ones become NULL and match nothing.
CREATE FUNCTION as_uuids(s TEXT[]) RETURNS UUID[] LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT array_agg(as_uuid(x)) FROM unnest(s) x
$$;
