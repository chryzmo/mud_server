DROP TABLE IF EXISTS equipment;

CREATE TABLE IF NOT EXISTS equipment
(
    id                integer NOT NULL GENERATED ALWAYS AS IDENTITY ( INCREMENT 1 START 1 MINVALUE 1 MAXVALUE 2147483647 CACHE 1 ),
    itemid            integer NOT NULL,
    slot              text COLLATE pg_catalog."default" NOT NULL,
    bonushp           integer NOT NULL DEFAULT 0,
    bonusmaxhp        integer NOT NULL DEFAULT 0,
    bonusmp           integer NOT NULL DEFAULT 0,
    bonusmaxmp        integer NOT NULL DEFAULT 0,
    bonusattackpower  integer NOT NULL DEFAULT 0,
    bonusdefense      integer NOT NULL DEFAULT 0,
    CONSTRAINT "EQUIPMENT_pkey" PRIMARY KEY (id),
    CONSTRAINT fk_item FOREIGN KEY (itemid) REFERENCES item(id)
);

/* Slots: weapon, shield, helmet, chest, legs, hands, ring, neck */
INSERT INTO equipment (itemid, slot, bonusattackpower, bonusdefense, bonushp, bonusmaxhp, bonusmp, bonusmaxmp) VALUES
    ((SELECT id FROM item WHERE name='IRON SWORD'),      'weapon', 5,  0,  0,  0,  0,  0),
    ((SELECT id FROM item WHERE name='WOODEN SHIELD'),   'shield', 0,  4,  0,  0,  0,  0),
    ((SELECT id FROM item WHERE name='LEATHER CAP'),     'helmet', 0,  2,  0,  0,  0,  0),
    ((SELECT id FROM item WHERE name='LEATHER ARMOR'),   'chest',  0,  5,  0,  5,  0,  0),
    ((SELECT id FROM item WHERE name='LEATHER LEGGINGS'),'legs',   0,  3,  0,  0,  0,  0),
    ((SELECT id FROM item WHERE name='LEATHER GLOVES'),  'hands',  2,  1,  0,  0,  0,  0),
    ((SELECT id FROM item WHERE name='COPPER RING'),     'ring',   0,  0,  0,  0,  1,  5),
    ((SELECT id FROM item WHERE name='SILVER PENDANT'),  'neck',   0,  1,  0,  2,  0,  0);

ALTER TABLE IF EXISTS equipment
    OWNER to mud;

SELECT e.id, i.name, e.slot, e.bonusattackpower, e.bonusdefense, e.bonushp, e.bonusmaxhp, e.bonusmp, e.bonusmaxmp
FROM equipment e JOIN item i ON i.id = e.itemid;
