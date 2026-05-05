-- +goose Up

CREATE TABLE searches (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL,
    keywords    TEXT    NOT NULL,
    description TEXT    NOT NULL DEFAULT '',
    min_price   REAL,
    max_price   REAL,
    currency    TEXT    NOT NULL DEFAULT 'AUD',
    location    TEXT,
    platforms   TEXT    NOT NULL DEFAULT '[]',
    active      INTEGER NOT NULL DEFAULT 1,
    created_at  INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at  INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE listings (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    external_id  TEXT    NOT NULL,
    platform     TEXT    NOT NULL,
    title        TEXT    NOT NULL,
    description  TEXT    NOT NULL DEFAULT '',
    price        REAL,
    currency     TEXT    NOT NULL DEFAULT 'AUD',
    url          TEXT    NOT NULL,
    image_urls   TEXT    NOT NULL DEFAULT '[]',
    end_time     INTEGER,
    condition    TEXT    NOT NULL DEFAULT '',
    location     TEXT    NOT NULL DEFAULT '',
    raw_data     TEXT    NOT NULL DEFAULT '{}',
    status       TEXT    NOT NULL DEFAULT 'active',
    first_seen   INTEGER NOT NULL DEFAULT (unixepoch()),
    last_seen    INTEGER NOT NULL DEFAULT (unixepoch()),
    UNIQUE(platform, external_id)
);

CREATE INDEX listings_platform_idx  ON listings(platform);
CREATE INDEX listings_end_time_idx  ON listings(end_time) WHERE end_time IS NOT NULL;
CREATE INDEX listings_status_idx    ON listings(status);
CREATE INDEX listings_first_seen_idx ON listings(first_seen);

CREATE TABLE search_listings (
    search_id  INTEGER NOT NULL REFERENCES searches(id) ON DELETE CASCADE,
    listing_id INTEGER NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    matched_at INTEGER NOT NULL DEFAULT (unixepoch()),
    PRIMARY KEY (search_id, listing_id)
);

CREATE INDEX search_listings_listing_id_idx ON search_listings(listing_id);

CREATE TABLE bids (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    listing_id  INTEGER NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    amount      REAL    NOT NULL,
    currency    TEXT    NOT NULL DEFAULT 'AUD',
    placed_at   INTEGER NOT NULL DEFAULT (unixepoch()),
    notes       TEXT    NOT NULL DEFAULT '',
    result      TEXT    NOT NULL DEFAULT 'pending'
);

CREATE INDEX bids_listing_id_idx ON bids(listing_id);

CREATE TABLE evaluations (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    listing_id   INTEGER NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    search_id    INTEGER NOT NULL REFERENCES searches(id) ON DELETE CASCADE,
    score        REAL    NOT NULL,
    reasoning    TEXT    NOT NULL,
    model_used   TEXT    NOT NULL,
    created_at   INTEGER NOT NULL DEFAULT (unixepoch()),
    UNIQUE(listing_id, search_id)
);

CREATE INDEX evaluations_search_id_idx ON evaluations(search_id);

CREATE TABLE scan_runs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    search_id   INTEGER NOT NULL REFERENCES searches(id) ON DELETE CASCADE,
    platform    TEXT    NOT NULL,
    started_at  INTEGER NOT NULL DEFAULT (unixepoch()),
    finished_at INTEGER,
    new_items   INTEGER NOT NULL DEFAULT 0,
    errors      TEXT    NOT NULL DEFAULT '',
    status      TEXT    NOT NULL DEFAULT 'running'
);

CREATE INDEX scan_runs_search_id_idx  ON scan_runs(search_id);
CREATE INDEX scan_runs_started_at_idx ON scan_runs(started_at);

CREATE TABLE email_notifications (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    kind        TEXT    NOT NULL,
    subject     TEXT    NOT NULL,
    body_html   TEXT    NOT NULL,
    sent_at     INTEGER NOT NULL DEFAULT (unixepoch()),
    listing_ids TEXT    NOT NULL DEFAULT '[]'
);

CREATE INDEX email_notifications_kind_idx    ON email_notifications(kind);
CREATE INDEX email_notifications_sent_at_idx ON email_notifications(sent_at);

-- +goose Down

DROP TABLE IF EXISTS email_notifications;
DROP TABLE IF EXISTS scan_runs;
DROP TABLE IF EXISTS evaluations;
DROP TABLE IF EXISTS bids;
DROP TABLE IF EXISTS search_listings;
DROP TABLE IF EXISTS listings;
DROP TABLE IF EXISTS searches;
