CREATE TABLE IF NOT EXISTS sources (
  id TEXT PRIMARY KEY,
  provider TEXT NOT NULL,
  source_url TEXT NOT NULL,
  source_type TEXT NOT NULL,
  creator_id TEXT NOT NULL,
  creator_name TEXT NOT NULL,
  auth_profile_id TEXT NOT NULL,
  enabled INTEGER NOT NULL,
  sync_cursor TEXT NOT NULL,
  last_synced_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS story_tracks (
  id TEXT PRIMARY KEY,
  source_id TEXT NOT NULL,
  track_key TEXT NOT NULL,
  track_name TEXT NOT NULL,
  canonical_author TEXT NOT NULL,
  series_meta TEXT NOT NULL,
  output_policy TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(source_id, track_key)
);

CREATE TABLE IF NOT EXISTS releases (
  id TEXT PRIMARY KEY,
  source_id TEXT NOT NULL,
  provider_release_id TEXT NOT NULL,
  url TEXT NOT NULL,
  title TEXT NOT NULL,
  published_at TEXT NOT NULL,
  edited_at TEXT NOT NULL,
  post_type TEXT NOT NULL,
  visibility_state TEXT NOT NULL,
  normalized_payload_ref TEXT NOT NULL,
  raw_payload_ref TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  discovered_at TEXT NOT NULL,
  status TEXT NOT NULL,
  UNIQUE(source_id, provider_release_id)
);

CREATE TABLE IF NOT EXISTS release_assignments (
  release_id TEXT PRIMARY KEY,
  track_id TEXT NOT NULL,
  rule_id TEXT NOT NULL,
  release_role TEXT NOT NULL,
  confidence REAL NOT NULL
);

CREATE TABLE IF NOT EXISTS artifacts (
  id TEXT PRIMARY KEY,
  release_id TEXT NOT NULL,
  track_id TEXT NOT NULL,
  artifact_kind TEXT NOT NULL,
  is_canonical INTEGER NOT NULL,
  filename TEXT NOT NULL,
  mime_type TEXT NOT NULL,
  sha256 TEXT NOT NULL,
  storage_ref TEXT NOT NULL,
  built_at TEXT NOT NULL,
  state TEXT NOT NULL,
  metadata_ref TEXT NOT NULL,
  normalized_ref TEXT NOT NULL,
  raw_ref TEXT NOT NULL,
  UNIQUE(release_id, sha256, artifact_kind)
);

CREATE TABLE IF NOT EXISTS publish_records (
  id TEXT PRIMARY KEY,
  artifact_id TEXT NOT NULL,
  target_id TEXT NOT NULL,
  target_kind TEXT NOT NULL,
  target_ref TEXT NOT NULL,
  publish_hash TEXT NOT NULL,
  published_at TEXT NOT NULL,
  status TEXT NOT NULL,
  message TEXT NOT NULL,
  UNIQUE(artifact_id, target_id, publish_hash)
);

CREATE TABLE IF NOT EXISTS run_records (
  id TEXT PRIMARY KEY,
  command TEXT NOT NULL,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  status TEXT NOT NULL,
  summary TEXT NOT NULL,
  source_scope TEXT NOT NULL,
  dry_run INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS event_records (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL,
  timestamp TEXT NOT NULL,
  level TEXT NOT NULL,
  component TEXT NOT NULL,
  message TEXT NOT NULL,
  entity_kind TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  payload_ref TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS leases (
  key TEXT PRIMARY KEY,
  holder TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
