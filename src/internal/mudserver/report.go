package mudserver

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"mud_server/src/internal/muddata"
)

/*
REPORT (plans/event-systems.md track E1). The owner cannot watch the chat all weekend, so players are
the eyes: REPORT <player> [reason] writes a report with the last lines of chat and room talk as
context, and tells any GM who is online. GM REPORTS lists the open ones, shows one with its context,
and closes them. Reporters are limited (three an hour), and when several different players report the
same person within a few minutes they are muted for a short while (a mute, never a kick; a GM can lift
it). Everything that touches the database runs away from the game loop.
*/

const (
	reportsPerHour   = 3
	reportWindow     = time.Hour
	reportReasonMax  = 200
	reportNameMax    = 24
	reportContext    = 15               // lines of recent talk kept with a report
	reportRecentKeep = 60               // lines the server remembers
	reportRecentAge  = 10 * time.Minute // older lines are not context
	reportAutoWindow = 10 * time.Minute // reports within this count together
	reportAutoCount  = 3                // different reporters that trigger the automatic mute
	reportAutoMute   = 10 * time.Minute
	reportListLimit  = 20
)

type talkLine struct {
	at    time.Time
	where string // "global" or a room name
	who   string
	text  string
}

type reportMark struct {
	by string // lower-case reporter
	at time.Time
}

// noteLine remembers a line of talk, for the context of a report. Loop only.
func (ms *MudServer) noteLine(where, who, text string) {
	ms.recent = append(ms.recent, talkLine{at: time.Now(), where: where, who: who, text: text})
	if len(ms.recent) > reportRecentKeep {
		ms.recent = append(ms.recent[:0], ms.recent[len(ms.recent)-reportRecentKeep:]...)
	}
}

// reportContextText is the recent global chat and the talk in the reporter's room.
func (ms *MudServer) reportContextText(p *MudPlayer, now time.Time) string {
	room := ""
	if p.Location != nil {
		room = p.Location.Name
	}
	var picked []talkLine
	for _, l := range ms.recent {
		if now.Sub(l.at) <= reportRecentAge && (l.where == "global" || l.where == room) {
			picked = append(picked, l)
		}
	}
	if len(picked) > reportContext {
		picked = picked[len(picked)-reportContext:]
	}
	var b strings.Builder
	for _, l := range picked {
		fmt.Fprintf(&b, "%s [%s] %s: %s\n", l.at.Format("15:04:05"), l.where, l.who, l.text)
	}
	return strings.TrimSpace(b.String())
}

// cleanName keeps a reported name to something short and printable.
func cleanName(s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > reportNameMax {
		s = string([]rune(s)[:reportNameMax])
	}
	return s
}

// report handles REPORT <player> [reason]. Loop only.
func (ms *MudServer) report(p *MudPlayer, text string, now time.Time) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		p.SendMessage("Usage: REPORT <player> [what happened]  (a GM will read it; three reports an hour)")
		return
	}
	target := cleanName(fields[0])
	reason := strings.Join(fields[1:], " ")
	if utf8.RuneCountInString(reason) > reportReasonMax {
		reason = string([]rune(reason)[:reportReasonMax])
	}
	if strings.EqualFold(target, p.Username) {
		p.SendMessage("You cannot report yourself.")
		return
	}
	kept := p.reports[:0]
	for _, t := range p.reports {
		if now.Sub(t) < reportWindow {
			kept = append(kept, t)
		}
	}
	p.reports = kept
	if len(p.reports) >= reportsPerHour {
		p.SendMessage(fmt.Sprintf("You have sent %d reports in the last hour. A GM will get to them; try again later.", reportsPerHour))
		return
	}
	p.reports = append(p.reports, now)

	tp := ms.findPlayerFold(target)
	if tp != nil {
		target = tp.Username // the spelling players see
	}
	context := ms.reportContextText(p, now)
	reporter := p.Username
	ms.offLoop(func() {
		if _, err := ms.DB.Connection.Exec(ms.DB.Context,
			`INSERT INTO report (reporter, target, reason, context) VALUES ($1, $2, $3, $4)`, reporter, target, reason, context); err != nil {
			fmt.Println("report insert error: " + err.Error())
		}
	})
	fmt.Printf("Report: %s reported %s: %s\n", reporter, target, reason)
	p.SendMessage("Thank you. A GM will look at your report.")
	line := fmt.Sprintf("[Report] %s reported %s", reporter, target)
	if reason != "" {
		line += ": " + reason
	}
	for _, g := range ms.players {
		if g.gm {
			g.SendMessage(line + "  (GM REPORTS lists them)")
		}
	}
	ms.autoMuteOnReports(tp, p, now)
}

// autoMuteOnReports mutes a player several different people have reported in a short time. Loop only.
func (ms *MudServer) autoMuteOnReports(target, reporter *MudPlayer, now time.Time) {
	if target == nil || target.gm {
		return
	}
	key := strings.ToLower(target.Username)
	marks := ms.reportMarks[key][:0]
	for _, m := range ms.reportMarks[key] {
		if now.Sub(m.at) < reportAutoWindow {
			marks = append(marks, m)
		}
	}
	marks = append(marks, reportMark{by: strings.ToLower(reporter.Username), at: now})
	if ms.reportMarks == nil {
		ms.reportMarks = map[string][]reportMark{}
	}
	ms.reportMarks[key] = marks
	distinct := map[string]bool{}
	for _, m := range marks {
		distinct[m.by] = true
	}
	if len(distinct) < reportAutoCount || target.muteRemaining(now) > 0 {
		return
	}
	delete(ms.reportMarks, key)
	ms.autoMute(target, reportAutoMute, fmt.Sprintf("%d players reported them", len(distinct)), now)
}

