# Helpic — backend

Go API for Helpic: freelancers and their clients on the same page — tasks, projects, boards and reports. The frontend lives in [`../frontend`](../frontend).

**Stack:** Go 1.26, chi, pgx, golang-migrate, PostgreSQL 16, JWT in an httpOnly cookie. Integration tests run against Testcontainers.

## Getting started

Requires Docker.

```sh
cp .env.example .env      # then set your own JWT_SECRET
make up                   # or: docker compose up -d --build
```

| Service | URL |
|---|---|
| API | http://localhost:8090/api/v1 |
| API docs (Swagger) | http://localhost:8090/api/docs |
| Health | http://localhost:8090/health |
| PostgreSQL | `localhost:5435` |

Migrations run when the server starts.

## API

Versioned in the path: everything lives under `/api/v1` (`/health` stays unversioned).

Endpoints, parameters, bodies and errors are in **Swagger** at `/api/docs`, generated from the handlers' comments — run `make swagger` after changing them. Sign in with `POST /auth/login` first; the cookie goes along with the next requests. The UI is off in production unless `SWAGGER_ENABLED` is set.

## Commands

```sh
make help              # list all commands
make up / down         # start / stop postgres + backend
make logs              # follow container logs
make test              # all tests (needs Docker)
make test-unit         # unit tests only
make test-integration  # integration tests only (needs Docker)
make swagger           # regenerate the API docs (docs/)
make psql              # database shell
make fmt               # go fmt
```

## Configuration

All variables are documented in [`.env.example`](.env.example). Always set your own `JWT_SECRET`; `CORS_ORIGIN` must match the frontend's origin.

## Layout

```
cmd/server/          entrypoint (server, migrate up|down)
docs/                generated Swagger spec
internal/            one package per feature, plus shared infrastructure
migrations/          SQL migrations
tests/unit/          unit tests, laid out like internal/
tests/integration/   integration tests (Testcontainers)
```

A feature is a package under `internal/` (model, repository, service, handler with `Routes`), wired in `internal/server/server.go`.
