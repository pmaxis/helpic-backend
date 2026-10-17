# Helpic — backend

Go API for Helpic — a platform where freelancers get tasks from their clients. Runs in its own Docker Compose project together with PostgreSQL; the frontend lives in [`../frontend`](../frontend) with its own `.env` and containers.

## Stack

Go 1.26, chi, pgx, golang-migrate, JWT in an httpOnly cookie, PostgreSQL 16. Integration tests run against Testcontainers.

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

Migrations run automatically when the server starts. After changing Go code: `docker compose up -d --build backend`.

## How it works

- Public sign-up always creates a **freelancer**.
- A freelancer invites a **client** by email and gets a one-time link (valid 14 days). The client opens it and sets a password — or, if they already have a client account (invited by another freelancer), signs in. The two are then linked; a client can work with several freelancers.
- Either side creates **tasks** for the other: a client for a linked freelancer, a freelancer for a linked client. A freelancer can also keep **personal tasks** with no client, which only they see (`client` is `null`; filter with `?with=personal`). Statuses: `new → in_progress → done`, plus `cancelled`.
  - the task's creator and its freelancer edit it in any status (the history keeps every change); the creator deletes it before work has started;
  - the freelancer moves tasks freely between new, in progress and done, and cancels or restores tasks they created;
  - a personal task can go to any status;
  - the client cancels or restores any task and sends a done task back to work.

- **Client teams**: the client a freelancer invited owns a company and brings in its people — employees or contractors, one *member* role for now — by an invitation link (members join with a new account). Members act for the company: they see its freelancers, projects, tasks and boards, and set tasks for its freelancers (they're the tasks' creators; the company's owner is the client). Reports stay the owner's alone. In SQL, `acting_client(user)` is the client a user acts as — the owner for a member, the user themselves otherwise. A member can't also be a client of their own, and a removed member sees none of the company's work.

- **Projects** group tasks. Like tasks, a project is shared between a linked freelancer and client, or is a freelancer's personal project; either side creates one, and its creator edits or deletes it. A task in a project takes the project's freelancer and client. Deleting a project keeps its tasks — they just leave the project.

- **Boards work like Jira.** Every freelancer–client pair — and a freelancer's personal work — has one shared board. Its columns are the pair's workflow **statuses** (New, In progress, Done, Cancelled to start with; add Review, QA… as needed); a task's status is the column it's in, and both sides see the same status and card order. Each status belongs to a **category** — new, in progress, done or cancelled — which drives the role rules above, open counts and the list filters. Only the freelancer manages the columns (add, rename, recategorise, reorder, delete — an empty column can always go, one with tasks only when another column of its category can take them, and a board keeps at least one column; new tasks land in the first "new" column, or the first column if there is none); both sides move cards. A freelancer also has an **all** board (`with=all`): every task of every client and their personal ones, the boards' columns merged by category and name; a card dropped on a lane goes to its own board's column of that name, or its first column of that category. Any board takes a `projects=id,…|none` filter.

- **Notifications** tell a user what the other side of their shared work did: added, moved, edited or deleted a task, started a project, accepted an invitation. Personal work and your own actions notify nobody. A notification never fails the change it reports.

- **Priority and labels**: a task's priority is urgent, high, medium, low or none; either side sets it, in any status. Labels (a name and a color from a palette) belong to a pair's board, like its columns, but both sides make, rename and delete them and put them on tasks. Cards on the board and rows in the list show both.

- **History**: every task keeps who changed what and when — created it, renamed it, edited the description, moved the due date, the project or the status, set the price, added or removed a file. Names are stored as they were at the time. Tasks from before history existed start with their creation.

- **Comments**: the discussion on a task. Both sides comment; only the author edits or deletes a comment (an edited one is marked). The other side is notified with the start of the text.

- **Attachments**: files on a task — mockups, specs, results. Both sides add and download them; only the uploader deletes one, and deleting the task removes its files. Files are stored on disk (`UPLOADS_DIR`, a Docker volume), up to `MAX_UPLOAD_MB` (25) each. The server reads a file's type from its bytes; files are always served as downloads, except PNG/JPEG/GIF/WebP images, which may be previewed (`?inline=1`).

- **Paying a report**: once a report is sent, its client marks it paid. The report becomes paid, and its tasks move to the pair's **Paid** board column — a status of the done category, made right after the last done column on the first payment. A task a paid report covers isn't put in a later report again. Each report keeps a history (made, renamed, summary, type, notes, tasks dropped, sent, paid); its client sees it from the sending on.

- **Prices**: a freelancer prices their tasks — any of them, in any status, including ones the client created; the client sees the price but can't change it. Prices are in the freelancer's currency, set in their profile. A report lists the prices and sums them: a draft follows the tasks' current prices, and sending it freezes the prices and the currency.

- **Reports**: a freelancer reports the work done for a client over a period (optionally only some of their projects) — or, without a client, their personal work, a report that's only downloaded, never sent. A report snapshots the tasks completed in the period — tasks record when they entered a "done" column — plus a summary. A report is basic (the list of tasks) or detailed: each task also says what was done in it, in a note that starts as the task's description and is rewritten in the draft without touching the task. The draft is the freelancer's alone to edit (title, summary, type, notes, drop tasks); sending it notifies the client, who can then read it, and freezes it. Either side saves it as PDF through the browser's print dialog (the page has print styles).

The role is carried in the JWT; `middleware.RequireRole` guards role-only routes.

## API

The API is versioned in the path: everything lives under **`/api/v1`**. A breaking change gets `/api/v2` next to it, so clients move over when they're ready. `/health` stays unversioned.

The endpoints, their parameters, bodies, responses and errors are described with **Swagger**, generated from the handlers' comments:

- UI: http://localhost:8090/api/docs — sign in with `POST /auth/login` first; the auth cookie goes along with the next requests
- spec: `/api/docs/doc.json`, and `docs/swagger.yaml` in the repo

After changing a handler or its comment, run `make swagger` to regenerate `docs/`. The UI is on outside production; set `SWAGGER_ENABLED` to override.

## Commands

```sh
make help           # list all commands
make up / down      # start / stop postgres + backend
make logs           # follow container logs
make test           # all Go tests (needs a running Docker daemon)
make test-unit      # unit tests only
make test-integration # integration tests only (needs Docker)
make psql           # database shell
make migrate-up     # apply migrations manually
make fmt            # go fmt
make swagger        # regenerate the API docs (docs/)
```

## Configuration

All variables are documented in [`.env.example`](.env.example). Always set your own `JWT_SECRET`; `CORS_ORIGIN` must match the frontend's origin.

## Layout

```
cmd/server/          entrypoint (server, migrate up|down)
internal/
  auth, user         registration, login, current user, roles
  profile            account settings: name, email, password, currency
  clients            invitations, freelancer–client links
  team               a client's company and its members
  projects           projects grouping tasks
  tasks              tasks and their status rules
  attachments        files on tasks, stored on disk
  comments           the discussion on a task
  board              shared Jira-style boards: workflow statuses and card order
  notifications      what the other side did, per recipient
  reports            work reports sent to clients
  money              prices: parsing amounts, currencies, formatting
  server             wiring and routes
  config, database, middleware, response, token
  request            the signed-in user and the JSON body, for every handler
migrations/          SQL migrations
tests/unit/          unit tests, laid out like internal/
tests/integration/   integration tests (Testcontainers)
```

Add a feature as a new package under `internal/` (model, repository, handler with `Routes(r chi.Router)`) and wire it in `internal/server/server.go`.
