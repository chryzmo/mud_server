package mudserver

import "testing"

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
