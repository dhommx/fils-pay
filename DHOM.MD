# DHOM.MD: Getting FilsPay running, problems found and fixes

This file covers the work done so far on **getting FilsPay to run locally in
Docker**. As checked in, the repo did not build, migrate or let anyone log in.
Each item below records the problem, why it happened, what it broke, and the
smallest change that fixed it.

> Scope: these are setup and run blockers. The FP-xxx tickets, the finance
> items and FP-300 have **not** been worked on yet. Things noticed along the
> way but not fixed are listed at the end.

---

## Summary

| # | Problem | Where | Fix |
|---|---|---|---|
| 1 | Dockerfiles referenced by compose don't exist | `ops/` | Wrote `Dockerfile.node`, `Dockerfile.go`, `.dockerignore` |
| 2 | `.env.docker` never reaches the containers | `ops/docker-compose.yml` | Added `env_file: .env.docker` to each app service |
| 3 | API refuses to start: no valid webhook encryption key | `ops/.env.docker` | Added a 32-byte `WEBHOOK_SECRET_ENCRYPTION_KEY` |
| 4 | Settlement daemon refuses to start: no internal token | `ops/.env.docker` | Added `SETTLEMENT_INTERNAL_TOKEN` (API and settlement share it) |
| 5 | Migration 0054 fails: view reads a table that never existed | `db/migrations/0054_views.sql` | Removed the unused view |
| 6 | Migration 0055 fails: inserts into columns that don't exist | `db/migrations/0055_chart_of_accounts_suspense.sql` | Rewrote it against the real schema |
| 7 | Every browser call fails with "Failed to fetch": API has no CORS | `apps/web`, `apps/admin` | Vite dev proxy for `/v1`, same-origin clients |
| 8 | Login impossible: the one-time code is never sent anywhere | `ops/.env.docker` | `OTP_DEV_EXPOSE=true` for local only |

Result: Postgres, api, settlement and reporting run in Docker. All 66
migrations apply on a fresh database, and the seed passes all of its own
checks. The merchant dashboard (`:5173`) and admin console (`:5174`) log in
end to end.

---

## 1. Missing Dockerfiles

