package worldfile

import (
	"fmt"
	"sort"
	"strings"
	"time"
	_ "time/tzdata" // the server image may carry no zoneinfo; event times name a zone
	"unicode/utf8"
)

/*
Scheduled events (plans/event-systems.md section 4): things that happen on their own at a set
time. An event has a start (a wall-clock time in a named zone), an optional repeat, a duration
and timed steps. A step announces something, says something in a room, or sets or clears a
world flag; rules and gates can read world flags, so an event can open a door for everyone.
*/

const (
	MaxEventSteps = 30
	maxAnnounce   = 300
	maxEventSpan  = 12 * time.Hour
	minRepeat     = time.Hour
	maxRepeat     = 48 * time.Hour
	MaxRepeats    = 10
	atLayout      = "2006-01-02 15:04"
)

// Event is one scheduled event as written.
type Event struct {
	Title    string      `yaml:"title"`
	At       string      `yaml:"at"`       // "2027-01-07 12:00 America/New_York"
	Repeat   *Repeat     `yaml:"repeat"`   // optional: it happens again every N hours
	Duration string      `yaml:"duration"` // "20m"; how long the event is running. Defaults to a minute past its last step
	Hidden   bool        `yaml:"hidden"`   // left out of EVENTS
	Steps    []EventStep `yaml:"steps"`
	// Finish runs when the event ends, whether by the clock or because the GM stopped it.
	Finish []EventStep `yaml:"finish"`
}

// Repeat makes an event happen again: Count times in all, Every apart (such as "24h").
type Repeat struct {
	Every string `yaml:"every"`
	Count int    `yaml:"count"`
}

// EventStep is something that happens After the start (such as "5m"; blank means at once). Exactly one action is set.
type EventStep struct {
	After     string   `yaml:"after"`
	Announce  string   `yaml:"announce"`
	RoomSay   *RoomSay `yaml:"room_say"`
	SetFlag   string   `yaml:"set_flag"`
	ClearFlag string   `yaml:"clear_flag"`
}

// RoomSay is text shown to everyone in a room.
type RoomSay struct {
	Room string `yaml:"room"`
	Text string `yaml:"text"`
}

// Step kinds, as the server runs them.
const (
	StepAnnounce  = "announce"
	StepRoomSay   = "room_say"
	StepSetFlag   = "set_flag"
	StepClearFlag = "clear_flag"
)

// ResolvedStep is a step with its time worked out.
type ResolvedStep struct {
	Offset time.Duration
	Kind   string
	Text   string // announce and room_say
	Room   string // room_say
	Flag   string // set_flag and clear_flag
}

// ResolvedEvent is an event ready to run: an absolute start and parsed times.
type ResolvedEvent struct {
	Key      string
	Title    string
	Hidden   bool
	Start    time.Time // UTC, the first occurrence
	Every    time.Duration
	Count    int // occurrences in all (1 without a repeat)
	Duration time.Duration
	Steps    []ResolvedStep
	Finish   []ResolvedStep
}

// StartOf is when occurrence i (from 0) begins.
func (e *ResolvedEvent) StartOf(i int) time.Time { return e.Start.Add(time.Duration(i) * e.Every) }

// ParseAt reads "2027-01-07 12:00 America/New_York" into an absolute time.
func ParseAt(at string) (time.Time, error) {
	fields := strings.Fields(at)
	if len(fields) != 3 {
		return time.Time{}, fmt.Errorf("at must look like \"2027-01-07 12:00 America/New_York\"")
	}
	loc, err := time.LoadLocation(fields[2])
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not a time zone name (use one like America/New_York)", fields[2])
	}
	t, err := time.ParseInLocation(atLayout, fields[0]+" "+fields[1], loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("at must look like \"2027-01-07 12:00 America/New_York\"")
	}
	return t.UTC(), nil
}

func (s EventStep) kind() (name string, n int) {
	add := func(k string, set bool) {
		if set {
			name = k
			n++
		}
	}
	add(StepAnnounce, s.Announce != "")
	add(StepRoomSay, s.RoomSay != nil)
	add(StepSetFlag, s.SetFlag != "")
	add(StepClearFlag, s.ClearFlag != "")
	return
}

// eventFlags are the world flags some event sets.
func (w *World) eventFlags() map[string]bool {
	out := map[string]bool{}
	for _, e := range w.Events {
		for _, list := range [][]EventStep{e.Steps, e.Finish} {
			for _, s := range list {
				if s.SetFlag != "" {
					out[s.SetFlag] = true
				}
			}
		}
	}
	return out
}

// ResolveEvents returns the valid events, ordered by key. Call it after Validate found no errors.
func (w *World) ResolveEvents() []*ResolvedEvent {
	var out []*ResolvedEvent
	for _, key := range sortedKeys(w.Events) {
		if ev, problems := w.resolveEvent(key); len(problems) == 0 {
			out = append(out, ev)
		}
	}
	return out
}

