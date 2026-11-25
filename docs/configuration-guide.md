# Configuration and security tools, explained

An environment variable is a named setting supplied to the app when it starts. `.env` is the local settings file read by Docker Compose. Normally, copy [`.env.example`](../.env.example) once and use `make docker-up`. Keep an existing `.env`; replacing it can lose provider settings or encryption keys. Direct Go commands do not automatically load `.env`.

The example omits advanced settings whose previous values match application defaults. Throttling, token expiry, database timeouts, and worker limits remain active without those lines.

## Local settings

| Setting | Meaning | What to do locally |
| --- | --- | --- |
| `APP_ENV` | Selects development, test, or production validation. | Keep `development`. Production requires a separate setup. |
| `TENANT_BASE_DOMAIN` | Base domain used to identify stores from request hosts. | Change when your store domain changes. Defaults to `example.test`. |
| `AUTH_HOST` | Host for shared authentication routes. | Change when your authentication domain changes. |
| `CORS_ALLOWED_ORIGINS` | Exact frontend addresses allowed to make browser requests. | Match your frontend, such as `http://localhost:5173`; separate multiple origins with commas. This does not replace authentication. |
| `API_PORT` | Port exposed on your computer by local Compose. | Change if 8080 is occupied. |
| `LOG_LEVEL` | Amount of diagnostic logging. | Example uses `debug`; omitting it selects `info`. |
| `PLATFORM_ADMIN_TOKEN` | Credential for platform store provisioning, separate from shopper/store-owner logins. | Example is public and development-only; production rejects it. |
| `PAYMENT_CONFIG_KEYS` | Encrypts stored store payment configuration and related payment data. `1:<key>` means key version 1. | Keep the development key for existing local encrypted data. Production rejects it. |
| `PAYMENT_CONFIG_ACTIVE_KEY_VERSION` | Selects the payment key for encrypting new data. | Keep `1` with the example key. Changing this belongs to key rotation. |
| `DATABASE_URL` | PostgreSQL address, database, credentials, and connection security. | Compose substitutes the container address. |
| `REDIS_ADDRESS` | Redis address for rate limiting and temporary state/cache. | Compose substitutes the container address. |
| `KAFKA_BROKERS` | Kafka addresses for background event delivery. | Compose substitutes the container address. |
| `TEST_DATABASE_URL` | Separate disposable database for integration tests. | Must be loopback and end in `_test`. Never use customer data. |
| `ALLOW_TEST_DATABASE_MUTATION` | Permission for tests to change the guarded test database. | `true` enables local integration checks. |
| `EVENT_RETENTION_HOLD` | Blocks manual event cleanup when `true`. | Leave `true`; normal event processing continues. |

