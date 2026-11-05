package muddatabase

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MudDatabase struct {
	Context context.Context
	Connection	*pgxpool.Pool
}

func CreateDatabase(db_url string) (MudDatabase) {
	var muddb MudDatabase

	ctx := context.Background()
	db, err := pgxpool.New(ctx, db_url)

	if err != nil {
		panic(err)
	} else {
		muddb = MudDatabase{
			Context: ctx,
			Connection: db,
		}
	}

	return muddb
}

// WaitUntilReachable pings the database until it answers or the wait runs out.
func (db MudDatabase) WaitUntilReachable(wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		ctx, cancel := context.WithTimeout(db.Context, 2*time.Second)
		err := db.Connection.Ping(ctx)
		cancel()
		if err == nil || time.Now().After(deadline) {
			return err
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// EnsureSchema applies schema changes that a database created from older seed
// scripts may lack. Each statement is idempotent; failures are returned so the
// caller can log them without refusing to start.
func (db MudDatabase) EnsureSchema() error {
	statements := []string{
		// Settles two players registering the same name at once. Fresh databases
		// get it from src/sql/mudplayer.sql under the same name.
		"CREATE UNIQUE INDEX IF NOT EXISTS player_username_key ON player (username)",
	}
	for _, stmt := range statements {
		if _, err := db.Connection.Exec(db.Context, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (db MudDatabase) ExecWithArgs(query string, args pgx.NamedArgs) error {
	tx, err := db.Connection.Begin(db.Context)
	if err != nil {
		return err
	}

	defer tx.Rollback(db.Context)
	_, err = tx.Exec(db.Context, query, args)
	if err != nil {
		return err
	}

	err = tx.Commit(db.Context)
	if err != nil {
		return err
	}

	return nil
}