func (w *World) resolveEvent(key string) (*ResolvedEvent, []string) {
	e := w.Events[key]
	var problems []string
	bad := func(path, format string, args ...any) {
		problems = append(problems, path+"\x00"+fmt.Sprintf(format, args...))
	}
	p := "events." + key
	ev := &ResolvedEvent{Key: key, Title: e.Title, Hidden: e.Hidden, Count: 1}
	if !keyPattern.MatchString(key) {
		bad(p, "event key must be lower case words joined by dashes")
	}
	if e.Title == "" || utf8.RuneCountInString(e.Title) > maxTitle {
		bad(p+".title", "a title of 1 to %d characters is required", maxTitle)
	}
	if start, err := ParseAt(e.At); err != nil {
		bad(p+".at", "%v", err)
	} else {
		ev.Start = start
	}
	if e.Repeat != nil {
		every, err := time.ParseDuration(e.Repeat.Every)
		switch {
		case err != nil:
			bad(p+".repeat.every", "%q is not a duration such as 24h", e.Repeat.Every)
		case every < minRepeat || every > maxRepeat:
			bad(p+".repeat.every", "repeat every between %s and %s", minRepeat, maxRepeat)
		default:
			ev.Every = every
		}
		if e.Repeat.Count < 2 || e.Repeat.Count > MaxRepeats {
			bad(p+".repeat.count", "count (the number of times it happens in all) is between 2 and %d", MaxRepeats)
		} else {
			ev.Count = e.Repeat.Count
		}
	}
	if len(e.Steps) == 0 || len(e.Steps) > MaxEventSteps {
		bad(p+".steps", "%d steps; between 1 and %d are required", len(e.Steps), MaxEventSteps)
	}
	if len(e.Finish) > MaxEventSteps {
		bad(p+".finish", "%d steps; at most %d", len(e.Finish), MaxEventSteps)
	}
	var last time.Duration
	step := func(s EventStep, where string, timed bool) (ResolvedStep, bool) {
		rs := ResolvedStep{}
		name, n := s.kind()
		if msg, ok := set1(n, "step"); !ok {
			bad(where, "%s", msg)
			return rs, false
		}
		if timed && s.After != "" {
			d, err := time.ParseDuration(s.After)
			if err != nil || d < 0 || d > maxEventSpan {
				bad(where+".after", "%q is not a time between 0s and %s after the start (such as 5m)", s.After, maxEventSpan)
				return rs, false
			}
			rs.Offset = d
		}
		if !timed && s.After != "" {
			bad(where+".after", "finish steps run when the event ends; they take no after")
			return rs, false
		}
		rs.Kind = name
		switch name {
		case StepAnnounce:
			rs.Text = s.Announce
		case StepRoomSay:
			rs.Room, rs.Text = s.RoomSay.Room, s.RoomSay.Text
			if _, ok := w.Rooms[rs.Room]; !ok {
				bad(where+".room_say.room", "%q is not a room", rs.Room)
				return rs, false
			}
		case StepSetFlag:
			rs.Flag = s.SetFlag
		case StepClearFlag:
			rs.Flag = s.ClearFlag
		}
		if rs.Text != "" && (utf8.RuneCountInString(rs.Text) > maxAnnounce) {
			bad(where, "text is at most %d characters", maxAnnounce)
			return rs, false
		}
		if (name == StepAnnounce || name == StepRoomSay) && strings.TrimSpace(rs.Text) == "" {
			bad(where, "text is required")
			return rs, false
		}
		if rs.Flag != "" && !keyPattern.MatchString(rs.Flag) {
			bad(where, "a world flag name is lower case words joined by dashes")
			return rs, false
		}
		return rs, true
	}
	for i, s := range e.Steps {
		if rs, ok := step(s, fmt.Sprintf("%s.steps[%d]", p, i), true); ok {
			if rs.Offset > last {
				last = rs.Offset
			}
			ev.Steps = append(ev.Steps, rs)
		}
	}
	for i, s := range e.Finish {
		if rs, ok := step(s, fmt.Sprintf("%s.finish[%d]", p, i), false); ok {
			ev.Finish = append(ev.Finish, rs)
		}
	}
	// Steps run in time order; a stable sort keeps the written order for steps at the same time.
	sort.SliceStable(ev.Steps, func(i, j int) bool { return ev.Steps[i].Offset < ev.Steps[j].Offset })
	ev.Duration = last + time.Minute
	if e.Duration != "" {
		d, err := time.ParseDuration(e.Duration)
		switch {
		case err != nil || d <= 0 || d > maxEventSpan:
			bad(p+".duration", "%q is not a duration between 1s and %s (such as 20m)", e.Duration, maxEventSpan)
		case d < last:
			bad(p+".duration", "the event ends (%s) before its last step (%s)", d, last)
		default:
			ev.Duration = d
		}
	}
	if ev.Every > 0 && ev.Duration > ev.Every {
		bad(p+".duration", "the event lasts longer (%s) than the gap before it repeats (%s)", ev.Duration, ev.Every)
	}
	return ev, problems
}

func (w *World) validateEvents(res *Result) {
	for _, key := range sortedKeys(w.Events) {
		f := w.source["event:"+key]
		_, problems := w.resolveEvent(key)
		for _, pr := range problems {
			path, msg, _ := strings.Cut(pr, "\x00")
			res.errorf(f, path, "%s", msg)
		}
	}
}