Domain settings do not create DNS records; local store requests still need the matching host. For a real deployment, use separate random 32-byte values encoded as unpadded base64url for the platform token and payment key; the payment key also needs its version prefix. Keep production secrets outside Git. Losing payment keys can make stored payment data unreadable; do not regenerate them on every restart. See [key rotation](operations-runbook.md#payment-encryption-key-rotation).

## Advanced settings you can leave alone

The variables below remain supported. The [configuration loader](../internal/platform/config/config.go) is the complete reference.

| Settings | Defaults and purpose |
| --- | --- |
| `TENANT_ROUTING_MODE`, `PLATFORM_HOST` | `multi_store`; platform host follows `TENANT_BASE_DOMAIN`. Single-store deployments additionally set `SINGLE_STORE_HOST` and `SINGLE_STORE_ID`. |
| `HTTP_ADDRESS` | `:8080`; local Compose supplies this inside containers. |
| `TRUSTED_PROXY_CIDRS` | Empty: forwarded client addresses are not trusted. Deployments behind a proxy must list only the actual proxy networks; this affects client rate limiting. |
| `ACCESS_TOKEN_TTL`, `SESSION_TTL` | Access tokens last 15 minutes; sessions last 30 days. TTL means how long something remains valid. |
| `PASSWORD_HASH_CONCURRENCY` | Two simultaneous password-hashing operations per process, bounding resource use. |
| `REGISTRATION_RATE_*`, `LOGIN_RATE_*`, `TOKEN_RATE_*`, `SOCIAL_RATE_*` | Limits per client address and identity. Login defaults: 20 per client and 5 per identity in 15 minutes. |
| `GUEST_CREATION_RATE_*`, `PLATFORM_PROVISION_RATE_*` | Guest creation: 20 per hour; store provisioning: 10 per hour. |
| `DATABASE_MAX_*`, `DATABASE_CONNECTION_*` | Pool: 20 open, 10 idle connections; 30-minute lifetime and 5-minute idle time. |
| `DATABASE_STATEMENT_TIMEOUT`, `DATABASE_LOCK_TIMEOUT`, `DATABASE_IDLE_TRANSACTION_TIMEOUT` | 15 seconds, 3 seconds, and 15 seconds; prevent database work from waiting indefinitely. |
| `SLOW_QUERY_THRESHOLD` | Slow-query duration logging from 500 milliseconds, without query text or parameters. |
| `EVENT_BATCH`, `EVENT_POLL`, `EVENT_CLAIM_LEASE`, `EVENT_MAX_ATTEMPTS`, `EVENT_RETRY_BASE`, `EVENT_RETRY_MAX` | 50 events, 1-second polling, 30-second claims, 12 attempts, retries from 1 second up to 5 minutes. |
| `INVENTORY_EXPIRY_*`, `CART_EXPIRY_*` | Batches of 50; inventory checks every 5 seconds, carts every minute. |
| `SHUTDOWN_TIMEOUT`, `DEPENDENCY_TIMEOUT`, `TENANT_RESOLUTION_TIMEOUT` | 10 seconds, 2 seconds, and 500 milliseconds. |
| `SOCIAL_TRANSACTION_TTL`, `SOCIAL_EXCHANGE_TTL`, `SOCIAL_RETURN_PATHS` | 10 minutes, 1 minute, and `/auth/social/complete`. |
| `MOMO_API_BASE_URL`, `MOMO_REDIRECT_URL`, `MOMO_PUBLIC_URL`, `MOMO_REQUEST_TIMEOUT` | Development uses MoMo sandbox, return/API URLs derived from the base domain, and a 35-second timeout. Live sandbox callbacks need a publicly reachable HTTPS API. |
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `GOOGLE_REDIRECT_URL` | Omit all three to disable Google login; provide the complete set to enable it. |
| `FACEBOOK_APP_ID`, `FACEBOOK_APP_SECRET`, `FACEBOOK_REDIRECT_URL`, `FACEBOOK_GRAPH_VERSION` | Omit credentials to disable Facebook login; enabling it needs the complete credential/redirect set. Graph version defaults to `v23.0`. |

## Security tools

These are check commands, not services you must keep running. Starting the shop does not start a scanner. Docker runs the release scanners on demand; no separate host installations are needed.

| Tool | Purpose | When this project uses it |
| --- | --- | --- |
| govulncheck | Finds known Go vulnerabilities that may be reachable by the code. | Included in `make docker-verify`; `make docker-vulncheck` runs it separately. Keep it. |
| Gitleaks | Finds accidentally committed secrets, including Git history. | `make secret-scan` and release-security CI. Keep it. |
| Trivy | Checks the built container image for known vulnerabilities. | `make container-scan` and release-security CI. Keep it for releases. |
| Syft / SBOM | Lists software packaged in the image to help investigate later vulnerabilities. An SBOM is an inventory, not a passing security scan. | `make sbom` and release-security CI. Leave automated; no daily setup. |
| ZAP baseline | Crawls reachable HTTP pages and reports passive security findings. It does not prove authenticated commerce or tenant isolation is safe. | `make zap-baseline`, manually against a disposable API. Required by the current release checklist, not daily development. |
| golangci-lint | Finds suspicious Go code and correctness issues. | Included in verification; code-quality checking rather than a replacement for security scans. |

Tool references: [Go vulnerability checking](https://go.dev/doc/security/vuln/), [Gitleaks](https://github.com/gitleaks/gitleaks), [Trivy](https://trivy.dev/docs/latest/guide/target/container_image/), [Syft](https://oss.anchore.com/docs/guides/sbom/), and [ZAP baseline](https://www.zaproxy.org/docs/docker/baseline-scan/). Project commands live in the [Makefile](../Makefile) and [release-security workflow](../.github/workflows/release-security.yml).

## Monitoring and production

Prometheus metrics tell you whether the app is healthy; Grafana displays dashboards; OpenTelemetry traces help follow a request through the app. They are monitoring tools, not vulnerability scanners. Local Compose does not start these external services or export traces unless configured. It enables an internal metrics listener without publishing that port to your computer.

Direct local runs can omit `METRICS_ADDRESS` and `OTEL_EXPORTER_OTLP_ENDPOINT`. The current production validator requires a metrics listener and an HTTPS trace-export endpoint; removing these production requirements would need a separate change.

Production also requires encrypted database connections (`sslmode=verify-full`), `REDIS_TLS_ENABLED=true`, `KAFKA_TLS_ENABLED=true`, and Kafka login credentials (`KAFKA_SASL_USERNAME` and `KAFKA_SASL_PASSWORD`). TLS encrypts a connection; SASL provides the Kafka login. Keep these production protections; local containers use development defaults.

Use [production deployment](production-deployment.md) and the separate [production environment example](../deploy/production.env.example) for deployment. Backup encryption (`age`), restore drills, payment-key rotation, and event reconciliation are occasional operator tools in the [runbook](operations-runbook.md); normal local development needs no extra setup for them. Production validation remains incomplete in [TASKS.md](../TASKS.md).
