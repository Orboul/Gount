# Gount Audit Report

Date: 2026-05-14

## Executive Summary

`gount` is a compact Go ingestion service centered on a single beacon endpoint, `POST /t`, backed by pluggable storage and local GeoIP lookup. The core runtime is small and easy to reason about, which is a strong fit for a "minimal attack surface" goal.

The main risks are not classic SQL injection bugs. They are surface-area mismatches and operational weak points:

- permissive default CORS (`*`) on the tracking endpoint
- no actual rate limiting or admission control
- oversized bodies are truncated, not rejected
- a startup auto-download path introduces avoidable supply-chain/network exposure
- the SQL store serializes all inserts through one mutex, which hurts beacon throughput
- repo artifacts still advertise legacy `GET`, tracking-pixel, and old product-name flows that no longer match the server

## Action Items

- [ ] Change the default CORS posture from wildcard to explicit allowlist-only.
- [ ] Reject oversized beacon bodies with `413 Payload Too Large` instead of silently truncating.
- [ ] Add rate limiting or upstream admission control for `/t` and `/health`.
- [ ] Remove or gate the automatic GeoLite download path; prefer operator-provisioned files plus checksum verification.
- [ ] Refactor `sqlStore` so PostgreSQL/MySQL inserts are not serialized by a global mutex.
- [ ] Fail startup when `secret_salt` is empty or still set to the placeholder.
- [ ] Restrict `/health` to `GET` and `HEAD`, and stop returning raw internal error details to callers.
- [ ] Delete or update stale artifacts that still assume `GET /t`, tracking pixels, `fetch(..., { method: 'GET' })`, `apkount`, or `update_frequency_days`.

## System Architecture & Frameworks

### Tech Stack

- Language: Go 1.24 (`[go/go.mod](/Users/matthewsaw/base-proj/gount/go/go.mod:1)`)
- HTTP server: standard library `net/http`
- Config: YAML via `gopkg.in/yaml.v3`
- GeoIP: `github.com/oschwald/geoip2-golang`
- Datastores:
  - SQLite via `modernc.org/sqlite`
  - PostgreSQL via `github.com/lib/pq`
  - MySQL via `github.com/go-sql-driver/mysql`
  - CSV and JSONL file stores implemented in-process
- Deployment/tooling: shell-based setup/build scripts, prebuilt binaries under `dist/`

### Runtime Components

- `main()` bootstraps config, logging, store, GeoIP DB, cleanup worker, and HTTP routes (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:1350)`).
- `/t` is the ingestion endpoint (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:1171)`).
- `/health` is the readiness endpoint (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:1235)`).
- A 24-hour retention worker prunes old records (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:1048)`).

### Data Flow: `sendBeacon()` to Database

1. Browser sends `POST /t` with URL-encoded body fields `p` and optional `ref` (`[README.md](/Users/matthewsaw/base-proj/gount/README.md:21)`).
2. `trackHandler` applies CORS headers, handles `OPTIONS`, and rejects non-`POST` methods (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:1171)`).
3. Request body is read with `io.ReadAll(io.LimitReader(..., 4096))`, then decoded with `url.ParseQuery` (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:1197)`).
4. Client IP is resolved from socket or trusted proxy headers (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:1074)`).
5. A pseudonymous `unique_id` is derived as `SHA-256(ip | user-agent | secret_salt)` (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:1103)`).
6. Country and optional city are looked up from the local MaxMind DB (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:1113)`).
7. Visit data is written to the configured store via `InsertVisit(...)` (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:1224)`).
8. The endpoint returns `204 No Content` unless `strict_tracking_errors` is enabled and persistence fails (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:1224)`).

## Security Audit

### Entry Points

- `POST /t` and `OPTIONS /t` (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:1177)`)
- `/health` on any HTTP method today (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:1235)`)
- YAML config file loading (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:175)`)
- Remote GeoLite download on first run or recovery (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:326)`)
- Database DSN / file paths from config (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:1010)`)

