package mudserver

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"

	muddatabase "mud_server/src/internal/database"
	muddata "mud_server/src/internal/muddata"
)

/*
The game master (plans/event-systems.md section 2). There is one GM, the owner, and no
staff, so the tools are small and the game looks after itself; these are the extras the
owner uses from a phone. A GM is a password account with the gm flag, created on the host
with `mud_server -add-gm NAME`; guests are never GMs. GM commands are one command, GM,
with sub-commands, and for anyone else it does not exist ("Unknown command."). Each use is
written to the gm_audit table and the server log. Commands run on the game loop; their
database writes are done off the loop.
*/

const (
	codeJoinsLocked     = "joins_locked"
	defaultMute         = 15 * time.Minute
	defaultKick         = 10 * time.Minute
	maxMute             = 24 * time.Hour
	gmWhoLimit          = 40
	minGMPasswordLength = 12
)

// muteRemaining is how long the player must still stay silent; zero if they may speak.
func (mp *MudPlayer) muteRemaining(now time.Time) time.Duration {
	if mp.mutedUntil.After(now) {
		return mp.mutedUntil.Sub(now)
	}
	return 0
}

// refuseIfMuted tells a muted player so and reports whether they were refused.
func (mp *MudPlayer) refuseIfMuted(now time.Time) bool {
	d := mp.muteRemaining(now)
	if d <= 0 {
		return false
	}
	mp.SendMessage(fmt.Sprintf("You are muted for another %s.", roundUp(d)))
	return true
}

func roundUp(d time.Duration) string {
	if d > time.Minute {
		return fmt.Sprintf("%d minutes", int((d+time.Minute-1)/time.Minute))
	}
	return "minute"
}

const gmUsage = `GM commands:
  GM WHO [prefix]            who is here (counts, then names and rooms)
  GM MUTE <name> [minutes]   silence SAY and WHISPER (default 15, at most 1440)
  GM UNMUTE <name>
  GM TELEPORT <name>         go to a player
  GM TELEPORT ROOM <text>    go to the room whose name starts with text
  GM SUMMON <name>           bring a player to you
  GM KICK <name> [minutes]   remove a character for a break (default 10, at most 1440); they cannot return until it ends
  GM ANNOUNCE <text>         tell everyone
  GM LOCK / GM UNLOCK        refuse or allow new visitors (players already in can reconnect)`

// gmCommand is the GM command. Loop only, like every command.
func (ms *MudServer) gmCommand() *MudCommand {
	return &MudCommand{
		description: "Game master commands.",
		gmOnly:      true,
		action: func(player *MudPlayer, arguments string) int {
			fields := strings.Fields(arguments)
			if len(fields) == 0 {
				player.SendMessage(gmUsage)
				return muddata.MUD_OK
			}
			verb, rest := strings.ToUpper(fields[0]), fields[1:]
			result := ms.gmRun(player, verb, rest, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(arguments), fields[0])))
			if result != "" {
				ms.audit(player, verb, strings.Join(rest, " "), result)
			}
			return muddata.MUD_OK
		},
	}
}

