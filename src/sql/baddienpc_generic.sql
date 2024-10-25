DROP TABLE IF EXISTS baddienpc_generic;

CREATE TABLE IF NOT EXISTS baddienpc_generic
(
    id integer NOT NULL GENERATED ALWAYS AS IDENTITY ( INCREMENT 1 START 1 MINVALUE 1 MAXVALUE 2147483647 CACHE 1 ),
    race text COLLATE pg_catalog."default" NOT NULL,
    attackaccuracy integer NOT NULL DEFAULT 0,
	attackpower integer NOT NULL DEFAULT 0,
	defense integer NOT NULL DEFAULT 0,
	hp integer NOT NULL DEFAULT 0,
	mp integer NOT NULL DEFAULT 0,
    combatTickerDurationInMS integer NOT NULL DEFAULT 1000,
	turnActionFunctionID integer DEFAULT NULL,
    CONSTRAINT "BADDIENPC_GENERIC_pkey" PRIMARY KEY (id)
);

ALTER TABLE IF EXISTS baddienpc_generic
    OWNER to mud;

INSERT INTO baddienpc_generic (race,attackaccuracy,attackpower,defense,hp,mp,combatTickerDurationInMS,turnActionFunctionID) VALUES
    ('ORC', 0,0,0,5,1,2000,1);