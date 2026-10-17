package database

import (
	"errors"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

// Direction of a migration run.
type Direction string

const (
	Up   Direction = "up"
	Down Direction = "down"
)

// Migrate runs SQL migrations from dir against the database at databaseURL.
// Up applies all pending migrations; Down rolls every migration back.
func Migrate(databaseURL, dir string, direction Direction) error {
	m, err := migrate.New("file://"+dir, toMigrateURL(databaseURL))
	if err != nil {
		return fmt.Errorf("open migrations: %w", err)
	}
	defer m.Close()

	switch direction {
	case Up:
		err = m.Up()
	case Down:
		err = m.Down()
	default:
		return fmt.Errorf("unknown migration direction %q", direction)
	}
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}

// toMigrateURL rewrites the scheme so the golang-migrate pgx/v5 driver picks it
// up while the rest of the app keeps using the standard postgres:// URL.
func toMigrateURL(databaseURL string) string {
	for _, prefix := range []string{"postgres://", "postgresql://"} {
		if strings.HasPrefix(databaseURL, prefix) {
			return "pgx5://" + strings.TrimPrefix(databaseURL, prefix)
		}
	}
	return databaseURL
}
