package mudserver

import (
	"strings"
	"testing"
	"time"

	"mud_server/src/internal/worldfile"
)

// Small mobs (mobs.go): who a monster fights, who a kill pays, and the bugs found on the way.

func mobRoom() (*Room, *World) {
	w := &World{prog: worldfile.DefaultProgression(), mobCap: defaultMobCap}
	room := &Room{Name: "Yard", UserMap: map[string]*MudPlayer{}, BaddieMap: map[string]*BaddieNPC{}, Inventory: CreateInventory()}
	return room, w
}

func mobPlayer(room *Room, w *World, name string) *MudPlayer {
	p := npcTestPlayer(name)
	p.world = w
	p.Location = room
	p.MaxHP = 20
	p.Equipment = NewEquipmentSlots()
	p.Inventory = CreateInventory()
	room.AddPlayer(p)
	return p
}

func mobText(p *MudPlayer) string {
	var out []string
	for {
		select {
		case f := <-p.Session.out:
			out = append(out, queuedText(f))
		default:
			return strings.Join(out, "\n")
		}
	}
}

func TestAPassiveMonsterFightsOnlyThoseWhoHitItAndTheLatestFirst(t *testing.T) {
	room, w := mobRoom()
	ann, bob := mobPlayer(room, w, "ann"), mobPlayer(room, w, "bob")
	rat := &BaddieNPC{Name: "Rat", Location: room, passive: true}
	room.BaddieMap["RAT"] = rat
	turn := GenerateNPCCommandList()[1].action
	now := time.Now()

	turn(room, rat, now)
	if rat.TargetPC != nil {
		t.Fatalf("a passive monster picked %s with no one having hit it", rat.TargetPC.Username)
	}
	rat.provoke(ann)
	rat.provoke(bob)
	turn(room, rat, now)
	if rat.TargetPC != bob {
		t.Errorf("target = %v, want the latest attacker, bob", rat.TargetPC)
	}
	room.RemovePlayer("bob") // bob leaves the room
	turn(room, rat, now)
	if rat.TargetPC != ann {
		t.Errorf("target = %v, want ann once bob has gone", rat.TargetPC)
	}
	room.RemovePlayer("ann")
	turn(room, rat, now)
	if rat.TargetPC != nil {
		t.Errorf("target = %v, want none when every attacker has gone", rat.TargetPC)
	}
}

func TestAnAggressiveMonsterStillPicksSomeoneInTheRoom(t *testing.T) {
	room, w := mobRoom()
	mobPlayer(room, w, "ann")
	orc := &BaddieNPC{Name: "Grak", Location: room}
	GenerateNPCCommandList()[1].action(room, orc, time.Now())
	if orc.TargetPC == nil {
		t.Error("an aggressive monster should pick an occupant")
	}
}

func TestAKillPaysEveryFighterStillHereAndOnlyOnce(t *testing.T) {
	room, w := mobRoom()
	ann, bob, cy := mobPlayer(room, w, "ann"), mobPlayer(room, w, "bob"), mobPlayer(room, w, "cy")
	rat := &BaddieNPC{Name: "Rat", Location: room, passive: true, xp: 7}
	room.BaddieMap["RAT"] = rat
	ann.NPCTarget, bob.NPCTarget = rat, rat
	rat.provoke(bob)
	rat.provoke(cy)
	room.RemovePlayer("cy") // cy hit it, then left
	rat.provoke(ann)

	room.defeated(rat, ann)
	if got := ann.st().xp[xpCombat]; got != 7 {
		t.Errorf("the killer was paid %d, want 7", got)
	}
	if got := bob.st().xp[xpCombat]; got != 7 {
		t.Errorf("a fighter who hit it was paid %d, want the full 7", got)
	}
	if got := cy.st().xp[xpCombat]; got != 0 {
		t.Errorf("someone who left was paid %d", got)
	}
	if !strings.Contains(mobText(bob), "You helped defeat Rat. (+7 combat XP)") {
		t.Error("a helper should be told what they earned")
	}
	if _, still := room.BaddieMap["RAT"]; still {
		t.Error("the monster should be gone")
	}
	if ann.NPCTarget != nil || bob.NPCTarget != nil {
		t.Error("targets on the dead monster should be cleared")
	}

	room.defeated(rat, bob) // a late blow at the same monster
	if got := bob.st().xp[xpCombat]; got != 7 {
		t.Errorf("a second kill of the same monster paid again: %d", got)
	}
}

