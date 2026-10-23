# Helpic — backend

Go API for Helpic, a workspace for freelancers and their clients: tasks, projects, boards, time tracking and reports. Frontend: [helpic-frontend](https://github.com/pmaxis/helpic-frontend).

**Stack:** Go 1.26, chi, pgx, PostgreSQL 16, golang-migrate. Auth is a short-lived JWT plus a rotating refresh token, both in httpOnly cookies, with optional two-factor sign-in.

## Run

Requires Docker.

```sh
cp .env.example .env    # set your own JWT_SECRET
make up
```

| | |
|---|---|
| API | http://localhost:8090/api/v1 |
| Swagger | http://localhost:8090/api/docs |
| Mailpit (dev email) | http://localhost:8025 |
| PostgreSQL | `localhost:5435` |

Migrations run on start.

## Configuration

Every variable is documented in [`.env.example`](.env.example). `CORS_ORIGIN` must match the frontend's origin. `ADMIN_EMAILS` opens the admin panel to those accounts; they also need two-factor sign-in on.

With `APP_ENV=production` the server refuses to start on unsafe settings: weak secrets, insecure cookies, a database without TLS verification, Mailpit, or Swagger left on.

## Commands

```sh
make up / down          # start / stop the stack
make test               # all tests (integration tests use Testcontainers)
make swagger            # regenerate docs/ after changing handler comments
make psql               # database shell
make help               # everything else
```

## Layout

```
cmd/server/         API server; `server migrate up|down`
cmd/cli/            operator commands, one file each in commands/
internal/           a package per feature (model, repository, service, handler), wired in internal/server
migrations/         SQL migrations
tests/              unit and integration tests
docs/               generated Swagger spec
```
