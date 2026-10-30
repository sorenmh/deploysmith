-- Where a version was built from (sent by forge from CI), so smithd can
-- report its deployments on the commit in GitHub
ALTER TABLE versions ADD COLUMN source_repo TEXT NOT NULL DEFAULT '';
ALTER TABLE versions ADD COLUMN source_sha TEXT NOT NULL DEFAULT '';
ALTER TABLE versions ADD COLUMN source_pr INTEGER NOT NULL DEFAULT 0;
