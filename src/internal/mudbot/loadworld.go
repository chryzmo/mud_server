package mudbot

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

/*
The world the Phase 4 load runs play in (plans/event-systems.md, D2). It is generic and holds no real
world text. A Lobby (the start room) and a Garden form a cloneable area, so thousands of guests spread
over copies of 50, as the real world will. East of the Lobby is the Feast Hall: its own room with
capacity 200, a person and a lamp to examine, ambient lines, and a Feast event that announces to
everyone and speaks in the hall. Bots started with -mix verbs -stay walk to the hall and fill it. A plaque in
the Lobby teaches three spells (a fun one, a mend and a ward), so bots started with -mix cast can cast.
*/

// LoadWorldYAML is the world file, with the Feast starting at feast (minute precision, UTC).
func LoadWorldYAML(feast time.Time) string {
	return fmt.Sprintf(`start_room: lobby
areas:
  lobby-area:
    rooms: [lobby, garden]
rooms:
  lobby:
    name: Lobby
    description: A wide lobby with benches along the walls.
    exits: {E: hall, S: garden}
    features:
      lamp: {names: [lamp], examine: "A brass lamp hangs from a hook, unlit."}
      plaque: {names: [plaque], examine: "A brass plaque you could READ."}
    rules:
      - on: {verb: READ, target: plaque}
        unless: [{knows_spell: glow}]
        do: [{teach: glow}, {teach: mend}, {teach: ward}]
    people:
      keeper:
        names: [Keeper]
        examine: "A patient keeper in a grey coat."
        greeting: "Well met. The feast is east."
        idle: ["The Keeper straightens a bench."]
        idle_every: [60, 120]
    ambient:
      - {every: [30, 60], lines: ["A draught stirs the banners.", "Somewhere a bell rings once."]}
    day_notes: {night: "The lamps are turned low."}
  garden:
    name: Garden
    description: A quiet garden of gravel paths.
    exits: {N: lobby}
    features:
      lamp: {names: [lamp], examine: "A small lamp glows on a post."}
  hall:
    name: Feast Hall
    description: A long hall laid for a great feast.
    exits: {W: lobby}
    capacity: 200
    quiet_at: 30
    features:
      lamp: {names: [lamp], examine: "A great lamp hangs from the rafters."}
    people:
      host:
        names: [Host]
        examine: "A cheerful host in a bright coat."
        greeting: "Welcome to the feast."
    ambient:
      - {every: [20, 40], lines: ["Glasses clink along the tables.", "A cheer goes up at the far end."]}
spells:
  glow: {name: Glow, effect: fun, cost: 1, cooldown: 3, description: "A soft glow.", cast: "You glow.", room: "{player} glows."}
  mend: {name: Mend, effect: mend, cost: 4, cooldown: 6, power: 10, description: "Heals.", cast: "You mend."}
  ward: {name: Ward, effect: ward, cost: 5, cooldown: 20, power: 3, description: "Shields.", cast: "You ward yourself."}
events:
  feast:
    title: The Feast
    at: "%s UTC"
    duration: 30m
    steps:
      - {announce: "The feast begins."}
      - {after: 5s, room_say: {room: hall, text: "The doors of the hall swing wide."}}
`, feast.UTC().Truncate(time.Minute).Format("2006-01-02 15:04"))
}

// WriteLoadWorld writes the load world into dir as world.yaml.
func WriteLoadWorld(dir string, feast time.Time) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "world.yaml"), []byte(LoadWorldYAML(feast)), 0o644)
}
