CREATE TABLE IF NOT EXISTS releases (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    level TEXT NOT NULL DEFAULT 'normal',
    repo TEXT NOT NULL,
    commit_sha TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft',
    author TEXT NOT NULL,
    boss_approved INTEGER NOT NULL DEFAULT 0,
    boss_approved_by TEXT,
    boss_approved_at TEXT,
    active_slot TEXT,
    deploy_target TEXT NOT NULL DEFAULT 'green',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS change_items (
    id TEXT PRIMARY KEY,
    release_id TEXT NOT NULL,
    title TEXT NOT NULL,
    type TEXT NOT NULL,
    ref TEXT NOT NULL,
    developer TEXT NOT NULL,
    expected_impact TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    reviewer1 TEXT NOT NULL,
    reviewer2 TEXT NOT NULL,
    demo_required INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    FOREIGN KEY (release_id) REFERENCES releases(id)
);

CREATE TABLE IF NOT EXISTS reviews (
    id TEXT PRIMARY KEY,
    item_id TEXT NOT NULL,
    reviewer TEXT NOT NULL,
    tested INTEGER NOT NULL DEFAULT 0,
    demo_seen INTEGER NOT NULL DEFAULT 0,
    result TEXT NOT NULL,
    comment TEXT,
    created_at TEXT NOT NULL,
    FOREIGN KEY (item_id) REFERENCES change_items(id)
);

CREATE TABLE IF NOT EXISTS release_steps (
    id TEXT PRIMARY KEY,
    release_id TEXT NOT NULL,
    step_key TEXT NOT NULL,
    title TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    message TEXT,
    started_at TEXT,
    finished_at TEXT,
    FOREIGN KEY (release_id) REFERENCES releases(id)
);

CREATE TABLE IF NOT EXISTS test_reports (
    id TEXT PRIMARY KEY,
    release_id TEXT NOT NULL,
    env TEXT NOT NULL,
    functional_json TEXT,
    data_diff_json TEXT,
    ai_verdict TEXT,
    ai_passed INTEGER NOT NULL DEFAULT 0,
    passed INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    FOREIGN KEY (release_id) REFERENCES releases(id)
);

CREATE TABLE IF NOT EXISTS switch_events (
    id TEXT PRIMARY KEY,
    release_id TEXT NOT NULL,
    from_slot TEXT NOT NULL,
    to_slot TEXT NOT NULL,
    reason TEXT,
    actor TEXT NOT NULL,
    created_at TEXT NOT NULL,
    FOREIGN KEY (release_id) REFERENCES releases(id)
);

CREATE TABLE IF NOT EXISTS audit_logs (
    id TEXT PRIMARY KEY,
    actor TEXT NOT NULL,
    action TEXT NOT NULL,
    target TEXT NOT NULL,
    detail TEXT,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_change_items_release ON change_items(release_id);
CREATE INDEX IF NOT EXISTS idx_reviews_item ON reviews(item_id);
CREATE INDEX IF NOT EXISTS idx_release_steps_release ON release_steps(release_id);

CREATE TABLE IF NOT EXISTS users (
    username TEXT PRIMARY KEY,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'normal',
    display_name TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
    token TEXT PRIMARY KEY,
    username TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS deploy_snapshots (
    id TEXT PRIMARY KEY,
    release_id TEXT,
    deploy_target TEXT NOT NULL,
    title TEXT NOT NULL,
    backend_git_ref TEXT NOT NULL,
    frontend_git_ref TEXT NOT NULL,
    backend_sha TEXT,
    frontend_sha TEXT,
    actor TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'success',
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_deploy_snapshots_target ON deploy_snapshots(deploy_target, created_at);
