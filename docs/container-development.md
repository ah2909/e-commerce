# Container development

Docker Compose can run the API, worker, migrations, and Go checks without a host Go installation. The frontend continues to run separately. The existing production deployment uses the same final image from `Dockerfile`.

Run `make` to list the four everyday Docker commands. Direct check targets remain available for CI and tools containers. Application settings live in `.env`; the Makefile does not duplicate them.

All local services are defined in `compose.yml`. Without a profile, Compose starts PostgreSQL, Redis, Kafka, and topic initialization. The `app` profile adds migrations, API, and worker; the `tools` profile enables the tools and verification jobs. Use `docker compose run --rm tools <command>` or `docker compose run --rm verify` for one-off checks. Production uses the separate `deploy/compose.production.yml` file with external infrastructure and injected secrets.

## Start and stop

Keep your existing `.env`. On a fresh checkout, copy `.env.example` to `.env` and configure local values first. Stop any host API using port 8080 and any host worker before starting their container equivalents.

```bash
docker compose --profile app up -d --build --wait --wait-timeout 120
docker compose logs -f api worker
docker compose --profile app --profile tools stop
```

These are also available as `make docker-up` and `make docker-stop`. The API is published only on `127.0.0.1:8080`; its readiness endpoint is `/health/ready`. Set `API_PORT=8081` in `.env` to use another host port when 8080 is occupied. The API still listens on 8080 inside its container. Migrations complete before the API starts, and the worker waits for API readiness and Kafka topic initialization. The API does not depend on Kafka readiness.

Compose reads application settings from `.env` and replaces dependency addresses with container-network addresses. Host routing, CORS origins, payment keys, and provider settings still come from your `.env`. Configure `CORS_ALLOWED_ORIGINS` for the frontend origin you actually use.

The project name remains `ecommerce-dev`, so the existing PostgreSQL, Redis, and Kafka volumes are reused. Stopping or rebuilding application containers retains the data. `down -v` deletes the project volumes, including database data.

Fresh PostgreSQL volumes create `ecommerce_test` through the versioned script in `docker/postgres/init/`. Initialization scripts run only when the data directory is empty. Keep these bootstrap files in Git so fresh checkouts and CI receive the same databases as local development.

## Check and rebuild

```bash
docker compose run --build --rm --no-deps tools vulncheck
docker compose run --build --rm verify
```

The shortcuts are `make docker-vulncheck` and `make docker-verify`. For individual checks, use `run --build --rm tools fmt-check`, `tools lint`, or `tools test` with the local Compose file. Use `run --build --rm verify test-integration` for integration tests. Unit tests use Linux's race detector. Source is mounted from the checkout; module downloads, build artifacts, and Linux check tools use separate Docker cache volumes. Running `fmt` changes the mounted source files.

The verification container shares PostgreSQL's network namespace so the existing test-database loopback guard remains enforced. Integration checks migrate and mutate only `ecommerce_test`, using the existing explicit mutation opt-in. Run one verification process at a time against that test database. The standalone tools container runs vulnerability, formatting, lint, and unit checks without starting infrastructure.

After editing backend code, run the start command again to rebuild and recreate the application containers. There is no automatic reload. To inspect migrations without applying them:

```bash
docker compose run --rm migrate status
```

## Update Go

1. Change the exact Go version in `go.mod` and the `golang` image version and digest in `Dockerfile` together. The `toolchain` stage is shared by runtime builds and local checks; `GOTOOLCHAIN=local` makes a mismatch fail instead of downloading another compiler.
2. Update affected module versions with the tools container, using `--entrypoint go` and `mod tidy` or `get module@version` in place of the Make command.
3. Run `make docker-verify`, then rebuild with `make docker-up` and confirm API readiness.

CI selects Go from `go.mod`. Production releases must build and scan a new application image and update their deployment image digest; follow [production deployment](production-deployment.md) for those operational steps.
