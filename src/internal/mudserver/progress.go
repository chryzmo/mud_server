package mudserver

import (
	"fmt"
	"strconv"
	"strings"

	"mud_server/src/internal/muddata"
	"mud_server/src/internal/worldfile"
)

/*
XP and levels (plans/event-systems.md section 6). A player earns exploration XP for the first time
they enter a room or examine a feature, and combat XP for each monster they kill; level is derived
from the total through the content's table. A level adds a little max HP, attack and defense, applied
once per level (the level already applied is saved with the XP, so a crash between the XP write and the
stat save is repaired at the next login). XP is player state, written at once like the rest of it.
*/

const (
	xpExplore = "explore"
	xpCombat  = "combat"
	xpApplied = "level" // the highest level whose stat gains this player has had
)

// levelFor is the level a total of xp reaches (1 is the first).
func levelFor(p *worldfile.Progression, xp int) int {
	level := 1
	for i, from := range p.Levels {
		if xp >= from {
			level = i + 1
		}
	}
	return level
}

// nextLevelAt is the total XP the level after this one needs, or 0 at the top.
func nextLevelAt(p *worldfile.Progression, level int) int {
	if level >= len(p.Levels) {
		return 0
	}
	return p.Levels[level]
}

func (mp *MudPlayer) totalXP() int { return mp.st().xp[xpExplore] + mp.st().xp[xpCombat] }

// level is the player's current level (1 with no progression loaded).
func (mp *MudPlayer) level() int {
	if mp.world == nil {
		return 1
	}
	return levelFor(&mp.world.prog, mp.totalXP())
}

// addXP gives XP to a bucket (xpExplore or xpCombat), saves it, and raises the level if it is earned.
func (mp *MudPlayer) addXP(bucket string, n int) {
	if n <= 0 || mp.world == nil {
		return
	}
	st := mp.st()
	st.xp[bucket] += n
	mp.persistState("xp", bucket, strconv.Itoa(st.xp[bucket]), false)
	if mp.applyLevels(true) == 0 {
		mp.SendStatUpdate() // the XP bar moved
	}
}

// applyLevels gives the stat gains for every level reached and not yet applied, and says so when
// announce is set. It returns how many levels were applied.
func (mp *MudPlayer) applyLevels(announce bool) int {
	p := &mp.world.prog
	st := mp.st()
	have := st.xp[xpApplied]
	if have < 1 {
		have = 1
	}
	want := levelFor(p, mp.totalXP())
	if want <= have {
		return 0
	}
	n := want - have
	mp.MaxHP += n * p.PerLevel.MaxHP
	mp.HP = min(mp.HP+n*p.PerLevel.MaxHP, mp.MaxHP)
	mp.AttackPower += n * p.PerLevel.Attack
	mp.Defense += n * p.PerLevel.Defense
	st.xp[xpApplied] = want
	mp.persistState("xp", xpApplied, strconv.Itoa(want), false)
	mp.dirty = true
	if announce {
		mp.SendMessage(fmt.Sprintf("You reach level %d! (+%d max HP, +%d attack, +%d defense per level)", want, p.PerLevel.MaxHP, p.PerLevel.Attack, p.PerLevel.Defense))
	}
	mp.SendStatUpdate()
	return n
}

// withThe puts "the" before a feature's name, without doubling one the name already starts with.
func withThe(name string) string {
	if len(name) > 4 && strings.EqualFold(name[:4], "the ") {
		return "the " + name[4:]
	}
	return "the " + name
}

// xpLine formats an XP gain for the client: the line in italics (ESC[3m ... ESC[0m, which the client draws
// as italic text) followed by a blank line, so the gain stands apart from the text around it.
func xpLine(text string) string { return "\x1b[3m" + text + "\x1b[0m\n" }

// discoverRoom is exploration XP for the first visit to a room (called from visit).
func (mp *MudPlayer) discoverRoom() {
	if mp.world != nil && mp.world.prog.XP.Room > 0 {
		mp.addXP(xpExplore, mp.world.prog.XP.Room)
		mp.SendMessage(xpLine(fmt.Sprintf("You discover a new place. (+%d exploration XP)", mp.world.prog.XP.Room)))
	}
}

// discoverFeature is exploration XP the first time a feature is examined; key is "room/feature", and name is
// what the feature is called, so the line says what the player looked at.
func (mp *MudPlayer) discoverFeature(roomKey, featureKey, name string) {
	if mp.world == nil || roomKey == "" || featureKey == "" || mp.world.prog.XP.Feature <= 0 {
		return
	}
	id := roomKey + "/" + featureKey
	if mp.st().seen[id] {
		return
	}
	mp.st().seen[id] = true
	mp.persistState("seen", id, "1", false)
	mp.addXP(xpExplore, mp.world.prog.XP.Feature)
	mp.SendMessage(xpLine(fmt.Sprintf("You look closely at %s. (+%d exploration XP)", withThe(name), mp.world.prog.XP.Feature)))
}

// killedMonster is combat XP for a kill.
func (mp *MudPlayer) killedMonster() {
	if mp.world != nil && mp.world.prog.XP.Kill > 0 {
		mp.addXP(xpCombat, mp.world.prog.XP.Kill)
	}
}

// xpFields is the level, total XP and the XP of the next level, for the stats frame.
func (mp *MudPlayer) xpFields() (level, xp, next int) {
	if mp.world == nil {
		return 1, 0, 0
	}
	level = mp.level()
	return level, mp.totalXP(), nextLevelAt(&mp.world.prog, level)
}

// rankText answers RANK: your level and XP, and your place on the board when it has you.
func (ms *MudServer) rankText(p *MudPlayer) string {
	level, xp, next := p.xpFields()
	var b strings.Builder
	fmt.Fprintf(&b, "Level %d, %d XP (exploration %d, combat %d).", level, xp, p.st().xp[xpExplore], p.st().xp[xpCombat])
	if next > 0 {
		fmt.Fprintf(&b, " Next level at %d XP.", next)
	}
	if bd := ms.board.Load(); bd != nil {
		if rank, ok := bd.ranks[strings.ToLower(p.Username)]; ok {
			fmt.Fprintf(&b, "\nYou are number %d of %d on the leaderboard.", rank, bd.total)
		} else if xp > 0 {
			b.WriteString("\nThe leaderboard has not caught up with you yet; it updates every few seconds.")
		}
	}
	return b.String()
}

func registerProgress(ms *MudServer, cList map[string]*MudCommand) {
	cList["RANK"] = &MudCommand{description: "Your level, XP and place on the leaderboard.", action: func(p *MudPlayer, arg string) int {
		p.SendMessage(ms.rankText(p))
		return muddata.MUD_OK
	}}
}
