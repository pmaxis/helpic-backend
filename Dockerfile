# --- build stage ---------------------------------------------------------
FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/pdf ./cmd/pdf

# --- pdf service (target: pdf) ---------------------------------------------
FROM alpine:3.20 AS pdf

# chromium prints report PDFs; font-inter matches the report template and
# font-dejavu covers any glyph Inter lacks.
RUN apk add --no-cache chromium font-inter font-dejavu && adduser -D -u 10001 app

WORKDIR /app
COPY --from=build /out/pdf /app/pdf

ENV CHROME_PATH=/usr/bin/chromium-browser
ENV PDF_PORT=8081
USER app

EXPOSE 8081
ENTRYPOINT ["/app/pdf"]

# --- API (target: api, the default) ----------------------------------------
FROM alpine:3.20 AS api

RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app \
    && mkdir -p /app/uploads && chown app /app/uploads

WORKDIR /app
COPY --from=build /out/server /app/server
COPY --from=build /src/migrations /app/migrations

ENV MIGRATIONS_DIR=/app/migrations
# Task attachments; mount a volume here to keep them.
ENV UPLOADS_DIR=/app/uploads
USER app

EXPOSE 8080
ENTRYPOINT ["/app/server"]
