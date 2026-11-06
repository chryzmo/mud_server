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

func CreateDatabase(db_url string, maxConns int) (MudDatabase) {
	var muddb MudDatabase

	ctx := context.Background()
	poolConfig, err := pgxpool.ParseConfig(db_url)
	if err != nil {
		panic(err)
	}
	poolConfig.MaxConns = int32(maxConns)
	db, err := pgxpool.NewWithConfig(ctx, poolConfig)

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
		// Guest sessions (plans/guest-sessions.md). The token and recovery code are
		// stored only as hashes; '' means the player has none (password accounts).
		"ALTER TABLE player ADD COLUMN IF NOT EXISTS guest boolean NOT NULL DEFAULT false",
		"ALTER TABLE player ADD COLUMN IF NOT EXISTS resumetokenhash text NOT NULL DEFAULT ''",
		"ALTER TABLE player ADD COLUMN IF NOT EXISTS recoveryhash text NOT NULL DEFAULT ''",
		"ALTER TABLE player ADD COLUMN IF NOT EXISTS lastseen timestamptz NOT NULL DEFAULT now()",
		"CREATE INDEX IF NOT EXISTS player_resumetoken_idx ON player (resumetokenhash) WHERE resumetokenhash <> ''",
		// World content (plans/event-systems.md): rooms, items, NPC templates and spawners that come
		// from content files are matched by a stable key, so a deploy updates them in place. NULL
		// means the row predates content or is not managed by it.
		"ALTER TABLE room ADD COLUMN IF NOT EXISTS contentkey text",
		"ALTER TABLE item ADD COLUMN IF NOT EXISTS contentkey text",
		"ALTER TABLE baddienpc_generic ADD COLUMN IF NOT EXISTS contentkey text",
		"ALTER TABLE baddiespawner ADD COLUMN IF NOT EXISTS contentkey text",
		"CREATE UNIQUE INDEX IF NOT EXISTS room_contentkey_key ON room (contentkey) WHERE contentkey IS NOT NULL",
		"CREATE UNIQUE INDEX IF NOT EXISTS item_contentkey_key ON item (contentkey) WHERE contentkey IS NOT NULL",
		"CREATE UNIQUE INDEX IF NOT EXISTS baddienpc_contentkey_key ON baddienpc_generic (contentkey) WHERE contentkey IS NOT NULL",
		"CREATE UNIQUE INDEX IF NOT EXISTS baddiespawner_contentkey_key ON baddiespawner (contentkey) WHERE contentkey IS NOT NULL",
		// The game master (plans/event-systems.md section 2): a flag on password accounts, a mute that
		// survives a restart, and an audit trail of every GM command.
		"ALTER TABLE player ADD COLUMN IF NOT EXISTS gm boolean NOT NULL DEFAULT false",
		"ALTER TABLE player ADD COLUMN IF NOT EXISTS muteduntil timestamptz",
		"CREATE TABLE IF NOT EXISTS gm_audit (id bigserial PRIMARY KEY, at timestamptz NOT NULL DEFAULT now(), gm text NOT NULL, command text NOT NULL, args text NOT NULL DEFAULT '', result text NOT NULL DEFAULT '')",
		// Things in a room that can be examined (plans/interactive-world.md); JSON, written by the content loader.
		"ALTER TABLE room ADD COLUMN IF NOT EXISTS featuresjson text NOT NULL DEFAULT '[]'",
		// Names are unique ignoring case. Last, because it fails if an older
		// database already holds two names that differ only in case.
		"CREATE UNIQUE INDEX IF NOT EXISTS player_username_lower_key ON player (lower(username))",
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

