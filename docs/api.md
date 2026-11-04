# API

JSON over HTTP at `/api/v1`. Authenticate with the session cookie, a personal token (`Authorization: Bearer prn_…`, Settings → API & shortcuts), a Pocket Core device or user token, or a Pocket Core app-link token. Everything is per person: another person's ids answer 404. Money is in **minor units** (cents); dates are `YYYY-MM-DD`. PUT bodies are partial: only the keys you send change.

## Overview

| Call | What |
|---|---|
| `GET /overview` | `counts` (total, expired, soon, check, undated, auto), `groups`, `expired`, `soon` (within the “coming up” window or already reminding), `check` (found automatically), `later`, `recent` (renewals done), `travel` (trip warnings), `year_cost` per currency |
| `GET /due?days=30` | `{days, renewals}` — what runs out within the days, and what has run out (the Pocket AI briefing, a Homepage widget) |
| `GET /day/{date}` | `{expires, renewed}` on that date (Pocket Day) |
| `GET /search?q` | Renewals matching `q` (title, whose, provider, number, notes) |
| `GET /travel` | Coming trips from Pocket Travel and the warnings for them |
| `GET /kinds`, `GET /holders` | The kinds with their default reminders and terms; the names renewals belong to |

## Renewals

| Call | What |
|---|---|
| `GET /renewals?q&kind&group&holder&status&source&before&after&archived=1&sort=urgency` | Soonest first (no date last). `status`: expired, soon, ok, undated, archived, check. Each has `status`, `days_left`, `act_by`, `group`, `renewals` (times renewed) |
| `POST /renewals` | `title`, `kind` (passport, id_card, driving_licence, visa, insurance, vehicle, warranty, domain, certificate, subscription, membership, contract, health, licence, other), `expires_on`, `holder`, `provider`, `number`, `issued_on`, `renew_n` + `renew_unit` (the usual term; default from the kind), `auto_renews`, `remind_days` (`"90,30,7"`; empty = the kind's), `lead_days` (notice period / how long renewing takes), `cost`, `currency`, `renew_url`, `notes`, `document_id` + `document_title` (Pocket Documents), `contact_id` + `contact_name` (Pocket Contacts), `external_id` (a repeat returns the first) |
| `GET /renewals/{id}` | `{renewal, history, reminders, next_expires, travel}` |
| `PUT /renewals/{id}` | Any of the fields above, plus `archived`, `needs_check`. A new `expires_on` resets the reminders |
| `DELETE /renewals/{id}` | One found in another app is remembered, so scans do not add it again |
| `POST /renewals/{id}/renew` | `{renewed_on, expires_on (default: one term on), cost, currency, note, number, issued_on, money}` → `{renewal, history, money}`; `money: true` books the cost in Pocket Money |
| `POST /renewals/{id}/task` | “Renew …” in Pocket Tasks, due by the last day to act |
| `POST /renewals/checked` | `{ids: […]}` or `{all: true}`: found ones looked at |
| `GET /history?renewal_id&limit`, `DELETE /history/{id}`, `POST /history/{id}/money` | Renewals done, newest first; book an earlier one's cost in Pocket Money |

## Finding renewals in the other apps

| Call | What |
|---|---|
| `GET /sources` | Each source (documents, home, garage, inventory, monitoring, money, email) with its connection, `included`, `running`, `last` scan and how many renewals came from it; plus `live`, `scan_nightly`, `documents_ai` and the event `stream` status |
| `POST /scan` | `{source: "home"}` or `{source: "all"}` (every included, connected source) — runs in the background (202); poll `/sources` |
| `GET /scans` | The last 50 scans |
| `GET /dismissed`, `POST /dismissed/restore` | Found and deleted (never added again); `{source_ref}` or `{all: true}` allows them again |

What each source reads:

- **Pocket Documents** (`documents:read`): date custom fields whose name says when something runs out (“Valid until”, “Expiry date”, “Važi do”…; not “Due date” or “Issued”), else — for documents that look like a passport, policy, contract or warranty — the text read by AI. With `events:read` on, every `documents.processed` / `documents.updated` event is read within seconds.
- **Pocket Home** (`home:read`): `GET /things` → `warranty_until`.
- **Pocket Garage** (`vehicles:read`): `GET /vehicles/{id}/reminders` (registration, inspection, insurance, vignette, tax…) and `GET /vehicles/{id}/documents` → `expires_on`.
- **Pocket Inventory** (`items:read`): `GET /items?expiring=3650` → `expiry_date`, food left out.
- **Pocket Monitoring** (`monitoring:read`): `GET /servers/{id}/checks` → `cert_expires` of https checks.
- **Pocket Money** (`transactions:read`): `GET /subscriptions` → yearly (or every 3+ months) ones, and any with an `end_date`.
- **Pocket Email** (`mail:read`): subjects like “expires”, “renewal”, “ističe” in the last year, read by AI.

Live: with each app's `events:read` link on, these events are followed over Core's stream — `documents.processed`, `documents.updated` (that one document is read), `home.thing_saved`, `garage.reminder_saved`, `garage.document_saved`, `inventory.item_created`, `inventory.item_updated`, `money.subscription_saved`, `money.subscription_paid`, `monitoring.cert_changed` (a quick scan of that app for that person, a few seconds later). A nightly scan at 04:00 catches the rest.

Everything found is added with `needs_check: true`, `source`, `source_ref`, `source_url` (open it there) and `found_by`. A later date in the source moves the renewal on (with a history entry); an earlier one never moves it back.

## The rest

`/auth/*`, `/users`, `/settings` (+ `/settings/system`), `/tokens`, `/integrations/test`, `/notify/test`, `/notify/check`, `/ai/status`, `/ai/models`, `/links/money/accounts`, `/links/money/categories`, `/links/calendar/calendars`, `POST /links/calendar/sync`, `/links/contacts?q`, `/links/documents?q`, `/backup` (admin), `/export.json`, `/export.csv`, `POST /import`.

## Pocket Core

Renewals offers `renewals:read` (overview, due, day, search, travel, kinds, holders, renewals, history) and `renewals:write` (`POST /renewals`, `PUT/DELETE /renewals/{id}` and `POST /renewals/{id}/renew` for what that app added). Everything else answers 403 to a link token.

It uses Pocket Documents (`documents:read`, `events:read`), Pocket Home (`home:read`, `events:read`), Pocket Garage (`vehicles:read`, `events:read`), Pocket Inventory (`items:read`, `events:read`), Pocket Monitoring (`monitoring:read`, `events:read`), Pocket Money (`accounts:read`, `transactions:read`, `transactions:write`, `events:read`), Pocket Email (`mail:read`), Pocket Travel (`trips:read`), Pocket Calendar (`calendar:read`, `calendar:write`), Pocket Tasks (`tasks:write`), Pocket Contacts (`contacts:read`) and Pocket AI inference.

Events, each with `user_email`: `renewals.added`, `renewals.found` (added from another app), `renewals.due` (a reminder day), `renewals.expired`, `renewals.renewed` (also when it renewed by itself).
