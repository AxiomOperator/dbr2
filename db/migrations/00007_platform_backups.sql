-- SPDX-License-Identifier: Apache-2.0
-- Phase 9: platform self-backup (ADR-0008). Every Platform Protection run is
-- recorded here; the System Repository is flagged on repositories.

-- +goose Up

-- The System Repository receives the platform self-backup (a pinned Kopia
-- snapshot of the age-encrypted Platform Recovery Bundle). At most one per
-- organization; it must never be the only copy (the bundle is also written
-- to a directory outside every Repository).
ALTER TABLE repositories ADD COLUMN is_system boolean NOT NULL DEFAULT false;
CREATE UNIQUE INDEX repositories_system ON repositories (org_id) WHERE is_system;

CREATE TABLE platform_backups (
    id             text PRIMARY KEY, -- pb_<ULID>
    org_id         uuid NOT NULL REFERENCES organizations (id),
    state          text NOT NULL DEFAULT 'running'
                   CHECK (state IN ('running', 'succeeded', 'partial', 'failed')),
    trigger        text NOT NULL DEFAULT 'scheduled',
    requested_by   uuid REFERENCES users (id) ON DELETE SET NULL,
    workflow_id    text,
    run_id         text,
    started_at     timestamptz NOT NULL DEFAULT now(),
    finished_at    timestamptz,
    size_bytes     bigint NOT NULL DEFAULT 0,
    sha256         text,           -- of the encrypted bundle file
    file_name      text,           -- dbr2-platform-<UTC timestamp>.tar.zst.age
    snapshot_id    text,           -- Kopia snapshot in the System Repository
    repository_id  uuid REFERENCES repositories (id) ON DELETE SET NULL,
    bundle_path    text,           -- copy in DBR2_PLATFORM_BUNDLE_DIR (worker)
    error          text,
    -- The bundle's plaintext manifest.json (versions, row counts, hashes,
    -- missing reposervers). It never contains secrets.
    manifest       jsonb
);
CREATE INDEX platform_backups_org ON platform_backups (org_id, started_at DESC);

-- +goose Down
DROP TABLE platform_backups;
DROP INDEX repositories_system;
ALTER TABLE repositories DROP COLUMN is_system;
