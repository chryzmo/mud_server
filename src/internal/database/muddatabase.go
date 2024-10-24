package muddatabase

import (
	"context"

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

