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
| API | http://localhost:8090/api |
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

| Method | Path | Who |
|---|---|---|
| POST | `/api/auth/register` | public (creates a freelancer) |
| POST | `/api/auth/login`, `/api/auth/logout` | public |
| GET | `/api/me` | signed in |
| PUT | `/api/me` (name), `/api/me/email`, `/api/me/password` | signed in; email and password need `current_password` |
| PUT | `/api/me/currency` `{currency}` (USD, EUR, GBP, UAH, PLN, CAD) | signed in; a freelancer's billing currency |
| GET, PUT | `/api/team` (PUT `{name}`) | client; PUT the company's owner |
| POST | `/api/team/invites` `{email, name?}` | the company's owner |
| DELETE | `/api/team/invites/{id}`, `/api/team/members/{id}` | the company's owner |
| GET, POST | `/api/team-invites/{token}` (POST `…/accept` `{name, password, password_confirmation}`) | public: the join page |
| GET | `/api/invites/{token}` | public — invitation page |
| POST | `/api/invites/{token}/accept` | public — creates/signs in the client |
| GET, POST | `/api/invitations` | freelancer |
| DELETE | `/api/invitations/{id}` | freelancer |
| GET | `/api/clients` | freelancer |
| GET | `/api/freelancers` | client |
| GET, POST | `/api/projects` (`?with={userID\|personal}`) | both — create like a task: `freelancer_id` / `client_id` |
| GET | `/api/projects/{id}` | both |
| PUT, DELETE | `/api/projects/{id}` | the project's creator |
| GET | `/api/tasks?status=new,in_progress&with={userID\|personal}&project={id}` | both |
| GET | `/api/tasks/{id}` | both |
| POST | `/api/tasks` | both — a client sends `freelancer_id`, a freelancer `client_id` (omit it for a personal task); with `project_id` the project decides both |
| PUT, DELETE | `/api/tasks/{id}` | PUT: the task's creator or freelancer; DELETE: the creator, while it's new |
| PUT | `/api/tasks/{id}/price` `{price: "1200.50" \| null}` | the task's freelancer (a freelancer may also pass `price` on create) |
| GET, POST | `/api/comments?task={id}` (POST `{body}`) | the task's sides |
| PUT, DELETE | `/api/comments/{id}` (PUT `{body}`) | the comment's author |
| GET, POST | `/api/attachments?task={id}` (POST: multipart, one `file` part) | the task's sides |
| GET, DELETE | `/api/attachments/{id}` (GET `?inline=1` previews an image) | the task's sides; DELETE the uploader |
| GET | `/api/tasks/{id}/history` | the task's sides |
| PUT | `/api/tasks/{id}/priority` `{priority}`, `/api/tasks/{id}/labels` `{label_ids}` | the task's sides |
| GET, POST | `/api/board/labels?with=…&project=…` (POST `{with?, project_id?, name, color}`) | the board's sides |
| PUT, DELETE | `/api/board/labels/{id}` (PUT `{name, color}`) | the board's sides |
| POST | `/api/tasks/{id}/status` `{status_id}` (or `{status}`: a category's first column) | both, per the rules above |
| GET | `/api/board?with={clientID\|personal\|freelancerID}` or `?project={id}` | both — the shared board's columns (statuses) with tasks; `can_manage` for the freelancer |
| GET | `/api/board/columns` (same query) | both — columns only |
| POST | `/api/board/columns` `{with \| project_id, name, category}` | freelancer |
| PUT | `/api/board/columns` `{"ids": [...]}` (reorder) | freelancer |
| PUT, DELETE | `/api/board/columns/{id}` | freelancer |
| POST | `/api/board/moves` `{task_id, column_id, after_task_id}` | both |
| GET | `/api/reports`, `/api/reports/{id}` | both — a client sees sent reports only |
| POST | `/api/reports` `{client_id, project_ids?, from, to, title?, summary?, kind?: basic\|detailed}` | freelancer |
| PUT | `/api/reports/{id}` `{title, summary, kind?}` | freelancer, drafts only |
| DELETE | `/api/reports/{id}` | freelancer, sent or not — a sent report is gone for the client too |
| PUT, DELETE | `/api/reports/{id}/items/{itemID}` (PUT `{notes}`) | freelancer, drafts only |
| POST | `/api/reports/{id}/send` | freelancer |
| POST | `/api/reports/{id}/paid` | the report's client, once it's sent |
| GET | `/api/reports/{id}/history` | the report's sides (a client sees sent and paid) |
| GET | `/api/notifications` (latest 50 + `unread`) | both |
| GET | `/api/notifications/unread-count` | both — cheap, for polling |
| POST | `/api/notifications/{id}/read`, `/api/notifications/read-all` | both |

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
