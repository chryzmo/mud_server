package mudserver

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

func TestLookDescriptionPreservesTextAndANSI(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*Room)
		want  string
	}{
		{"empty", func(*Room) {}, "You are in 'Hall'\nBare walls.\n\n"},
		{"npc", func(r *Room) {
			r.BaddieMap["GRAK"] = &BaddieNPC{Name: "Grak"}
		}, "You are in 'Hall'\nBare walls.\n\nThe monsters here are:\n\x1b[1;31mGrak\x1b[0m\n\n"},
		{"player", func(r *Room) {
			r.UserMap["Ada"] = &MudPlayer{Username: "Ada"}
		}, "You are in 'Hall'\nBare walls.\n\nThe players here are:\n\x1b[1;34mAda\x1b[0m\n\n"},
		{"item", func(r *Room) {
			r.Inventory.AddItem(&Item{Name: "KEY", Quantity: 2})
		}, "You are in 'Hall'\nBare walls.\n\nYou see:\n\x1b[1;32mKEY\x1b[0m\n\n"},
		{"exits", func(r *Room) {
			r.ExitMap = map[string]*Room{"W": {}, "N": {}, "E": {}}
		}, "You are in 'Hall'\nBare walls.\n\nExits: [E] [N] [W]\n\n"},
		{"all sections", func(r *Room) {
			r.Name, r.Description = "Café", "First line.\nSecond line."
			r.BaddieMap["GRAK"] = &BaddieNPC{Name: "Grak"}
			r.UserMap["Ada"] = &MudPlayer{Username: "Ada"}
			r.Inventory.AddItem(&Item{Name: "KEY", Quantity: 2})
			r.ExitMap = map[string]*Room{"W": {}, "N": {}, "E": {}}
		}, "You are in 'Café'\nFirst line.\nSecond line.\n\nThe monsters here are:\n\x1b[1;31mGrak\x1b[0m\n\nThe players here are:\n\x1b[1;34mAda\x1b[0m\n\nYou see:\n\x1b[1;32mKEY\x1b[0m\n\nExits: [E] [N] [W]\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			room := &Room{Name: "Hall", Description: "Bare walls.", UserMap: make(map[string]*MudPlayer), BaddieMap: make(map[string]*BaddieNPC), Inventory: CreateInventory()}
			tc.setup(room)
			got := room.LookDescription()
			if got != tc.want {
				t.Fatalf("LOOK = %q, want %q", got, tc.want)
			}
			// Returning another description must not mutate a previous result.
			room.Name = "Changed"
			room.LookDescription()
			if got != tc.want {
				t.Fatal("later LOOK mutated the earlier output")
			}
		})
	}
}

func TestLookDescriptionListsEveryEntry(t *testing.T) {
	room := &Room{Name: "Hall", Description: "Bare walls.",
		UserMap:   map[string]*MudPlayer{"Ada": {}, "Bea": {}, "Cy": {}},
		BaddieMap: map[string]*BaddieNPC{"GRAK": {Name: "Grak"}, "MORG": {Name: "Morg"}},
		Inventory: CreateInventory(), ExitMap: map[string]*Room{"N": {}, "E": {}},
	}
	room.Inventory.AddItem(&Item{Name: "KEY", Quantity: 2})
	room.Inventory.AddItem(&Item{Name: "GOLD", Quantity: 3})
	sections := strings.Split(room.LookDescription(), "\n\n")
	if len(sections) != 6 || sections[0] != "You are in 'Hall'\nBare walls." || sections[4] != "Exits: [E] [N]" || sections[5] != "" {
		t.Fatalf("unexpected section layout: %q", sections)
	}
	for i, want := range [][]string{
		{"The monsters here are:", "\x1b[1;31mGrak\x1b[0m", "\x1b[1;31mMorg\x1b[0m"},
		{"The players here are:", "\x1b[1;34mAda\x1b[0m", "\x1b[1;34mBea\x1b[0m", "\x1b[1;34mCy\x1b[0m"},
		{"You see:", "\x1b[1;32mGOLD\x1b[0m", "\x1b[1;32mKEY\x1b[0m"},
	} {
		got := strings.Split(sections[i+1], "\n")
		if got[0] != want[0] {
			t.Fatalf("section heading = %q, want %q", got[0], want[0])
		}
		// Map iteration order is unspecified; normalize only in the test.
		sort.Strings(got[1:])
		sort.Strings(want[1:])
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("section = %q, want all entries %q", got, want)
		}
	}
}

func crowdedLookRoom(occupants int) *Room {
	room := &Room{Name: "Start Room", Description: "Local crowded LOOK fixture", UserMap: make(map[string]*MudPlayer), Inventory: CreateInventory()}
	for i := 0; i < occupants; i++ {
		name := fmt.Sprintf("loadbot%05d", i)
		room.UserMap[name] = &MudPlayer{Username: name}
	}
	return room
}

var lookDescriptionSink string

func measureLook(b *testing.B, room *Room) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lookDescriptionSink = room.LookDescription()
	}
}

// Test a generous allocated-byte budget, not wall-clock timing. The old growing
// string copies allocate ~13 MB at 1,000 occupants and ~317 MB at 5,000. The
// linear budget allows headroom for ANSI formatting, builder growth and runtime
// differences without letting that quadratic regression pass.
func TestCrowdedLookAllocationBudget(t *testing.T) {
	for _, occupants := range []int{1000, 5000} {
		t.Run(fmt.Sprint(occupants), func(t *testing.T) {
			room := crowdedLookRoom(occupants)
			result := testing.Benchmark(func(b *testing.B) { measureLook(b, room) })
			budget := int64(512*occupants + 4096)
			if got := result.AllocedBytesPerOp(); got > budget {
				t.Errorf("LOOK with %d occupants allocates %d bytes/op, exceeds linear budget %d", occupants, got, budget)
			}
		})
	}
}

func BenchmarkCrowdedLook(b *testing.B) {
	for _, occupants := range []int{200, 500, 1000, 5000} {
		b.Run(fmt.Sprint(occupants), func(b *testing.B) { measureLook(b, crowdedLookRoom(occupants)) })
	}
}
