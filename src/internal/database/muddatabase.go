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
		// The friendly characters in a room (plans/interactive-world.md section 4); JSON, written by the content loader.
		"ALTER TABLE room ADD COLUMN IF NOT EXISTS peoplejson text NOT NULL DEFAULT '[]'",
		// Rules, gates and puzzle tokens (plans/interactive-world.md sections 3 and 5), and each player's puzzle state.
		// There is deliberately no foreign key from player_state to player: the seed scripts drop player.
		"ALTER TABLE room ADD COLUMN IF NOT EXISTS rulesjson text NOT NULL DEFAULT '[]'",
		"ALTER TABLE room ADD COLUMN IF NOT EXISTS gatesjson text NOT NULL DEFAULT '{}'",
		"ALTER TABLE item ADD COLUMN IF NOT EXISTS bound boolean NOT NULL DEFAULT false",
		"CREATE TABLE IF NOT EXISTS player_state (player_id integer NOT NULL, kind text NOT NULL, key text NOT NULL, value text NOT NULL DEFAULT '', PRIMARY KEY (player_id, kind, key))",
		// World flags set by events or the GM, and where each scheduled event has got to (plans/event-systems.md section 4).
		"CREATE TABLE IF NOT EXISTS world_flag (key text PRIMARY KEY)",
		"CREATE TABLE IF NOT EXISTS event_state (event_key text PRIMARY KEY, occurrence integer NOT NULL, phase text NOT NULL, started_at timestamptz, step integer NOT NULL DEFAULT 0)",
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