**Problem.** `ops/docker-compose.yml` builds every service from
`ops/Dockerfile.node` and `ops/Dockerfile.go`, but neither file is in the
repo. The ops README has the same argument in its comments ("they were in the
initial ops commit"), and git history shows they never were.

**Impact.** `docker compose up` stops right away. Nothing can run.

**Fix.**
- `ops/Dockerfile.node`: `node:20` (full image, not slim, so the native
  modules `argon2`/`sharp` can build), `npm ci` for the workspace, then copy
  the source in.
- `ops/Dockerfile.go`: two stages. `golang:1.22-alpine` builds
  `./cmd/settlement`, then a small `alpine` runtime with `tzdata`, so the
  `Asia/Bahrain` timezone lookups work.
- `.dockerignore`: keeps `node_modules`, `dist` and `.git` out of the build.

## 2. `.env.docker` was never loaded

**Problem.** The compose file says settings not listed under `environment:`
"come from ops/.env.docker, which compose picks up automatically". That isn't
true. Compose only reads a file named `.env` automatically, and only for `${VAR}`
substitution inside the compose file. It never passes that file into
containers.

**Why it matters.** Everything in that file (`NODE_ENV=production`, `PORT`,
`ACQUIRER_CALLBACK_SIGNING_SECRET`) silently never reached the services. The
team believed they were running in "production mode like the hosted box" when
they weren't.

**Fix.** Added `env_file: [.env.docker]` to `migrate`, `seed`, `api`,
`settlement` and `reporting`.

## 3. API crashed at boot: webhook encryption key

**Problem.** With `NODE_ENV=production` now actually applied, `loadConfig` in
`apps/api/src/config.ts` requires `WEBHOOK_SECRET_ENCRYPTION_KEY`. It must be
base64 that decodes to **exactly 32 bytes** (AES-256-GCM).

The compose comment says "isn't needed in dev; the api uses a built-in dev
key". Even in dev that's wrong: the built-in fallback
`dGVzdC1rZXktZm9yLWRldi1vbmx5LW5vdC1mb3ItcHJvZA==` decodes to
`test-key-for-dev-only-not-for-prod`, which is **34 bytes**. The API's own
length check rejects it, so the fallback can never work.

**Fix.** Generated a random 32-byte key (`openssl rand -base64 32`) and added it
to `ops/.env.docker`.

## 4. Settlement daemon crashed at boot: internal token

**Problem.** The compose comment says the daemon "falls back to
`SETTLEMENT_INTERNAL_TOKEN=dev-token`". That default doesn't exist in the code.
`config.RequireInternalToken()` in `apps/settlement/internal/config/config.go`
fails boot when the token is empty. The API also requires it under
`NODE_ENV=production`.

**Fix.** Generated a random token and added `SETTLEMENT_INTERNAL_TOKEN` to
`ops/.env.docker`. The API and settlement both read it from there, so they
always match.

## 5. Migration 0054 breaks every fresh database

**Problem.** `0054_views.sql` creates `v_unbatched_capture_events`, which reads
from `settlement_batch_events`. **No migration ever creates that table.**

**Why.** It looks like it was written for a schema that was planned but never
shipped. The view isn't used anywhere in the TypeScript, Go or SQL code.

**Impact.** `npm run migrate` stops at 0054, so 0054–0072 never apply. A new
developer or a new hosted environment can't get a working database.

**Fix.** Removed the dead view from 0054. The other two views in that file are
unchanged. The migration runner wraps each file in a transaction
(`db/migrate.ts`), so the failed attempt left nothing half-applied.

> Side note: a comment in `db/migrate.ts` says `schema_migrations` stores a
> checksum per file and refuses to run edited migrations. It doesn't. The
> table only has `filename` and `applied_at`.

## 6. Migration 0055 uses a schema that doesn't exist

**Problem.** `0055_chart_of_accounts_suspense.sql` inserts into
`chart_of_accounts (code, name, account_type, description)` with
`ON CONFLICT (code)` and kind `'suspense'`. The real table (0007) is
`(id, name, kind)`, and `kind` is limited to `asset | liability | revenue`.

**Impact.** Same as #5: migrations stop at 0055.

**Fix.** Rewrote it with the real columns. The two suspense accounts
(`fx_rounding_difference`, `chargeback_suspense`) are added as `asset`, the
same way 0007 already handles its own suspense account
`scheme_rounding_residue`. Nothing in the code references these accounts yet.

## 7. "Failed to fetch": the API has no CORS

**Problem.** The front-ends run on `localhost:5173` (web) and `localhost:5174`
(admin) and call the API at `localhost:3000` with `credentials: "include"`.
That's a cross-origin request, so the browser first sends a CORS preflight
(`OPTIONS`) check. The API has **no CORS handling at all**. It returns `404`
for the preflight, and the browser blocks the call.

A comment in `apps/web/src/api/client.ts` says "CORS is locked to the two
origins in config". That config doesn't exist.

**Fix (local dev only, smallest change).**
- `apps/web/vite.config.ts` and `apps/admin/vite.config.ts`: added
  `proxy: { "/v1": "http://localhost:3000" }`. Every API route is under `/v1`.
- `apps/web/.env.local` and `apps/admin/.env.local` (gitignored): set
  `VITE_API_BASE_URL=` / `VITE_ADMIN_API_BASE_URL=` to empty, so the clients
  call their own origin and Vite forwards to the API.

The browser now only sees same-origin requests, so the session cookie works
without any CORS. **A hosted deployment still needs real CORS or same-origin
routing.** The proxy only exists in the Vite dev server.

## 8. Login impossible: OTP code goes nowhere

**Problem.** Login is two steps: password, then a one-time code. The API
generates the code and logs "otp code generated" but never sends it by
SMS/email and never logs its value. In dev the code is meant to be returned
in the response (`devOtpCode`). That's switched off whenever
`NODE_ENV=production`, which #2 made real.

**Impact.** Nobody can log in to the admin console or the merchant
dashboard locally.

**Fix.** Added `OTP_DEV_EXPOSE=true` to `ops/.env.docker`. The login screen now
shows the code. **Local only.** This must never be set on a hosted box.

---

## How to run it

```bash
# backend (from repo root)
docker compose -f ops/docker-compose.yml up -d postgres
docker compose -f ops/docker-compose.yml run --rm migrate
docker compose -f ops/docker-compose.yml run --rm seed
docker compose -f ops/docker-compose.yml up -d api settlement reporting

# front-ends (separate terminals)
npm install
cd apps/web   && npm run dev     # http://localhost:5173/dashboard/login
cd apps/admin && npm run dev     # http://localhost:5174/login
```

| Who | Login |
|---|---|
| Admin | `superadmin@filspay.test` / `FilsPayAdminSeed!2026` |
| Merchant | Merchant ID `9972daab-2c86-459f-9d78-3fe1be4e3280`, `owner@alpharetail.example` / `FilsPaySeed!2026` |

---

# Part 2: Bug fixes from the case file

## Bug 1: Scheme statement always a few fils HIGHER than our ledger

**Reported by:** Finance (Fatima): "always over, never under, three months
running".

**Where:** `apps/settlement/internal/settlement/batch.go`, `CloseBatch`, the
`INSERT INTO scheme_export_lines`.

**What's wrong.** The card scheme only accepts two decimals, so each line is
cut down to the nearest 10 fils. Each `scheme_export_lines` row stores the
amount twice:

| Column | Computed from | Example: sale of 4.755 BD (4755 fils) |
|---|---|---|
| `amount_fils` (our ledger) | `TruncateToSchemeAmount(gross)`, rounded **down** | `4750` → 4.750 BD |
| `amount` (what goes in the scheme file) | `SchemeAmountBD(gross)`, from the **untruncated** gross | `4.755` → stored as **4.76** |

**Why it happens.** `amount` is a `NUMERIC(10,2)` column, and Postgres
**rounds half-up** when a 3-decimal value goes into a 2-decimal column. The
file writer (`LoadSchemeExportLines`) prefers `amount` over `amount_fils`. So
the scheme gets a rounded value while our ledger keeps a truncated one.
Rounding can only match or beat truncation, never fall below it. That's
exactly why the gap is **always over and never under**: +10 fils on every
line whose third decimal is 5–9.

The migration that added the column (0050) has a comment saying "amount is
amount_fils/1000 cut to two decimals... file and fils always agree". That's
true for the backfill, but not for new rows written by `CloseBatch`. Another
comment in the same file asks "NUMERIC(10,2) rounds on assignment though?"
and nobody answered.

**What it breaks.**
- The scheme statement never ties to our ledger. Finance chases an unexplained
  gap every month.
- **We tell the scheme to settle more money than we booked.** Up to 10 fils per
  line, every day, across every merchant.
- ADR-0002's rule ("the scheme file `amount` must be exactly
  `amount_fils / 1000`") is broken, so any residue figure built on it is wrong.

**Fix (one word).** Build `amount` from the truncated value, not the gross:

```diff
- `, batchID, line.transactionID, int64(truncated), SchemeAmountBD(line.grossFils), i+1)
+ `, batchID, line.transactionID, int64(truncated), SchemeAmountBD(truncated), i+1)
```

