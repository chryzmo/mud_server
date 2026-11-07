package mudserver

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	muddatabase "mud_server/src/internal/database"
	"mud_server/src/internal/worldfile"
)

/*
The event scheduler (plans/event-systems.md section 4). Events come from the content: a start
time, an optional repeat, a duration and timed steps (announce, say in a room, set or clear a
world flag). The scheduler is one step of the world tick on the game loop, comparing times: no
goroutines, and the clock is whatever the tick passes in. Where each event has got to is queued
to the database the moment it changes, so a restart resumes an event that is still running.

Catch-up: an occurrence whose window has passed while the server was down is skipped and logged,
never played late. A running event resumes at the right step, and a step that was due more than
staleStep ago still sets or clears its flag (the world must end up right) but says nothing (an
announcement made late would only confuse).
*/

const staleStep = time.Minute

type liveEvent struct {
	def     *worldfile.ResolvedEvent
	occ     int  // the occurrence running, or the next one to run
	running bool // between its start and its end
	started time.Time
	step    int // steps already run in this occurrence
}

type scheduler struct {
	ms     *MudServer
	events []*liveEvent
	ran    func(step worldfile.ResolvedStep, stale bool) // tests watch the steps run
}

func (e *liveEvent) end() time.Time { return e.started.Add(e.def.Duration) }

// newScheduler builds the scheduler for the content's events, restoring saved progress. It runs
// before the game loop starts.
func newScheduler(ms *MudServer, defs []*worldfile.ResolvedEvent, db *muddatabase.MudDatabase) *scheduler {
	s := &scheduler{ms: ms}
	for _, d := range defs {
		s.events = append(s.events, &liveEvent{def: d})
	}
	type row struct {
		Key     string
		Occ     int32
		Phase   string
		Started *time.Time
		Step    int32
	}
	rows, err := db.Connection.Query(db.Context, "SELECT event_key, occurrence, phase, started_at, step FROM event_state")
	if err != nil {
		fmt.Println("event_state error: " + err.Error())
		return s
	}
	saved, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil {
		fmt.Println("event_state rows error: " + err.Error())
		return s
	}
	for _, r := range saved {
		if e := s.find(r.Key); e != nil {
			e.occ, e.step = int(r.Occ), int(r.Step)
			e.running = r.Phase == "running" && r.Started != nil
			if r.Started != nil {
				e.started = r.Started.UTC()
			}
		}
	}
	return s
}

func (s *scheduler) find(key string) *liveEvent {
	for _, e := range s.events {
		if e.def.Key == key {
			return e
		}
	}
	return nil
}

func (s *scheduler) save(e *liveEvent) {
	phase := "waiting"
	if e.running {
		phase = "running"
	}
	started := ""
	if !e.started.IsZero() {
		started = strconv.FormatInt(e.started.UnixNano(), 10)
	}
	s.ms.queueState(stateWrite{kind: "event", key: e.def.Key, value: strings.Join([]string{phase, strconv.Itoa(e.occ), started, strconv.Itoa(e.step)}, "|")})
}

// tick moves every event along to now. Loop only.
func (s *scheduler) tick(now time.Time) {
	for _, e := range s.events {
		s.advance(e, now)
	}
}

func (s *scheduler) advance(e *liveEvent, now time.Time) {
	for !e.running {
		if e.occ >= e.def.Count {
			return // every occurrence has been and gone
		}
		start := e.def.StartOf(e.occ)
		if now.Before(start) {
			return
		}
		if !now.Before(start.Add(e.def.Duration)) {
			fmt.Printf("event %s: occurrence %d (%s) passed unplayed; skipped\n", e.def.Key, e.occ+1, start.Format(time.RFC3339))
			e.occ++
			s.save(e)
			continue
		}
		s.begin(e, start)
	}
	changed := false
	for e.step < len(e.def.Steps) {
		step := e.def.Steps[e.step]
		due := e.started.Add(step.Offset)
		if due.After(now) {
			break
		}
		s.run(step, now.Sub(due) > staleStep)
		e.step++
		changed = true
	}
	if changed {
		s.save(e)
	}
	if !now.Before(e.end()) {
		s.finish(e)
	}
}

