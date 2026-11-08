package mudserver

import (
	"sort"
	"strings"
)

// The structured part of the room frame (plans/interactive-world.md section 6): what is
// here, as data, so the client's taps are exact and do not depend on the text's wording.

type (
	roomThing struct {
		ID    string   `json:"id"`
		Name  string   `json:"name"`
		Verbs []string `json:"verbs,omitempty"` // what the room's rules let you do with it, such as TURN
	}
	roomItem struct {
		Name string `json:"name"`
		Qty  int32  `json:"qty,omitempty"`
	}
)

// frameVerbs are the verbs a tap can send with only the thing's name. Verbs that need a second
// thing or words (USE, GIVE, PUT, THROW, SET) are typed instead.
func (r *Room) frameVerbs(key string) []string {
	var verbs []string
	for i := range r.Rules {
		t := r.Rules[i].On
		if t == nil || t.Verb == "" || t.Target != key || t.With != "" || t.Value != "" {
			continue
		}
		v := strings.ToUpper(t.Verb)
		dup := false
		for _, have := range verbs {
			dup = dup || have == v
		}
		if !dup {
			verbs = append(verbs, v)
		}
	}
	return verbs
}

// roomFrame is the frame for this player: their usable exits, the sealed ones, and the
// features, people and items in the room. Loop only.
func (mp *MudPlayer) roomFrame() roomMsg {
	room := mp.Location
	msg := roomMsg{T: msgRoom, Name: room.Name, Exits: mp.usableExits(), Locked: mp.sealedExits()}
	for _, f := range room.Features {
		if f.Unlisted || len(f.Names) == 0 {
			continue
		}
		msg.Things = append(msg.Things, roomThing{ID: f.Key, Name: f.Names[0], Verbs: room.frameVerbs(f.Key)})
	}
	for _, b := range room.BaddieMap {
		msg.Things = append(msg.Things, roomThing{ID: "mob:" + strings.ToUpper(b.Name), Name: b.Name, Verbs: []string{"ATTACK"}})
	}
	sort.Slice(msg.Things, func(i, j int) bool { return msg.Things[i].Name < msg.Things[j].Name }) // content is a map: keep the order steady
	for _, p := range room.People {
		if len(p.Names) > 0 {
			thing := roomThing{ID: p.Key, Name: p.Names[0]}
			if len(p.Shop) > 0 {
				thing.Verbs = []string{"SHOP"}
			}
			msg.People = append(msg.People, thing)
		}
	}
	for name, it := range room.Inventory.items {
		msg.Items = append(msg.Items, roomItem{Name: name, Qty: it.Quantity})
	}
	sort.Slice(msg.People, func(i, j int) bool { return msg.People[i].Name < msg.People[j].Name })
	sort.Slice(msg.Items, func(i, j int) bool { return msg.Items[i].Name < msg.Items[j].Name })
	return msg
}

// sendRoomFrame sends the player a fresh room frame.
func (mp *MudPlayer) sendRoomFrame() {
	mp.sendFrame(encode(mp.roomFrame()))
}

// roomChanged sends every player in the room a fresh frame, after an item was taken or dropped.
func (r *Room) roomChanged() {
	for _, p := range r.Players() {
		p.sendRoomFrame()
	}
}
