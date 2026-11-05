package mudserver

import (
	"os"
	"testing"
)

func TestDatabaseConnectionURLFromFields(t *testing.T) {
	c := Configuration{}
	c.Database.DatabaseHost = "db.example"
	c.Database.DatabasePort = "5433"
	c.Database.DatabaseUsername = "mud"
	c.Database.DatabasePassword = "pw"
	c.Database.DatabaseName = "game"

	want := "postgres://mud:pw@db.example:5433/game"
	if got := c.DatabaseConnectionURL(); got != want {
		t.Errorf("DatabaseConnectionURL() = %q, want %q", got, want)
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("MUD_LISTEN_ADDR", "0.0.0.0:9000")
	t.Setenv("MUD_DATABASE_URL", "postgres://u:p@h:1/d")

	c := Configuration{}
	c.Server.ListenPort = "127.0.0.1:8080"
	c.Database.DatabaseHost = "localhost"
	c.ApplyEnvOverrides()

	if c.Server.ListenPort != "0.0.0.0:9000" {
		t.Errorf("ListenPort = %q, want override", c.Server.ListenPort)
	}
	if got := c.DatabaseConnectionURL(); got != "postgres://u:p@h:1/d" {
		t.Errorf("DatabaseConnectionURL() = %q, want MUD_DATABASE_URL", got)
	}
}

func TestEnvOverridesLeaveConfigAloneWhenUnset(t *testing.T) {
	t.Setenv("MUD_LISTEN_ADDR", "")
	t.Setenv("MUD_DATABASE_URL", "")

	c := Configuration{}
	c.Server.ListenPort = "127.0.0.1:8080"
	c.ApplyEnvOverrides()

	if c.Server.ListenPort != "127.0.0.1:8080" {
		t.Errorf("ListenPort = %q, want unchanged", c.Server.ListenPort)
	}
	if c.Database.DatabaseURL != "" {
		t.Errorf("DatabaseURL = %q, want empty", c.Database.DatabaseURL)
	}
}

func TestLoadConfigFile(t *testing.T) {
	c := Configuration{}
	c = c.LoadConfig("../../../config/server_config.json")

	if c.Server.ListenPort == "" {
		t.Error("ListenPort empty; config file not parsed")
	}
	if c.Game.PlayerStartHP <= 0 {
		t.Errorf("PlayerStartHP = %d, want > 0", c.Game.PlayerStartHP)
	}
}

func TestProcessAliases(t *testing.T) {
	ms := &MudServer{}
	cases := []struct {
		in, inArgs     string
		wantCmd, wantA string
	}{
		{"N", "", "MOVE", "NORTH"},
		{"NORTH", "", "MOVE", "NORTH"},
		{"S", "", "MOVE", "SOUTH"},
		{"W", "", "MOVE", "WEST"},
		{"EAST", "", "MOVE", "EAST"},
		{"UP", "", "MOVE", "UP"},
		{"DOWN", "", "MOVE", "DOWN"},
		{"L", "", "LOOK", ""},
		{"I", "", "INVENTORY", ""},
		{"INV", "", "INVENTORY", ""},
		{"SAY", "hello there", "SAY", "hello there"},
	}
	for _, tc := range cases {
		cmd, args := ms.processAliases(tc.in, tc.inArgs)
		if cmd != tc.wantCmd || args != tc.wantA {
			t.Errorf("processAliases(%q, %q) = (%q, %q), want (%q, %q)", tc.in, tc.inArgs, cmd, args, tc.wantCmd, tc.wantA)
		}
	}
}

func TestCrowdConfigParses(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/c.json"
	body := `{"Crowd":{"SoftCap":40,"MaxCopies":7,"Areas":[{"Name":"start","Rooms":[1,2,3]},{"Name":"hub","Rooms":[9]}]}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c := Configuration{}.LoadConfig(path)
	if c.Crowd.SoftCap != 40 || c.Crowd.MaxCopies != 7 || len(c.Crowd.Areas) != 2 {
		t.Fatalf("Crowd = %+v", c.Crowd)
	}
	if a := c.Crowd.Areas[0]; a.Name != "start" || len(a.Rooms) != 3 || a.Rooms[2] != 3 {
		t.Errorf("first area = %+v", a)
	}
}

func TestShippedConfigCopiesTheSeededStartingArea(t *testing.T) {
	c := Configuration{}.LoadConfig("../../../config/server_config.json")
	if len(c.Crowd.Areas) != 1 || c.Crowd.Areas[0].Name != "start" || len(c.Crowd.Areas[0].Rooms) != 3 {
		t.Errorf("shipped Crowd.Areas = %+v, want the 3 seeded rooms as the start area", c.Crowd.Areas)
	}
	if c.Crowd.SoftCap != 50 || c.Crowd.MaxCopies != 200 {
		t.Errorf("shipped caps = %d/%d, want 50/200", c.Crowd.SoftCap, c.Crowd.MaxCopies)
	}
}