### Findings

#### 1. Default CORS is broader than a minimal-surface beacon service should allow

Current behavior returns `Access-Control-Allow-Origin: *` whenever `cors_allowed_origins` is empty (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:1153)`).

Impact:

- any site can send browser-originated beacons to the service
- this increases abuse potential and makes the endpoint act like a public collector by default
- it conflicts with the stated preference for a minimal attack surface

Recommendation:

- make explicit origin allowlists the default
- if an operator truly wants public collection, require an intentional opt-in such as `cors_allowed_origins: ["*"]`

#### 2. Oversized bodies are truncated, not rejected

The handler uses `io.LimitReader(r.Body, 4096)` and then parses whatever was read (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:1197)`). If a payload exceeds 4096 bytes, the extra bytes are discarded and the truncated body may still parse successfully.

Impact:

- request tampering and malformed partial payloads can be accepted as valid
- operators do not get a clean `413 Payload Too Large`
- the implementation does enforce a small payload cap, but not in a fail-closed way

Recommendation:

- use `http.MaxBytesReader` or read `limit+1` bytes and reject when the cap is exceeded
- document the supported maximum explicitly

Suggested refactor:

```go
const maxBeaconBody = 4096

func (a *App) trackHandler(w http.ResponseWriter, r *http.Request) {
    r.Body = http.MaxBytesReader(w, r.Body, maxBeaconBody)
    body, err := io.ReadAll(r.Body)
    if err != nil {
        var maxErr *http.MaxBytesError
        if errors.As(err, &maxErr) {
            http.Error(w, "beacon payload too large", http.StatusRequestEntityTooLarge)
            return
        }
        http.Error(w, "invalid beacon payload", http.StatusBadRequest)
        return
    }
    // parse body...
}
```

#### 3. No rate limiting or abuse controls exist

There is no application-level rate limiting, no token bucket, no IP quotas, and no backpressure controls around `/t` or `/health`.

Impact:

- trivial high-rate abuse can fill storage, exhaust disk, or saturate CPU on GeoIP lookups
- health checks can also be spammed to force DB ping and GeoDB lookups

Recommendation:

- enforce limits at the reverse proxy first
- optionally add a lightweight in-process limiter for direct-exposed deployments

Suggested deployment policy:

```nginx
limit_req_zone $binary_remote_addr zone=gount_track:10m rate=20r/s;
limit_req_zone $binary_remote_addr zone=gount_health:1m rate=2r/s;

location = /t {
    limit_req zone=gount_track burst=40 nodelay;
    proxy_pass http://127.0.0.1:8080;
}

location = /health {
    limit_req zone=gount_health burst=4 nodelay;
    proxy_pass http://127.0.0.1:8080;
}
```

#### 4. Automatic GeoLite download increases supply-chain and availability exposure

On first run and some recovery paths, the service downloads GeoLite files from GitHub-hosted mirrors (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:308)`, `[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:326)`).

Impact:

- startup depends on outbound network access
- the runtime attack surface includes remote content retrieval
- there is no checksum or signature verification before activation
- this is at odds with a hardened ingestion endpoint model

Recommendation:

- prefer operator-provisioned `.mmdb` files
- if auto-download remains, require checksum verification and make it an explicit setup-time action instead of a runtime side effect

#### 5. Secret salt bootstrap is easy to deploy insecurely

The default config writes `secret_salt: "CHANGE_ME_use_openssl_rand_hex_32"` (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:100)`), but startup does not reject that value.

Impact:

- operators can unintentionally deploy with a known salt
- pseudonymous IDs become predictable across such installations

Recommendation:

- fail fast when `secret_salt` is empty or unchanged from the placeholder

Suggested guard:

```go
const placeholderSalt = "CHANGE_ME_use_openssl_rand_hex_32"

if strings.TrimSpace(cfg.SecretSalt) == "" || cfg.SecretSalt == placeholderSalt {
    log.Fatal("config: secret_salt must be set to a unique random value before startup")
}
```

