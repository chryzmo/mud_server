DROP TABLE IF EXISTS player;

CREATE TABLE IF NOT EXISTS player
(
    id integer NOT NULL GENERATED ALWAYS AS IDENTITY ( INCREMENT 1 START 1 MINVALUE 1 MAXVALUE 2147483647 CACHE 1 ),
    username text NOT NULL,
    inventoryidslice JSON,
	attackpower integer, 
    defense integer,
    HP integer,
    MP integer,
    maxHP integer,
    maxMP integer,
    roomid integer,
    spawnlocationid integer,
    CONSTRAINT "PLAYER_pkey" PRIMARY KEY (id),
    CONSTRAINT fk_roomid FOREIGN KEY(roomid) REFERENCES room(id),
    CONSTRAINT fk_spawnlocationid FOREIGN KEY(roomid) REFERENCES room(id)
);

INSERT INTO player (username,inventoryidslice,attackpower,defense,hp,maxhp,mp,maxmp,roomid,spawnlocationid) VALUES
    ('c','{}',99,50,5,5,5,5,1,1);

ALTER TABLE IF EXISTS player
    OWNER to mud;

SELECT * FROM player;