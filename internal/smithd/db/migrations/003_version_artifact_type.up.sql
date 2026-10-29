-- What a version carries: Kubernetes manifests, or an OpenTofu bundle
ALTER TABLE versions ADD COLUMN artifact_type TEXT NOT NULL DEFAULT 'manifests' CHECK(artifact_type IN ('manifests', 'terraform'));