#### 6. `OPTIONS` handling is wider than necessary for a sendBeacon-focused endpoint

The handler supports `OPTIONS` and reflects `Access-Control-Request-Headers` back to the caller (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:1177)`).

Impact:

- broader cross-origin method/header negotiation than most `sendBeacon()` deployments need
- reflection is not immediately exploitable here, but it is not minimal

Recommendation:

- if the product only supports basic beacon requests, keep `POST` only and do not reflect arbitrary requested headers
- otherwise maintain a fixed allowlist such as `Content-Type`

Suggested refactor:

```go
if r.Method == http.MethodOptions {
    if a.corsAllowOrigin(r.Header.Get("Origin")) == "" {
        w.WriteHeader(http.StatusForbidden)
        return
    }
    w.Header().Set("Access-Control-Allow-Methods", "POST")
    w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
    w.Header().Set("Access-Control-Max-Age", "600")
    w.WriteHeader(http.StatusNoContent)
    return
}
```

#### 7. `/health` leaks internals and accepts unnecessary methods

`healthHandler` does not restrict methods, and it returns raw readiness error text to the caller (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:1235)`).

Impact:

- information disclosure about storage and GeoDB state
- a slightly wider public surface than needed

Recommendation:

- allow only `GET` and `HEAD`
- return a generic unhealthy body to unauthenticated callers while keeping detailed errors in logs

#### 8. Injection review: SQL injection risk is low in current code

Current SQL writes use parameterized statements for all user-controlled values (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:587)`).

Assessment:

- SQL injection: no obvious issue found
- file injection/path traversal from request payload: no obvious issue found
- header-based IP spoofing: mitigated by `trusted_proxies` checks (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:1074)`)

Residual concerns:

- configuration values such as DSNs and filesystem paths remain trusted operator input
- logs and health bodies can still expose internals during failures

### Minimal Attack Surface Assessment

What already aligns well:

- `POST`-only ingestion behavior in code (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:1188)`)
- fast `204 No Content` response path (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:1232)`)
- parameterized SQL writes
- trusted-proxy gating for forwarded headers
- bounded field lengths for `path` and `referrer`

What works against the philosophy:

- wildcard CORS by default
- runtime auto-download behavior
- multiple storage backends, including slow file formats, in the main binary
- OPTIONS reflection for arbitrary requested headers
- broad `/health` behavior and verbose unhealthy messages
- stale repo assets that still encourage unsupported GET-based tracking

## Code Hygiene & Redundancy

### Stale or Dead Artifacts

#### Legacy docs in `gount/README.md`

This generated/local README still documents:

- tracking pixels via `GET /t` (`[gount/README.md](/Users/matthewsaw/base-proj/gount/gount/README.md:39)`)
- `fetch(..., { keepalive: true })` with `GET` (`[gount/README.md](/Users/matthewsaw/base-proj/gount/gount/README.md:44)`)
- a config key `update_frequency_days` that does not exist in Go config (`[gount/README.md](/Users/matthewsaw/base-proj/gount/gount/README.md:67)`)

This is a high-value cleanup target because it actively points users toward a larger and now incorrect surface.

#### Demo and test remnants still use GET and old names