func TestAMonsterWithNoXPPaysTheProgressionKillXP(t *testing.T) {
	room, w := mobRoom()
	ann := mobPlayer(room, w, "ann")
	rat := &BaddieNPC{Name: "Rat", Location: room, passive: true}
	room.BaddieMap["RAT"] = rat
	rat.provoke(ann)
	room.defeated(rat, ann)
	if got := ann.st().xp[xpCombat]; got != w.prog.XP.Kill {
		t.Errorf("paid %d, want the progression's kill XP %d", got, w.prog.XP.Kill)
	}
}

func TestEqualGoldLootNoLongerPanics(t *testing.T) {
	for i := 0; i < 50; i++ {
		if got := lootGold(5, 5); got != 5 {
			t.Fatalf("lootGold(5, 5) = %d", got)
		}
		if got := lootGold(2, 4); got < 2 || got > 4 {
			t.Fatalf("lootGold(2, 4) = %d", got)
		}
	}
	if got := lootGold(0, 0); got != 0 {
		t.Errorf("lootGold(0, 0) = %d", got)
	}
}

func TestTwoMonstersWithTheSameNameBothSurvive(t *testing.T) {
	room, _ := mobRoom()
	room.BaddieMap["RAT"] = &BaddieNPC{Name: "Rat"}
	second := room.uniqueMobName("Rat")
	room.BaddieMap[strings.ToUpper(second)] = &BaddieNPC{Name: second}
	third := room.uniqueMobName("RAT")
	if second == "Rat" || third == second || third == "Rat" {
		t.Errorf("names = Rat, %q, %q; want three different names", second, third)
	}
	if len(room.BaddieMap) != 2 {
		t.Errorf("room has %d monsters, want the two added", len(room.BaddieMap))
	}
}

func TestASpawnerWaitsForPlayersAndForRoomInTheWorld(t *testing.T) {
	room, w := mobRoom()
	s := &BaddieSpawner{TotalNumber: 3, tickDurationMS: 250, Location: room,
		baddie: BaddieNPC{Name: "Rat", Race: "RAT", passive: true, combatTickerDurationInMS: 1000}}
	now := time.Now()

	for i := 0; i < 4; i++ {
		s.tick(now.Add(time.Duration(i)*time.Second), w)
	}
	if len(room.BaddieMap) != 0 {
		t.Fatalf("a spawner made %d monsters in an empty room", len(room.BaddieMap))
	}

	mobPlayer(room, w, "ann")
	s.tick(now.Add(10*time.Second), w) // first sight: the interval starts now
	if len(room.BaddieMap) != 0 {
		t.Fatalf("a monster appeared the moment a player arrived")
	}
	s.tick(now.Add(11*time.Second), w)
	if len(room.BaddieMap) != 1 {
		t.Fatalf("after the interval the room has %d monsters, want 1", len(room.BaddieMap))
	}
	for _, b := range room.BaddieMap {
		if !b.passive {
			t.Error("the spawned monster should keep its passive trait")
		}
	}

	w.mobCap, w.liveMobs = 5, 5
	s.tick(now.Add(12*time.Second), w)
	if len(room.BaddieMap) != 1 {
		t.Errorf("a full world still spawned: %d monsters", len(room.BaddieMap))
	}
	w.liveMobs = 4
	s.tick(now.Add(13*time.Second), w)
	if len(room.BaddieMap) != 2 || w.liveMobs != 5 {
		t.Errorf("with room in the world: %d monsters, live count %d", len(room.BaddieMap), w.liveMobs)
	}
}

func TestMonstersAreTappableInTheRoomFrame(t *testing.T) {
	room, w := mobRoom()
	ann := mobPlayer(room, w, "ann")
	room.BaddieMap["GRAK"] = &BaddieNPC{Name: "Grak"}
	var found bool
	for _, th := range ann.roomFrame().Things {
		if th.Name == "Grak" && len(th.Verbs) == 1 && th.Verbs[0] == "ATTACK" {
			found = true
		}
	}
	if !found {
		t.Errorf("things = %+v, want Grak with the ATTACK verb", ann.roomFrame().Things)
	}
}
