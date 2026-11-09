package main

import (
	"reflect"
	"testing"
)

// The room frame carries what is here as data (plans/interactive-world.md section 6).

func names(v any) []string {
	var out []string
	list, _ := v.([]any)
	for _, e := range list {
		m, _ := e.(map[string]any)
		s, _ := m["name"].(string)
		out = append(out, s)
	}
	return out
}

func TestRoomFrameListsThingsPeopleAndSealedExits(t *testing.T) {
	t.Parallel()
	conn := puzzler(t, puzzleServer(t, nil), "framer")
	send(t, conn, "LOOK")
	room := expectMessage(t, conn, "room")

	if got := names(room["things"]); !reflect.DeepEqual(got, []string{"blue bell", "first clock", "fountain", "old rope", "red bell", "second clock"}) {
		t.Errorf("things = %v", got)
	}
	if got := names(room["people"]); !reflect.DeepEqual(got, []string{"Porter"}) {
		t.Errorf("people = %v", got)
	}
	if !reflect.DeepEqual(room["locked"], []any{"D"}) {
		t.Errorf("locked = %v, want the trapdoor only: the hidden stair is not shown", room["locked"])
	}
	// What a thing offers comes from the room's rules: the clock turns.
	things := room["things"].([]any)
	clock := things[1].(map[string]any)
	if clock["id"] != "clock-1" || !reflect.DeepEqual(clock["verbs"], []any{"TURN"}) {
		t.Errorf("first clock = %v, want id clock-1 and verb TURN", clock)
	}
	if fountain := things[2].(map[string]any); !reflect.DeepEqual(fountain["verbs"], []any{"RING"}) {
		t.Errorf("fountain verbs = %v, want only RING: USE and SET need more than a name", fountain["verbs"])
	}
	if rope := things[3].(map[string]any); !reflect.DeepEqual(rope["verbs"], []any{"PULL"}) {
		t.Errorf("rope verbs = %v, want PULL", rope["verbs"])
	}
}

func TestRoomFrameFollowsGatesForThePlayerWhoOpenedThem(t *testing.T) {
	t.Parallel()
	conn := puzzler(t, puzzleServer(t, nil), "opener")
	send(t, conn, "SAY an echo")
	for {
		room := expectMessage(t, conn, "room")
		if room["locked"] == nil && contains(room["exits"], "D") {
			return
		}
	}
}

func contains(v any, s string) bool {
	list, _ := v.([]any)
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

// Items change under everyone's feet: both players get a fresh frame when one takes or drops.
func TestRoomFrameIsResentWhenAnItemMoves(t *testing.T) {
	t.Parallel()
	wsURL := startTestServer(t)
	alice := passwordPlayer(t, wsURL, "alice")
	bob := passwordPlayer(t, wsURL, "bob")
	send(t, alice, "LOOK")
	if got := names(expectMessage(t, alice, "room")["items"]); !reflect.DeepEqual(got, []string{"KEY"}) {
		t.Fatalf("items = %v, want [KEY]", got)
	}

	send(t, bob, "GET KEY")
	for _, who := range []string{"alice", "bob"} {
		c := alice
		if who == "bob" {
			c = bob
		}
		for {
			room := expectMessage(t, c, "room")
			if room["items"] == nil {
				break // the key is gone for both
			}
		}
	}

	send(t, bob, "DROP KEY")
	for {
		if got := names(expectMessage(t, alice, "room")["items"]); reflect.DeepEqual(got, []string{"KEY"}) {
			break
		}
	}
}
