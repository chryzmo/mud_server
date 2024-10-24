package muddatabase

import (
	"context"

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

