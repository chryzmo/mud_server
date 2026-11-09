package main

import (
	"strings"
	"testing"
)

// USE <item> in a room where a rule takes that item runs the rule, as GIVE, THROW or PUT would; when several
// rules apply it says what to type; with none it does nothing, as before.

func TestUseAnItemRunsTheOneRuleThatTakesItHere(t *testing.T) {
	t.Parallel()
	conn := puzzler(t, puzzleServer(t, nil), "user")
	send(t, conn, "MOVE N")
	expect(t, conn, "You are in 'Gallery'")
	send(t, conn, "SEARCH booth")
	expect(t, conn, "You receive \x1b[1;32mTICKET\x1b[0m.")

	// No rule takes the ticket in the Gallery: nothing happens.
	send(t, conn, "USE ticket")
	expect(t, conn, "Nothing happens.")

	// In the Hall the porter takes it (GIVE ticket TO porter), so USE does that.
	send(t, conn, "MOVE S")
	expect(t, conn, "You are in 'Hall'")
	send(t, conn, "USE ticket")
	expect(t, conn, "Porter Bellweather beams and hands you a brass key.")
	send(t, conn, "INVENTORY")
	if out := expect(t, conn, "BRASS KEY"); strings.Contains(out, "TICKET") {
		t.Errorf("the ticket should have been given away: %q", out)
	}
}

const useCoinWorld = `
start_room: hall
rooms:
  hall:
    name: Hall
    description: A hall.
    features:
      fountain: {names: [fountain], examine: A fountain.}
    people:
      porter: {names: [Porter], examine: A porter., greeting: Hello.}
    rules:
      - on: {enter: true}
        unless: [{has_item: coin}]
        do: [{give_item: coin}]
      - on: {verb: GIVE, target: porter, with: coin}
        do: [{take_item: coin}, {say: The porter pockets the coin.}]
      - on: {verb: THROW, target: fountain, with: coin}
        do: [{take_item: coin}, {say: The coin sinks with a plink.}]
items:
  coin: {name: COIN, description: A coin.}
`

func TestUseAnItemWithSeveralWaysHereListsThem(t *testing.T) {
	t.Parallel()
	wsURL := cafeServer(t, useCoinWorld, nil)
	conn := dial(t, wsURL)
	expect(t, conn, "Welcome to the MUD!")
	loginNew(t, conn, "spender")
	expect(t, conn, "You are in 'Hall'")

	send(t, conn, "USE coin")
	out := expect(t, conn, "more than one thing to do")
	for _, want := range []string{"GIVE COIN TO Porter", "THROW COIN INTO fountain"} {
		if !strings.Contains(out, want) {
			t.Errorf("the choices lack %q: %q", want, out)
		}
	}
	send(t, conn, "INVENTORY") // nothing was used up
	expect(t, conn, "COIN")

	send(t, conn, "GIVE coin TO porter")
	expect(t, conn, "The porter pockets the coin.")
	send(t, conn, "USE coin")
	expect(t, conn, "You do not see that here.") // it is gone from the pack, and nothing here is called that
}
