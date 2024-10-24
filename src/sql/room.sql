DROP TABLE IF EXISTS room;

CREATE TABLE IF NOT EXISTS room
(
    id integer NOT NULL GENERATED ALWAYS AS IDENTITY ( INCREMENT 1 START 1 MINVALUE 1 MAXVALUE 2147483647 CACHE 1 ),
    name text COLLATE pg_catalog."default" NOT NULL,
    description text COLLATE pg_catalog."default" NOT NULL,
	exitjson text COLLATE pg_catalog."default" NOT NULL,
    inventoryidarray integer[],
    CONSTRAINT "ROOM_pkey" PRIMARY KEY (id)
)

INSERT INTO room (name,description,exitjson,inventoryidarray) VALUES 
    ('Start Room','You find yourself in a generic looking room.  There is a door to the North.','{"N": 2}',[1]);
INSERT INTO room (name,description,exitjson,inventoryidarray) VALUES 
    ('Balcony','You are at a balcony overlooking the woods.  There is a door to the East and to the South.','{"S": 1,"E": 3}',[]);
INSERT INTO room (name,description,exitjson,inventoryidarray) VALUES 
    ('Arena','You are in the arena.  There is a door to the West.','{"W": 2}',[]);

ALTER TABLE IF EXISTS room
    OWNER to mud;