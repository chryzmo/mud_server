DROP TABLE IF EXISTS item;

CREATE TABLE IF NOT EXISTS item
(
    id integer NOT NULL GENERATED ALWAYS AS IDENTITY ( INCREMENT 1 START 1 MINVALUE 1 MAXVALUE 2147483647 CACHE 1 ),
    name text COLLATE pg_catalog."default" NOT NULL,
    description text COLLATE pg_catalog."default",
	persistent boolean NOT NULL DEFAULT false,
	quantity integer NOT NULL DEFAULT 1,
    CONSTRAINT "ITEM_pkey" PRIMARY KEY (id)
);

INSERT INTO item (name,description,persistent, quantity) VALUES 
    ('KEY','A generic looking key.',false,1);

INSERT INTO item (name,description,persistent, quantity) VALUES 
    ('GOLD','Gold coin used for currency.',false,1);

ALTER TABLE IF EXISTS item
    OWNER to mud;

SELECT * FROM item;