func (s *scheduler) begin(e *liveEvent, started time.Time) {
	e.running, e.started, e.step = true, started, 0
	s.save(e)
	fmt.Printf("event %s started (occurrence %d of %d)\n", e.def.Key, e.occ+1, e.def.Count)
}

// finish ends the running occurrence: its finish steps run, and the event waits for its next occurrence.
func (s *scheduler) finish(e *liveEvent) {
	for _, step := range e.def.Finish {
		s.run(step, false)
	}
	e.running, e.started, e.step = false, time.Time{}, 0
	e.occ++
	s.save(e)
	fmt.Printf("event %s ended\n", e.def.Key)
}

// run does one step. A stale step still changes flags but says nothing.
func (s *scheduler) run(step worldfile.ResolvedStep, stale bool) {
	ms := s.ms
	if s.ran != nil {
		s.ran(step, stale)
	}
	switch step.Kind {
	case worldfile.StepAnnounce:
		if !stale {
			frame := encode(announceMsg{T: msgAnnounce, Text: step.Text, From: "event"})
			for _, p := range ms.players {
				p.sendFrame(frame)
			}
		}
	case worldfile.StepRoomSay:
		if !stale {
			for _, room := range ms.World.RoomMap {
				if room.Key == step.Room {
					for _, p := range room.Players() {
						p.SendMessage(step.Text)
					}
				}
			}
		}
	case worldfile.StepSetFlag:
		ms.setWorldFlag(step.Flag, true)
	case worldfile.StepClearFlag:
		ms.setWorldFlag(step.Flag, false)
	}
}

// setWorldFlag sets or clears a world flag, saves it, and tells the players standing by a gate it governs.
func (ms *MudServer) setWorldFlag(name string, on bool) {
	if ms.World.worldFlags[name] == on {
		return
	}
	if on {
		ms.World.worldFlags[name] = true
	} else {
		delete(ms.World.worldFlags, name)
	}
	ms.queueState(stateWrite{kind: "world", key: name, remove: !on})
	for _, p := range ms.players {
		if p.Location == nil {
			continue
		}
		for _, g := range p.Location.Gates {
			if g.OpenWhen == name {
				p.sendRoomFrame()
				break
			}
		}
	}
}

// loadWorldFlags reads the saved world flags. It runs before the game loop starts.
func loadWorldFlags(db *muddatabase.MudDatabase) map[string]bool {
	flags := map[string]bool{}
	rows, err := db.Connection.Query(db.Context, "SELECT key FROM world_flag")
	if err != nil {
		fmt.Println("world_flag error: " + err.Error())
		return flags
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		fmt.Println("world_flag rows error: " + err.Error())
		return flags
	}
	for _, k := range keys {
		flags[k] = true
	}
	return flags
}

// ---------------------------------------------------------------------------------------------
// Players and the GM

var venueZone = func() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.UTC
	}
	return loc
}()

func roughly(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < 90*time.Minute:
		return fmt.Sprintf("%d minutes", int((d+30*time.Second)/time.Minute))
	}
	if d >= 48*time.Hour {
		return fmt.Sprintf("%.0f days", d.Hours()/24)
	}
	return fmt.Sprintf("%.0f hours", d.Hours())
}

// nextStart is when the event next begins, if it has an occurrence left.
func (e *liveEvent) nextStart() (time.Time, bool) {
	if e.running || e.occ >= e.def.Count {
		return time.Time{}, false
	}
	return e.def.StartOf(e.occ), true
}

