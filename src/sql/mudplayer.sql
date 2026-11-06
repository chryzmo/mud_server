DROP TABLE IF EXISTS player_state; -- puzzle state is keyed by player id, and ids start again when the seed is reloaded
DROP TABLE IF EXISTS player;

CREATE TABLE IF NOT EXISTS player
(
    id integer NOT NULL GENERATED ALWAYS AS IDENTITY ( INCREMENT 1 START 1 MINVALUE 1 MAXVALUE 2147483647 CACHE 1 ),
    username text NOT NULL,
    passwordhash text NOT NULL DEFAULT '',
    inventoryidslice JSON,
    equipmentslots JSON NOT NULL DEFAULT '{}',
	attackpower integer,
    defense integer,
    HP integer,
    MP integer,
    maxHP integer,
    maxMP integer,
    roomid integer,
    spawnlocationid integer,
    guest boolean NOT NULL DEFAULT false,
    resumetokenhash text NOT NULL DEFAULT '',
    recoveryhash text NOT NULL DEFAULT '',
    lastseen timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT "PLAYER_pkey" PRIMARY KEY (id),
    -- Same name as the index the server creates on older databases at startup.
    CONSTRAINT player_username_key UNIQUE (username),
    CONSTRAINT fk_roomid FOREIGN KEY(roomid) REFERENCES room(id),
    CONSTRAINT fk_spawnlocationid FOREIGN KEY(spawnlocationid) REFERENCES room(id)
);

CREATE UNIQUE INDEX IF NOT EXISTS player_username_lower_key ON player (lower(username));
CREATE INDEX IF NOT EXISTS player_resumetoken_idx ON player (resumetokenhash) WHERE resumetokenhash <> '';

INSERT INTO player (username,passwordhash,inventoryidslice,equipmentslots,attackpower,defense,hp,maxhp,mp,maxmp,roomid,spawnlocationid) VALUES
    ('c','','{}','{}',5,10,20,20,10,10,1,1);

ALTER TABLE IF EXISTS player
    OWNER to mud;

SELECT * FROM player;