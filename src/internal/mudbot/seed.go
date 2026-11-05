package mudbot

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

type SQLExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// SeedPlayers is a load-test fixture, not an alternative login/authentication
// path. Hash once at bcrypt's minimum cost so steady-state tests don't become
// CPU benchmarks of password verification. Never use these accounts in production.
// Existing names cause an error instead of silently reusing different passwords.
func SeedPlayers(ctx context.Context, db SQLExecutor, count int, prefix, password string) error {
	if count < 1 || count > 100000 || !validPrefix.MatchString(prefix) || password == "" || len(password) > 72 || strings.ContainsAny(password, " \t\r\n") {
		return errors.New("invalid bot seed count, prefix or password")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `INSERT INTO player
		(username,passwordhash,inventoryidslice,equipmentslots,attackpower,defense,hp,maxhp,mp,maxmp,roomid,spawnlocationid)
		SELECT $2 || lpad(i::text, 5, '0'), $3, '{}', '{}', 5, 10, 20, 20, 10, 10, 1, 1
		FROM generate_series(0, $1::integer - 1) AS i`, count, prefix, string(hash))
	return err
}

func validateSeedURL(databaseURL string) error {
	u, err := url.Parse(databaseURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || !isLoopback(u.Hostname()) || u.Path != "/mud_load" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("seed target must be a loopback postgres URL for mud_load with no query overrides")
	}
	return nil
}

// ResetLocal creates (if missing), drops/reseeds the schema in, and populates the
// dedicated local mud_load database. It refuses every other database/host and
// connection query overrides. The caller must explicitly request a reset.
func ResetLocal(ctx context.Context, databaseURL, sqlDirectory string, count int, prefix, password string) error {
	if err := validateSeedURL(databaseURL); err != nil {
		return err
	}
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return errors.New("invalid seed database configuration")
	}
	admin := config.Copy()
	admin.Database = "postgres"
	conn, err := pgx.ConnectConfig(ctx, admin)
	if err != nil {
		return err
	}
	var exists bool
	err = conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'mud_load')").Scan(&exists)
	if err == nil && !exists {
		_, err = conn.Exec(ctx, "CREATE DATABASE mud_load")
	}
	conn.Close(ctx)
	if err != nil {
		return err
	}
	conn, err = pgx.ConnectConfig(ctx, config)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	order, err := os.ReadFile(filepath.Join(sqlDirectory, "load_order.txt"))
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(order), "\n") {
		name := strings.TrimSpace(line)
		if name == "" || strings.HasPrefix(name, "#") {
			continue
		}
		if filepath.Base(name) != name {
			return fmt.Errorf("invalid SQL filename: %q", name)
		}
		script, err := os.ReadFile(filepath.Join(sqlDirectory, name))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(script)); err != nil {
			return fmt.Errorf("seed %s: %w", name, err)
		}
	}
	if err := SeedPlayers(ctx, tx, count, prefix, password); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