- `testing/demo.html` sends `fetch(..., { method: 'GET', keepalive: true })` and includes a pixel fallback (`[testing/demo.html](/Users/matthewsaw/base-proj/gount/testing/demo.html:180)`, `[testing/demo.html](/Users/matthewsaw/base-proj/gount/testing/demo.html:205)`)
- `testing/esdemo.html` points at `tracker.orboul.com` and also uses `fetch()` plus an `<img>` beacon (`[testing/esdemo.html](/Users/matthewsaw/base-proj/gount/testing/esdemo.html:17)`, `[testing/esdemo.html](/Users/matthewsaw/base-proj/gount/testing/esdemo.html:23)`)
- `testing/config.yaml` still says `apkount` and includes `update_frequency_days` (`[testing/config.yaml](/Users/matthewsaw/base-proj/gount/testing/config.yaml:1)`, `[testing/config.yaml](/Users/matthewsaw/base-proj/gount/testing/config.yaml:16)`)
- `test_api.sh` load-tests the endpoint with `curl "http://localhost:8080/t?p=/idkyet"` which is `GET`, not `POST` (`[test_api.sh](/Users/matthewsaw/base-proj/gount/test_api.sh:7)`)
- `NOTES.md` still references `Orboul/Gount` as source (`[NOTES.md](/Users/matthewsaw/base-proj/gount/NOTES.md:8)`)

Recommendation:

- either delete these artifacts or bring them fully in line with the hardened POST-only beacon contract

### Redundant / Overlapping Logic

#### File-store retention paths duplicate expensive rewrite logic

- CSV retention loads all rows into memory and rewrites the file (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:816)`)
- JSON retention loads the entire file into memory and rewrites it (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:947)`)

These are acceptable for toy or migration use, but they are not a strong fit for a high-volume beacon endpoint.

#### SQL store mutex is over-broad

`sqlStore.InsertVisit`, `DeleteOldVisits`, `Close`, and `HealthCheck` all take the same mutex, even for PostgreSQL/MySQL where `database/sql` already handles connection concurrency (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:577)`).

This is not dead code, but it is redundant synchronization and a likely throughput bottleneck.

### Dependency Hygiene

No obviously unused direct Go dependencies were found in `go/go.mod`. The direct requirements all map to active code paths. The larger hygiene concern is support breadth, not package bloat:

- five storage backends in one binary
- setup/demo/testing artifacts that target older behavior

## Optimizations

### High-Concurrency Handling

#### 1. Remove global serialization from SQL writes

For PostgreSQL/MySQL, allow concurrent inserts and keep locking only around SQLite recovery state.

Suggested direction:

- split SQLite recovery concerns from generic SQL inserts
- use `database/sql` pool settings explicitly

Example:

```go
db.SetMaxOpenConns(32)
db.SetMaxIdleConns(32)
db.SetConnMaxIdleTime(5 * time.Minute)
```

#### 2. Consider asynchronous batching for beacon writes

For the highest write rates, a bounded in-memory channel plus one or more writer goroutines can reduce per-request latency and collapse write amplification. This is especially useful for SQLite and file backends.

Tradeoff:

- better throughput
- more complexity
- possible data loss on crash unless the queue is carefully drained

#### 3. Keep `/t` logic constant-time and allocation-light

Current handler work is already small, but the following would help further:

- reject oversize bodies before parse
- avoid GeoIP lookup for RFC1918/private/local addresses if geo data is not meaningful for them
- short-circuit known-empty or malformed bodies early

#### 4. Treat CSV/JSON backends as non-production

For a real beacon endpoint, SQLite is the minimum practical local backend, and PostgreSQL is the better multi-worker/high-rate option. CSV/JSON should be documented as debug/export backends, not high-concurrency targets.

### Database Indexing & Schema

Current schema creates only `idx_visits_timestamp`, which is appropriate for retention cleanup (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:521)`).

Recommendations by backend:

- SQLite:
  - keep `WAL`; it is the right default for concurrent readers and one writer (`[go/main.go](/Users/matthewsaw/base-proj/gount/go/main.go:505)`)
  - consider `PRAGMA synchronous=NORMAL` if durability tradeoffs are acceptable
- PostgreSQL:
  - consider `BIGINT` plus a generated `timestamptz`, or store `timestamptz` directly for easier analytics
  - for large tables, add a `BRIN` index on time instead of or alongside a btree
  - if common queries are by page and time, add `(path, timestamp DESC)`
  - if common queries are by country and time, add `(country, timestamp DESC)`