`truncated` is always a multiple of 10 fils, so it fits 2 decimals exactly
and Postgres has nothing to round.

**Proof.**
- New test `apps/settlement/internal/settlement/scheme_amount_matches_fils_test.go`
  closes a batch with 4755, 4747, 1005 and 12349 fils and checks
  `amount == amount_fils / 1000` on every line.
- **Before the fix it fails on all 4 lines** (`4.76` vs `4.75`, `4.75` vs `4.74`,
  `1.01` vs `1.00`, `12.35` vs `12.34`). Each one is +10 fils, which is the
  bug exactly.
- After the fix it passes. The full Go suite (`go test ./...`) passes.
- Existing tests are untouched.

**Not covered by this fix.** Rows already written with a rounded `amount`
stay wrong in the database. They need a one-off data correction
(`UPDATE scheme_export_lines SET amount = amount_fils::numeric/1000 WHERE amount <> amount_fils::numeric/1000`),
signed off by finance because those files were already sent to the scheme.

---

## Found along the way, not fixed yet

These don't block running the app, but they're real problems to take into the
audit:

1. **Secret committed to git.** `ops/.env.docker` is tracked. Its comments
   claim `.gitignore`'s `.env` entry covers it "as a prefix", but gitignore
   patterns don't work that way. The sandbox
   `ACQUIRER_CALLBACK_SIGNING_SECRET` is in the git history and should be
   rotated.
2. **Migration 0072 joins the wrong column.** `refund_merchant_summary` joins
   `transactions t ON t.id = r.id` (refund id vs. transaction id). It should
   be `t.id = r.transaction_id`. As written, `original_captured_fils` is
   always empty or wrong.
3. **One user owns two merchants.** `owner@alpharetail.example` is an owner of
   both Alpha Retail Co and Beta Digital Services in the seed data. That
   matters for the "cancelled merchant B's link from merchant A" report.
4. **Secure cookies with `NODE_ENV=production`.** Session cookies are
   `Secure`. This works on `http://localhost` in Chrome, but fails on any
   other plain-HTTP host.
5. **Many code comments are wrong** (#2, #3, #4, #7, the migrate checksum).
   Treat inline comments in this repo as claims to verify, not facts.

---

## Files changed

| File | Change |
|---|---|
| `ops/Dockerfile.node` | new |
| `ops/Dockerfile.go` | new |
| `.dockerignore` | new |
| `ops/docker-compose.yml` | `env_file` on five services |
| `ops/.env.docker` | + `WEBHOOK_SECRET_ENCRYPTION_KEY`, `SETTLEMENT_INTERNAL_TOKEN`, `OTP_DEV_EXPOSE` (original content kept) |
| `db/migrations/0054_views.sql` | removed dead `v_unbatched_capture_events` view |
| `db/migrations/0055_chart_of_accounts_suspense.sql` | rewritten for the real `chart_of_accounts` schema |
| `apps/web/vite.config.ts`, `apps/admin/vite.config.ts` | `/v1` dev proxy |
| `apps/web/.env.local`, `apps/admin/.env.local` | empty API base URL (gitignored, local only) |
| `apps/settlement/internal/settlement/batch.go` | Bug 1: scheme `amount` built from truncated fils |
| `apps/settlement/internal/settlement/scheme_amount_matches_fils_test.go` | new regression test for Bug 1 |
