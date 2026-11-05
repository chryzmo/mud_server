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

