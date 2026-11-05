package mudserver

import (
	"fmt"
	"testing"
	"time"
)

func npcTestPlayer(name string) *MudPlayer {
	return &MudPlayer{
		Username: name,
		HP:       20,
		Session: &Session{
			out:    make(chan []byte, (SessionSettings{}).withDefaults().OutboundQueueSize),
			closed: make(chan struct{}),
		},
	}
}

// All state and NPC turns run on the real game loop. The undrained outbound
// queues make the old per-candidate broadcast burst fail deterministically,
// without relying on socket timing or changing the slow-client policy.
func TestNPCTargetSelectionDoesNotFloodCrowdedRoom(t *testing.T) {
	ms := startBareLoop(t)
	callLoop(t, ms, func() {
		room := &Room{UserMap: make(map[string]*MudPlayer)}
		for i := 0; i < 500; i++ {
			p := npcTestPlayer(fmt.Sprintf("crowd%03d", i))
			room.AddPlayer(p)
		}
		npc := &BaddieNPC{Name: "Grak", Location: room}
		command := GenerateNPCCommandList()[1]
		command.action(room, npc, time.Now())
		if npc.TargetPC == nil || room.GetPlayer(npc.TargetPC.Username) != npc.TargetPC {
			t.Error("NPC did not select an occupant")
			return
		}
		want := "\x1b[1;31mGrak\x1b[0m looks at " + npc.TargetPC.Username + "."
		for _, p := range room.UserMap {
			select {
			case <-p.Session.closed:
				t.Errorf("target selection overflowed %s's outbound queue", p.Username)
				return
			default:
			}
			if got := len(p.Session.out); got != 1 {
				t.Errorf("%s received %d selection messages, want one", p.Username, got)
				return
			}
			if got := string(<-p.Session.out); got != want {
				t.Errorf("selection message = %q, want %q", got, want)
				return
			}
		}
	})
}

func TestNPCTargetSelectionRetainsAndReplacesTarget(t *testing.T) {
	ms := startBareLoop(t)
	callLoop(t, ms, func() {
		room := &Room{UserMap: make(map[string]*MudPlayer)}
		command := GenerateNPCCommandList()[1]
		now := time.Now()
		npc := &BaddieNPC{Name: "Grak", Location: room, nextAttackAt: now.Add(time.Hour)}
		command.action(room, npc, now)
		if npc.TargetPC != nil {
			t.Error("empty room produced a target")
			return
		}
		first, second := npcTestPlayer("first"), npcTestPlayer("second")
		room.AddPlayer(first)
		room.AddPlayer(second)
		command.action(room, npc, now)
		selected := npc.TargetPC
		if selected != first && selected != second {
			t.Error("target is not one of the occupants")
			return
		}
		// Selection announces once; retaining a valid target does not announce
		// again. Its next attack is deliberately not due during this test.
		for _, p := range room.UserMap {
			if len(p.Session.out) != 1 {
				t.Error("selection must announce once to each occupant")
				return
			}
			<-p.Session.out
		}
		command.action(room, npc, now)
		if npc.TargetPC != selected || len(first.Session.out) != 0 || len(second.Session.out) != 0 {
			t.Error("valid target was replaced or announced again")
			return
		}
		room.RemovePlayer(selected.Username)
		command.action(room, npc, now)
		if npc.TargetPC == nil || npc.TargetPC == selected || room.GetPlayer(npc.TargetPC.Username) != npc.TargetPC {
			t.Error("departed target was not replaced by the remaining occupant")
			return
		}
		if len(npc.TargetPC.Session.out) != 1 || len(selected.Session.out) != 0 {
			t.Error("replacement must announce once, only to current occupants")
			return
		}
		room.RemovePlayer(npc.TargetPC.Username)
		command.action(room, npc, now)
		if npc.TargetPC != nil {
			t.Error("target must clear when nobody remains")
		}
	})
}
