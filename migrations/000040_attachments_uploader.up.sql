-- An upload adds up what its uploader already stored, against their quota.
CREATE INDEX attachments_uploader_idx ON attachments (uploaded_by);