- MySQL:
  - consider `INDEX idx_visits_path_ts (path, timestamp)`
  - consider `INDEX idx_visits_country_ts (country, timestamp)`

Important write-path note:

- every extra secondary index slows ingestion
- for a write-heavy beacon service, add only the indexes that match actual query patterns

## Problem & Solution Table

| Problem | Evidence | Risk | Actionable Solution |
|---|---|---|---|
| Wildcard CORS by default | `[go/main.go:1153](/Users/matthewsaw/base-proj/gount/go/main.go:1153)` | Broader public ingestion surface | Make empty allowlist mean deny, and require explicit origins or explicit `"*"` opt-in. |
| Oversized bodies are truncated | `[go/main.go:1197](/Users/matthewsaw/base-proj/gount/go/main.go:1197)` | Ambiguous parsing, no `413` | Replace `io.LimitReader` pattern with `http.MaxBytesReader`. |
| No rate limiting | No limiter in code or config | DoS, storage exhaustion | Add proxy-level rate limiting and optional in-process token bucket. |
| Runtime GeoLite auto-download | `[go/main.go:308](/Users/matthewsaw/base-proj/gount/go/main.go:308)` | Supply-chain and availability exposure | Move download to setup time, require checksum validation, allow offline provisioning. |
| Placeholder salt accepted | `[go/main.go:100](/Users/matthewsaw/base-proj/gount/go/main.go:100)` and startup path at `[go/main.go:1368](/Users/matthewsaw/base-proj/gount/go/main.go:1368)` | Predictable visitor IDs on weak deployments | Fail startup when salt is empty or unchanged. |
| OPTIONS reflects requested headers | `[go/main.go:1180](/Users/matthewsaw/base-proj/gount/go/main.go:1180)` | Unnecessary negotiation surface | Use fixed allow headers, or remove preflight support if not needed. |
| `/health` leaks internal state | `[go/main.go:1237](/Users/matthewsaw/base-proj/gount/go/main.go:1237)` | Information disclosure | Restrict methods and return generic unhealthy text externally. |
| SQL inserts are globally mutexed | `[go/main.go:577](/Users/matthewsaw/base-proj/gount/go/main.go:577)` | Throughput bottleneck | Keep locking only for SQLite recovery; let PostgreSQL/MySQL use pooled concurrent writes. |
| CSV/JSON retention rewrites full files | `[go/main.go:816](/Users/matthewsaw/base-proj/gount/go/main.go:816)`, `[go/main.go:947](/Users/matthewsaw/base-proj/gount/go/main.go:947)` | Poor scalability | Mark file stores as non-production or replace with append-only plus offline compaction. |
| Legacy GET/pixel docs | `[gount/README.md:39](/Users/matthewsaw/base-proj/gount/gount/README.md:39)` | User confusion, larger implied surface | Rewrite docs to show only `POST /t` with `navigator.sendBeacon()`. |
| Old demo still uses `GET` | `[testing/demo.html:180](/Users/matthewsaw/base-proj/gount/testing/demo.html:180)` | Broken testing path | Convert demo to `navigator.sendBeacon()` or delete it. |
| Old product-name/config remnants | `[testing/config.yaml:1](/Users/matthewsaw/base-proj/gount/testing/config.yaml:1)`, `[NOTES.md:8](/Users/matthewsaw/base-proj/gount/NOTES.md:8)` | Maintenance drift | Rename or remove stale files and unsupported config keys. |
| Load-test script uses GET | `[test_api.sh:7](/Users/matthewsaw/base-proj/gount/test_api.sh:7)` | Invalid benchmark coverage | Update script to `curl -X POST --data 'p=/idkyet'`. |

## Verification Notes

- Code audit performed against current workspace files under `/Users/matthewsaw/base-proj/gount`
- Go tests executed successfully: `go test ./...` in `/Users/matthewsaw/base-proj/gount/go`

