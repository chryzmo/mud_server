DROP TABLE IF EXISTS equipment;
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

/* Equippable items — IDs 3-10 assuming KEY=1, GOLD=2 */
INSERT INTO item (name,description,persistent,quantity) VALUES ('IRON SWORD','A well-balanced iron sword.',true,1);
INSERT INTO item (name,description,persistent,quantity) VALUES ('LEATHER CAP','A simple cap made of cured leather.',true,1);
INSERT INTO item (name,description,persistent,quantity) VALUES ('LEATHER ARMOR','A suit of cured leather armor.',true,1);
INSERT INTO item (name,description,persistent,quantity) VALUES ('LEATHER LEGGINGS','Leg protection made from cured leather.',true,1);
INSERT INTO item (name,description,persistent,quantity) VALUES ('LEATHER GLOVES','Lightweight gloves of cured leather.',true,1);
INSERT INTO item (name,description,persistent,quantity) VALUES ('WOODEN SHIELD','A sturdy shield banded with iron.',true,1);
INSERT INTO item (name,description,persistent,quantity) VALUES ('COPPER RING','A plain ring of hammered copper.',true,1);
INSERT INTO item (name,description,persistent,quantity) VALUES ('SILVER PENDANT','A pendant strung on a fine silver chain.',true,1);

ALTER TABLE IF EXISTS item
    OWNER to mud;

SELECT * FROM item;