// gmRun does one sub-command and returns a short result for the audit trail ("" for help).
func (ms *MudServer) gmRun(gm *MudPlayer, verb string, args []string, text string) string {
	now := time.Now()
	switch verb {
	case "HELP":
		gm.SendMessage(gmUsage)
		return ""

	case "WHO":
		prefix := ""
		if len(args) > 0 {
			prefix = strings.ToLower(args[0])
		}
		var names []string
		guests, accounts, dormant := 0, 0, 0
		for name, p := range ms.players {
			if p.guest {
				guests++
			} else {
				accounts++
			}
			if p.dormant {
				dormant++
			}
			if prefix == "" || strings.HasPrefix(strings.ToLower(name), prefix) {
				line := name
				if p.gm {
					line += " [GM]"
				}
				if p.Location != nil {
					line += " (" + p.Location.Name + ")"
				}
				if d := p.muteRemaining(now); d > 0 {
					line += " muted " + roundUp(d)
				}
				if p.dormant {
					line += " away"
				}
				names = append(names, line)
			}
		}
		sort.Strings(names)
		var b strings.Builder
		fmt.Fprintf(&b, "Online: %d (%d guests, %d accounts, %d away). Visitors: %s.", len(ms.players), guests, accounts, dormant, map[bool]string{true: "locked", false: "welcome"}[ms.joinsLocked.Load()])
		for i, line := range names {
			if i == gmWhoLimit {
				fmt.Fprintf(&b, "\n...and %d more; narrow it with GM WHO <prefix>.", len(names)-gmWhoLimit)
				break
			}
			b.WriteString("\n" + line)
		}
		gm.SendMessage(b.String())
		return fmt.Sprintf("%d online", len(ms.players))

	case "MUTE", "UNMUTE":
		if len(args) == 0 {
			gm.SendMessage("Usage: GM " + verb + " <name>" + map[bool]string{true: " [minutes]", false: ""}[verb == "MUTE"])
			return "usage"
		}
		target := ms.findPlayerFold(args[0])
		if target == nil {
			gm.SendMessage("No one called " + args[0] + " is here.")
			return "not found"
		}
		if target.gm {
			gm.SendMessage("A GM cannot be muted.")
			return "refused"
		}
		if verb == "UNMUTE" {
			target.mutedUntil = time.Time{}
			target.SendMessage("A GM has lifted your mute.")
			ms.saveMute(target)
			gm.SendMessage(target.Username + " can speak again.")
			return "unmuted " + target.Username
		}
		d := defaultMute
		if len(args) > 1 {
			m, err := strconv.Atoi(args[1])
			if err != nil || m < 1 {
				gm.SendMessage("Minutes must be a whole number from 1 to 1440.")
				return "usage"
			}
			d = min(time.Duration(m)*time.Minute, maxMute)
		}
		target.mutedUntil = now.Add(d)
		target.SendMessage(fmt.Sprintf("A GM has muted you for %d minutes. You can still move and play; you just cannot talk for now.", int(d/time.Minute)))
		ms.saveMute(target)
		gm.SendMessage(fmt.Sprintf("%s is muted for %d minutes.", target.Username, int(d/time.Minute)))
		return fmt.Sprintf("muted %s %dm", target.Username, int(d/time.Minute))

	case "TELEPORT":
		if len(args) == 0 {
			gm.SendMessage("Usage: GM TELEPORT <name>  or  GM TELEPORT ROOM <text>")
			return "usage"
		}
		var dest *Room
		if strings.EqualFold(args[0], "ROOM") {
			want := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), args[0])))
			if want == "" {
				gm.SendMessage("Usage: GM TELEPORT ROOM <text>")
				return "usage"
			}
			var best *Room
			for _, r := range ms.World.RoomMap {
				if r.clone || !strings.HasPrefix(strings.ToLower(r.Name), want) {
					continue
				}
				if best == nil || r.ID < best.ID {
					best = r
				}
			}
			if best == nil {
				gm.SendMessage("No room's name starts with that.")
				return "no room"
			}
			dest, _ = ms.World.placeNear(best.ID, -1)
			if dest == nil {
				gm.SendMessage("That room cannot take anyone right now.")
				return "refused"
			}
		} else {
			target := ms.findPlayerFold(args[0])
			if target == nil || target.Location == nil {
				gm.SendMessage("No one called " + args[0] + " is here.")
				return "not found"
			}
			dest = target.Location
		}
		ms.relocate(gm, dest)
		return "teleport " + dest.Name

	case "SUMMON":
		if len(args) == 0 {
			gm.SendMessage("Usage: GM SUMMON <name>")
			return "usage"
		}
		target := ms.findPlayerFold(args[0])
		if target == nil {
			gm.SendMessage("No one called " + args[0] + " is here.")
			return "not found"
		}
		if target == gm {
			gm.SendMessage("You are already with yourself.")
			return "refused"
		}
		target.SendMessage("A GM has summoned you.")
		ms.relocate(target, gm.Location)
		gm.SendMessage(target.Username + " is here.")
		return "summoned " + target.Username

	case "KICK":
		if len(args) == 0 {
			gm.SendMessage("Usage: GM KICK <name> [minutes]")
			return "usage"
		}
		target := ms.findPlayerFold(args[0])
		if target == nil {
			gm.SendMessage("No one called " + args[0] + " is here.")
			return "not found"
		}
		if target.gm {
			gm.SendMessage("A GM cannot be kicked.")
			return "refused"
		}
		d := defaultKick
		if len(args) > 1 {
			m, err := strconv.Atoi(args[1])
			if err != nil || m < 1 {
				gm.SendMessage("Minutes must be a whole number from 1 to 1440.")
				return "usage"
			}
			d = min(time.Duration(m)*time.Minute, maxMute)
		}
		minutes := int(d / time.Minute)
		ms.kicks.set(target.Username, now.Add(d))
		// Say why before closing, so the client stops instead of reconnecting (as with idle and replaced).
		target.Session.sendError(codeKicked, fmt.Sprintf("A GM asked you to take a break for %d minutes. You can come back after that.", minutes))
		target.Session.CloseWith(closeKicked)
		ms.disconnect(target) // out of the world now, saved; their token still works once the break is over
		gm.SendMessage(fmt.Sprintf("%s is out for %d minutes.", target.Username, minutes))
		return fmt.Sprintf("kicked %s %dm", target.Username, minutes)

	case "ANNOUNCE":
		if text == "" {
			gm.SendMessage("Usage: GM ANNOUNCE <text>")
			return "usage"
		}
		frame := encodeText("[GM] " + text)
		n := 0
		for _, p := range ms.players {
			if p.sendFrame(frame) == nil {
				n++
			}
		}
		gm.SendMessage(fmt.Sprintf("Announced to %d players.", n))
		return fmt.Sprintf("announced to %d", n)

	case "LOCK":
		ms.joinsLocked.Store(true)
		gm.SendMessage("New visitors are locked out. Players already in can reconnect. GM UNLOCK reopens.")
		return "locked"
	case "UNLOCK":
		ms.joinsLocked.Store(false)
		gm.SendMessage("New visitors are welcome again.")
		return "unlocked"
	}
	gm.SendMessage("Unknown GM command. " + gmUsage)
	return ""
}

