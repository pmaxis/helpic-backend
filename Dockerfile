# --- build stage ---------------------------------------------------------
FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# --- runtime stage ------------------------------------------------------------
FROM alpine:3.20

# chromium prints report PDFs; font-inter matches the report template and
# font-dejavu covers any glyph Inter lacks.
RUN apk add --no-cache ca-certificates chromium font-inter font-dejavu && adduser -D -u 10001 app \
    && mkdir -p /app/uploads && chown app /app/uploads

WORKDIR /app
COPY --from=build /out/server /app/server
COPY --from=build /src/migrations /app/migrations

ENV MIGRATIONS_DIR=/app/migrations
ENV CHROME_PATH=/usr/bin/chromium-browser
# Task attachments; mount a volume here to keep them.
ENV UPLOADS_DIR=/app/uploads
USER app

EXPOSE 8080
ENTRYPOINT ["/app/server"]
