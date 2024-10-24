package mudserver

import (
	"errors"
	"fmt"
	muddatabase "mud_server/src/internal/database"

	"github.com/jackc/pgx/v5"
)

type Item struct {
	ID int32
	Name string
	Quantity int
	Persistent bool
}

// Create a copy of the item.  May be useful for breaking up stacks
func (i Item) createCopy() (*Item) {
	return &Item{
		ID: i.ID,
		Name: i.Name,
		Quantity: i.Quantity,
		Persistent: i.Persistent,
	}
}

func (i Item) FetchFromDB (db muddatabase.MudDatabase, itemid  interface{}) (*Item) {
	query := ""
	var err error

	switch itemid.(type) {
	case string:
		query = "SELECT id,name,quantity,persistent FROM ITEM WHERE name=$1"
	case int, int8, int32, int64:
		query = "SELECT id,name,quantity,persistent FROM ITEM WHERE id=$1"
	default:
		err = errors.New("Item.FetchFromDB invalid ID.  ID must be string|int32")
	}

	rows,err := db.Connection.Query(db.Context,query,itemid)

	if err != nil {
		fmt.Println("Item error: "+err.Error())
	}

	ret,err := pgx.CollectOneRow(rows,pgx.RowToStructByName[Item])

	if err != nil {
		fmt.Println("Item error: "+err.Error())
	}

	return &ret
}