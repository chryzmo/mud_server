DROP TABLE IF EXISTS baddiespawner;

CREATE TABLE IF NOT EXISTS baddiespawner
(
    id integer NOT NULL GENERATED ALWAYS AS IDENTITY ( INCREMENT 1 START 1 MINVALUE 1 MAXVALUE 2147483647 CACHE 1 ),
    totalnumber integer NOT NULL DEFAULT 0,
    tickdurationms integer NOT NULL DEFAULT 0,
	baddieid integer, /* Figure out if we need to split out generic/named */
    roomid integer,
    CONSTRAINT "BADDIESPAWNER_pkey" PRIMARY KEY (id),
    CONSTRAINT fk_room FOREIGN KEY(roomid) REFERENCES room(id)
);

INSERT INTO baddiespawner (totalnumber,tickdurationms,baddieid,roomid) VALUES
    (1,3000,1,3);

ALTER TABLE IF EXISTS baddiespawner
    OWNER to mud;