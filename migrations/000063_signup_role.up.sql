-- Public sign-up is open to clients too: a sign-up keeps the role it asked
-- for until its link creates the account. Sign-ups from before were all
-- freelancers'.
ALTER TABLE pending_signups ADD COLUMN role TEXT NOT NULL DEFAULT 'freelancer' CHECK (role IN ('freelancer', 'client'));