// kickList remembers which characters are on a break after GM KICK, by lower-cased name.
// Connection readers check it (resume, recover, login), so it has its own lock.
type kickList struct {
	mu    sync.Mutex
	until map[string]time.Time
}

func (k *kickList) set(name string, until time.Time) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.until == nil {
		k.until = make(map[string]time.Time)
	}
	now := time.Now()
	for n, t := range k.until {
		if !t.After(now) {
			delete(k.until, n)
		}
	}
	k.until[strings.ToLower(name)] = until
}

// remaining is how much of the break is left for name; zero if none.
func (k *kickList) remaining(name string, now time.Time) time.Duration {
	k.mu.Lock()
	defer k.mu.Unlock()
	if t := k.until[strings.ToLower(name)]; t.After(now) {
		return t.Sub(now)
	}
	return 0
}

// refuseIfKicked tells a kicked character's connection they are on a break, and reports
// whether it refused. Reader goroutines call it before doing any database work for them.
func (ms *MudServer) refuseIfKicked(session *Session, name string) bool {
	d := ms.kicks.remaining(name, time.Now())
	if d <= 0 {
		return false
	}
	session.sendAuth(false, codeKicked, fmt.Sprintf("A GM asked this character to take a break. Please try again in about %d minutes.", int((d+time.Minute-1)/time.Minute)))
	return true
}

// findPlayerFold finds an online player by name, ignoring case. Loop only.
func (ms *MudServer) findPlayerFold(name string) *MudPlayer {
	if p := ms.players[name]; p != nil {
		return p
	}
	for n, p := range ms.players {
		if strings.EqualFold(n, name) {
			return p
		}
	}
	return nil
}

// relocate moves a player to a room, with the usual arrival and departure notices, and shows them the
// room. It skips the crowd caps: a GM put them there. Loop only.
func (ms *MudServer) relocate(p *MudPlayer, dest *Room) { relocatePlayer(p, dest) }

// audit records a GM command, in the log now and in the database off the loop. The time is
// taken here, on the loop, so the trail is in the order things happened even though the
// inserts run concurrently.
func (ms *MudServer) audit(gm *MudPlayer, command, args, result string) {
	fmt.Printf("GM %s: %s %s -> %s\n", gm.Username, command, args, result)
	name, at := gm.Username, time.Now()
	ms.offLoop(func() {
		if _, err := ms.DB.Connection.Exec(ms.DB.Context,
			`INSERT INTO gm_audit (at, gm, command, args, result) VALUES ($1, $2, $3, $4, $5)`, at, name, command, args, result); err != nil {
			fmt.Println("gm_audit insert error: " + err.Error())
		}
	})
}

// saveMute persists a player's mute, so it survives a restart.
func (ms *MudServer) saveMute(p *MudPlayer) {
	id, until := p.ID, p.mutedUntil
	if id == 0 {
		return
	}
	var v any
	if !until.IsZero() {
		v = until
	}
	ms.offLoop(func() {
		if _, err := ms.DB.Connection.Exec(ms.DB.Context, `UPDATE player SET muteduntil=$2 WHERE id=$1`, id, v); err != nil {
			fmt.Println("saveMute error: " + err.Error())
		}
	})
}

// offLoop runs database work away from the game loop, which never does I/O.
func (ms *MudServer) offLoop(f func()) {
	ms.offLoopWG.Add(1)
	go func() {
		defer ms.offLoopWG.Done()
		f()
	}()
}