// autoMute mutes a player on the server's own say-so, tells them why, and records it. Loop only.
func (ms *MudServer) autoMute(target *MudPlayer, d time.Duration, why string, now time.Time) {
	target.mutedUntil = now.Add(d)
	target.SendMessage(fmt.Sprintf("You have been muted for %d minutes (%s). You can still move and play; you just cannot talk for now. A GM can look at it.", int(d/time.Minute), why))
	ms.saveMute(target)
	ms.auditAs("system", "AUTOMUTE", target.Username, fmt.Sprintf("%dm: %s", int(d/time.Minute), why))
	for _, g := range ms.players {
		if g.gm {
			g.SendMessage(fmt.Sprintf("[Auto] %s muted for %d minutes: %s. GM UNMUTE %s lifts it.", target.Username, int(d/time.Minute), why, target.Username))
		}
	}
}

type reportRow struct {
	ID       int64
	At       time.Time
	Reporter string
	Target   string
	Reason   string
	Context  string
	Open     bool
}

const reportCols = `id, at, reporter, target, reason, context, open`

// gmReports is GM REPORTS [id | CLOSE id|ALL]. The database work runs off the loop and the answer comes back to the GM.
func (ms *MudServer) gmReports(gm *MudPlayer, args []string) string {
	reply := func(text string) { ms.enqueue(func() { gm.SendMessage(text) }) }
	switch {
	case len(args) == 0:
		ms.offLoop(func() {
			rows, err := ms.DB.Connection.Query(ms.DB.Context, `SELECT `+reportCols+` FROM report WHERE open ORDER BY id DESC LIMIT $1`, reportListLimit)
			if err != nil {
				reply("Could not read reports: " + err.Error())
				return
			}
			list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[reportRow])
			if err != nil {
				reply("Could not read reports: " + err.Error())
				return
			}
			if len(list) == 0 {
				reply("No open reports.")
				return
			}
			var b strings.Builder
			fmt.Fprintf(&b, "Open reports (newest first, up to %d). GM REPORTS <number> shows one; GM REPORTS CLOSE <number|ALL>.", reportListLimit)
			for _, r := range list {
				fmt.Fprintf(&b, "\n#%d %s %s reported %s", r.ID, r.At.Format("Mon 15:04"), r.Reporter, r.Target)
				if r.Reason != "" {
					b.WriteString(": " + r.Reason)
				}
			}
			reply(b.String())
		})
		return "list"

	case strings.EqualFold(args[0], "CLOSE"):
		if len(args) != 2 {
			gm.SendMessage("Usage: GM REPORTS CLOSE <number|ALL>")
			return "usage"
		}
		if strings.EqualFold(args[1], "ALL") {
			ms.offLoop(func() {
				tag, err := ms.DB.Connection.Exec(ms.DB.Context, `UPDATE report SET open=false WHERE open`)
				if err != nil {
					reply("That did not work: " + err.Error())
					return
				}
				reply(fmt.Sprintf("Closed %d reports.", tag.RowsAffected()))
			})
			return "close all"
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(args[1], "#"), 10, 64)
		if err != nil {
			gm.SendMessage("Usage: GM REPORTS CLOSE <number|ALL>")
			return "usage"
		}
		ms.offLoop(func() {
			tag, err := ms.DB.Connection.Exec(ms.DB.Context, `UPDATE report SET open=false WHERE id=$1 AND open`, id)
			switch {
			case err != nil:
				reply("That did not work: " + err.Error())
			case tag.RowsAffected() == 0:
				reply(fmt.Sprintf("No open report #%d.", id))
			default:
				reply(fmt.Sprintf("Closed report #%d.", id))
			}
		})
		return "close " + strconv.FormatInt(id, 10)

	default:
		id, err := strconv.ParseInt(strings.TrimPrefix(args[0], "#"), 10, 64)
		if err != nil {
			gm.SendMessage("Usage: GM REPORTS [number | CLOSE number|ALL]")
			return "usage"
		}
		ms.offLoop(func() {
			rows, err := ms.DB.Connection.Query(ms.DB.Context, `SELECT `+reportCols+` FROM report WHERE id=$1`, id)
			if err != nil {
				reply("Could not read the report: " + err.Error())
				return
			}
			list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[reportRow])
			if err != nil || len(list) == 0 {
				reply(fmt.Sprintf("No report #%d.", id))
				return
			}
			r := list[0]
			state := "open"
			if !r.Open {
				state = "closed"
			}
			text := fmt.Sprintf("Report #%d (%s) %s: %s reported %s", r.ID, state, r.At.Format("Mon 2006-01-02 15:04"), r.Reporter, r.Target)
			if r.Reason != "" {
				text += "\nReason: " + r.Reason
			}
			if r.Context != "" {
				text += "\nRecent talk:\n" + r.Context
			}
			reply(text)
		})
		return "show " + strconv.FormatInt(id, 10)
	}
}

func registerReport(ms *MudServer, cList map[string]*MudCommand) {
	cList["REPORT"] = &MudCommand{description: "Report a player who is spoiling the game for others.  Usage: REPORT <player> [what happened]", action: func(p *MudPlayer, arg string) int {
		ms.report(p, arg, time.Now())
		return muddata.MUD_OK
	}}
}