// summary is what EVENTS tells a player: what is on now and what is coming. Hidden events are left out.
func (s *scheduler) summary(now time.Time) string {
	var b strings.Builder
	type upcoming struct {
		title string
		at    time.Time
	}
	var next []upcoming
	for _, e := range s.events {
		if e.def.Hidden {
			continue
		}
		if e.running {
			b.WriteString(fmt.Sprintf("On now: %s (ends in %s)\n", e.def.Title, roughly(e.end().Sub(now))))
		} else if at, ok := e.nextStart(); ok && at.Add(e.def.Duration).After(now) {
			next = append(next, upcoming{e.def.Title, at})
		}
	}
	sort.Slice(next, func(i, j int) bool { return next[i].at.Before(next[j].at) })
	if len(next) > 5 {
		next = next[:5]
	}
	for _, u := range next {
		b.WriteString(fmt.Sprintf("Coming: %s, %s (in %s)\n", u.title, u.at.In(venueZone).Format("Mon 3:04 PM MST"), roughly(u.at.Sub(now))))
	}
	if b.Len() == 0 {
		return "Nothing is scheduled right now."
	}
	return strings.TrimRight(b.String(), "\n")
}

// gmList is the GM's view: every event, hidden ones too, with its state.
func (s *scheduler) gmList(now time.Time) string {
	if len(s.events) == 0 {
		return "No events are defined."
	}
	var b strings.Builder
	for _, e := range s.events {
		state := "finished"
		if e.running {
			state = fmt.Sprintf("RUNNING, step %d of %d, ends in %s", e.step, len(e.def.Steps), roughly(e.end().Sub(now)))
		} else if at, ok := e.nextStart(); ok {
			state = fmt.Sprintf("waiting, next %s (occurrence %d of %d)", at.In(venueZone).Format("Mon 2006-01-02 15:04 MST"), e.occ+1, e.def.Count)
		}
		hidden := ""
		if e.def.Hidden {
			hidden = " [hidden]"
		}
		b.WriteString(fmt.Sprintf("%s%s: %s: %s\n", e.def.Key, hidden, e.def.Title, state))
	}
	return strings.TrimRight(b.String(), "\n")
}

// gmControl runs GM EVENT START, STOP and SKIP. It returns what to tell the GM.
func (s *scheduler) gmControl(verb, key string, now time.Time) string {
	e := s.find(key)
	if e == nil {
		return fmt.Sprintf("No event %q. GM EVENT lists them.", key)
	}
	switch verb {
	case "START":
		if e.running {
			return e.def.Title + " is already running."
		}
		if e.occ >= e.def.Count {
			e.occ = e.def.Count - 1 // every occurrence is used up: run it once more
		}
		s.begin(e, now)
		s.advance(e, now)
		return "Started " + e.def.Title + "."
	case "STOP":
		if !e.running {
			return e.def.Title + " is not running."
		}
		s.finish(e)
		return "Stopped " + e.def.Title + "."
	case "SKIP":
		if e.running || e.occ >= e.def.Count {
			return e.def.Title + " has nothing waiting to skip."
		}
		e.occ++
		s.save(e)
		return "Skipped the next occurrence of " + e.def.Title + "."
	}
	return "Usage: GM EVENT [START|STOP|SKIP <event>]"
}

// writeEvent saves one event's progress; value is "phase|occurrence|started unix nanoseconds|step".
func (ms *MudServer) writeEvent(key, value string) error {
	parts := strings.Split(value, "|")
	if len(parts) != 4 {
		return fmt.Errorf("bad event state %q", value)
	}
	occ, _ := strconv.Atoi(parts[1])
	step, _ := strconv.Atoi(parts[3])
	var started *time.Time
	if n, err := strconv.ParseInt(parts[2], 10, 64); err == nil && parts[2] != "" {
		t := time.Unix(0, n).UTC()
		started = &t
	}
	_, err := ms.DB.Connection.Exec(ms.DB.Context,
		`INSERT INTO event_state (event_key, occurrence, phase, started_at, step) VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (event_key) DO UPDATE SET occurrence = EXCLUDED.occurrence, phase = EXCLUDED.phase,
		   started_at = EXCLUDED.started_at, step = EXCLUDED.step`, key, occ, parts[0], started, step)
	return err
}