// AddGM makes name a game master with the given password: a new password account, or an
// existing one (its password is replaced). A guest's name is refused. It returns whether
// the account was created. It is what `mud_server -add-gm` runs, on the host.
func AddGM(c Configuration, name, password string) (created bool, err error) {
	name = strings.TrimSpace(name)
	switch {
	case len(name) < 2 || len(name) > 32:
		return false, errors.New("the name must be 2 to 32 characters")
	case strings.IndexFunc(name, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0:
		return false, errors.New("the name cannot contain spaces or control characters")
	case len(password) < minGMPasswordLength:
		return false, fmt.Errorf("the password must be at least %d characters", minGMPasswordLength)
	case len(password) > 72:
		return false, errors.New("the password must be at most 72 characters (bcrypt's limit)")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return false, err
	}

	db := muddatabase.CreateDatabase(c.DatabaseConnectionURL(), 2)
	defer db.Connection.Close()
	if err := db.WaitUntilReachable(15 * time.Second); err != nil {
		return false, errors.New("database unreachable: " + err.Error())
	}
	if err := db.EnsureSchema(); err != nil {
		return false, errors.New("schema update failed: " + err.Error())
	}

	var id int32
	var guest bool
	err = db.Connection.QueryRow(db.Context, `SELECT id, guest FROM player WHERE lower(username)=lower($1)`, name).Scan(&id, &guest)
	switch {
	case err == nil && guest:
		return false, errors.New("that name belongs to a guest character; choose another")
	case err == nil:
		_, err = db.Connection.Exec(db.Context, `UPDATE player SET gm=true, passwordhash=$2 WHERE id=$1`, id, string(hash))
		return false, err
	case !errors.Is(err, pgx.ErrNoRows):
		return false, err
	}

	// A new account. The world places a character whose saved room is missing at the start room.
	var roomID int32
	if err := db.Connection.QueryRow(db.Context, `SELECT id FROM room ORDER BY (contentkey IS NULL), id LIMIT 1`).Scan(&roomID); err != nil {
		return false, errors.New("the database has no rooms: start the server once first")
	}
	mp := CreateMudPlayer(nil, PlayerStartStats{
		HP: c.Game.PlayerStartHP, MaxHP: c.Game.PlayerStartMaxHP, MP: c.Game.PlayerStartMP, MaxMP: c.Game.PlayerStartMaxMP,
		Attack: c.Game.PlayerStartAttack, Defense: c.Game.PlayerStartDefense,
	}, roomID)
	mp.Username = name
	mp.PasswordHash = string(hash)
	if err := mp.InsertIntoDB(&db); err != nil {
		return false, err
	}
	_, err = db.Connection.Exec(db.Context, `UPDATE player SET gm=true WHERE id=$1`, mp.ID)
	return true, err
}

// RunAddGM is `mud_server -add-gm NAME`: it reads the password (twice, without echo, on a
// terminal; one line from standard input otherwise, for scripts) and calls AddGM against the
// configured database. It returns the process exit code.
func RunAddGM(args []string, stdin *os.File, stdout, stderr io.Writer) int {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(stderr, "usage: mud_server -add-gm NAME   (the password is read from the terminal, or one line from standard input)")
		return 2
	}
	var password string
	if term.IsTerminal(int(stdin.Fd())) {
		fmt.Fprintf(stdout, "Password for %s (at least %d characters): ", args[0], minGMPasswordLength)
		p1, err := term.ReadPassword(int(stdin.Fd()))
		fmt.Fprintln(stdout)
		if err != nil {
			fmt.Fprintln(stderr, "add-gm:", err)
			return 1
		}
		fmt.Fprint(stdout, "Again: ")
		p2, err := term.ReadPassword(int(stdin.Fd()))
		fmt.Fprintln(stdout)
		if err != nil || string(p1) != string(p2) {
			fmt.Fprintln(stderr, "add-gm: the passwords did not match")
			return 1
		}
		password = string(p1)
	} else {
		data, err := io.ReadAll(io.LimitReader(stdin, 1024))
		if err != nil {
			fmt.Fprintln(stderr, "add-gm:", err)
			return 1
		}
		password = strings.TrimRight(string(data), "\r\n")
	}
	created, err := AddGM(loadConfiguration(), args[0], password)
	if err != nil {
		fmt.Fprintln(stderr, "add-gm:", err)
		return 1
	}
	if created {
		fmt.Fprintf(stdout, "Created the game master account %q.\n", args[0])
	} else {
		fmt.Fprintf(stdout, "%q is now a game master (password replaced).\n", args[0])
	}
	return 0
}
