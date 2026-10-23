# --- build stage ---------------------------------------------------------
FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/cli ./cmd/cli

# --- API ---------------------------------------------------------------------
FROM alpine:3.20 AS api

RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app \
    && mkdir -p /app/uploads && chown app /app/uploads

WORKDIR /app
COPY --from=build /out/server /app/server
# Operator jobs: docker compose exec backend /app/cli <command>
COPY --from=build /out/cli /app/cli
COPY --from=build /src/migrations /app/migrations

ENV MIGRATIONS_DIR=/app/migrations
# Task attachments; mount a volume here to keep them.
ENV UPLOADS_DIR=/app/uploads
USER app

EXPOSE 8080
ENTRYPOINT ["/app/server"]
