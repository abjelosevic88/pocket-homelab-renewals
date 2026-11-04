CREATE TABLE users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  email         TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  role          TEXT NOT NULL DEFAULT 'member',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT
);

CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE api_tokens (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL DEFAULT '',
  token_hash   TEXT NOT NULL UNIQUE,
  prefix       TEXT NOT NULL DEFAULT '',
  last_used_at TEXT,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Box-wide settings (title, setup flag); everything else is personal.
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);

CREATE TABLE user_settings (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key     TEXT NOT NULL,
  value   TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (user_id, key)
);

CREATE TABLE cache (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL DEFAULT '',
  fetched_at TEXT NOT NULL
);

-- ---------- Pocket Renewals: everything below is per person ----------

-- Something that runs out and has to be renewed: a passport, an insurance
-- policy, a warranty, a domain, a TLS certificate, a subscription.
CREATE TABLE renewals (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title           TEXT NOT NULL,
  kind            TEXT NOT NULL DEFAULT 'other',      -- passport | id_card | driving_licence | visa | insurance | vehicle | warranty | domain | certificate | subscription | membership | contract | health | licence | other
  holder          TEXT NOT NULL DEFAULT '',           -- whose it is: "Ana", "the Golf"
  provider        TEXT NOT NULL DEFAULT '',           -- who issued it: the office, the insurer, the registrar
  number          TEXT NOT NULL DEFAULT '',           -- document or policy number
  issued_on       TEXT NOT NULL DEFAULT '',
  expires_on      TEXT NOT NULL DEFAULT '',           -- YYYY-MM-DD, '' = no end date known yet
  renew_n         INTEGER NOT NULL DEFAULT 0,         -- the usual term (0 = not set)
  renew_unit      TEXT NOT NULL DEFAULT 'year',       -- day | week | month | year
  auto_renews     INTEGER NOT NULL DEFAULT 0,         -- renews by itself (a subscription): rolls on when the date passes
  remind_days     TEXT NOT NULL DEFAULT '',           -- "90,30,7"; '' = the kind's default
  lead_days       INTEGER NOT NULL DEFAULT 0,         -- act this long before: a notice period, how long renewing takes
  cost            INTEGER,                            -- minor units, what a renewal costs
  currency        TEXT NOT NULL DEFAULT 'EUR',
  renew_url       TEXT NOT NULL DEFAULT '',           -- where to renew it
  notes           TEXT NOT NULL DEFAULT '',
  document_id     INTEGER,                            -- the paper in Pocket Documents
  document_title  TEXT NOT NULL DEFAULT '',
  contact_id      INTEGER,                            -- who to call, in Pocket Contacts
  contact_name    TEXT NOT NULL DEFAULT '',
  archived        INTEGER NOT NULL DEFAULT 0,         -- no longer have it
  needs_check     INTEGER NOT NULL DEFAULT 0,         -- found automatically, nobody looked at it yet
  source          TEXT NOT NULL DEFAULT 'web',        -- web | api | import | demo | documents | home | garage | inventory | monitoring | money | email | <app slug>
  source_ref      TEXT NOT NULL DEFAULT '',           -- what it was found from: "documents:12", "home:thing:4"
  source_url      TEXT NOT NULL DEFAULT '',           -- open it in the app it came from
  found_by        TEXT NOT NULL DEFAULT '',           -- how: "the Expiry date field", "AI read the text"
  notified_days   INTEGER,                            -- the last reminder sent: days before (negative = after) the date
  notified_on     TEXT NOT NULL DEFAULT '',
  cal_event_id    INTEGER,                            -- the event in Pocket Calendar
  cal_synced      TEXT NOT NULL DEFAULT '',           -- what the event shows, '' = needs a push
  task_id         INTEGER,                            -- a task in Pocket Tasks
  created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX renewals_user ON renewals(user_id, archived, expires_on);
CREATE UNIQUE INDEX renewals_source ON renewals(user_id, source_ref) WHERE source_ref <> '';

-- Every renewal: the old and the new date, what it cost.
CREATE TABLE history (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  renewal_id    INTEGER NOT NULL REFERENCES renewals(id) ON DELETE CASCADE,
  renewed_on    TEXT NOT NULL,
  old_expires   TEXT NOT NULL DEFAULT '',
  new_expires   TEXT NOT NULL DEFAULT '',
  cost          INTEGER NOT NULL DEFAULT 0,
  currency      TEXT NOT NULL DEFAULT 'EUR',
  note          TEXT NOT NULL DEFAULT '',
  money_tx_id   INTEGER,                              -- the booking in Pocket Money
  source        TEXT NOT NULL DEFAULT 'web',          -- web | auto | <app slug> (a later date found there)
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX history_renewal ON history(user_id, renewal_id, renewed_on);

-- Found automatically and then deleted: never added again by a scan.
CREATE TABLE dismissed (
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  source_ref  TEXT NOT NULL,
  title       TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  PRIMARY KEY (user_id, source_ref)
);

-- Each look through another app: when, what it found and added.
CREATE TABLE scans (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  source       TEXT NOT NULL,                         -- documents | home | garage | inventory | monitoring | money | email
  trigger      TEXT NOT NULL DEFAULT 'manual',        -- manual | weekly | event
  started_at   TEXT NOT NULL,
  finished_at  TEXT NOT NULL DEFAULT '',
  looked       INTEGER NOT NULL DEFAULT 0,            -- items looked at
  found        INTEGER NOT NULL DEFAULT 0,            -- with a date that runs out
  added        INTEGER NOT NULL DEFAULT 0,
  updated      INTEGER NOT NULL DEFAULT 0,
  error        TEXT NOT NULL DEFAULT ''
);
CREATE INDEX scans_user ON scans(user_id, source, started_at);
