package mudserver

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type Configuration struct {
	Server struct {
		ServerName	string `json:"ServerName"`
		ListenPort	string `json:"ListenPort"`
		SaveIntervalMS int `json:"SaveIntervalMS"` // batched saves of changed players and rooms (default 5000)
		// Peers allowed to tell us a client's address in ClientIPHeader (CIDRs or addresses).
		// Absent: the loopback addresses Caddy uses inside the pod. An empty list trusts nobody.
		TrustedProxies []string `json:"TrustedProxies"`
		ClientIPHeader string `json:"ClientIPHeader"` // default X-Client-IP
		LogClientIPs *bool `json:"LogClientIPs"` // log client addresses on join/login/recover (default true)
		LogWriter io.Writer `json:"-"` // where those lines go; nil is standard output (tests inject their own)
	} `json:"Server"`
	// Per-connection tuning; zero means the default (see SessionSettings).
	Connection struct {
		ReadLimitBytes    int64 `json:"ReadLimitBytes"`
		CommandsPerSecond int `json:"CommandsPerSecond"`
		CommandBurst      int `json:"CommandBurst"`
		SayPerSecond      int `json:"SayPerSecond"`
		SayBurst          int `json:"SayBurst"`
		OutboundQueueSize int `json:"OutboundQueueSize"`
		WriteTimeoutMS    int `json:"WriteTimeoutMS"`
		PingIntervalMS    int `json:"PingIntervalMS"`
		PongTimeoutMS     int `json:"PongTimeoutMS"`
		ResumeGraceMS     int `json:"ResumeGraceMS"` // how long a dropped guest stays in the world (default 90000)
		ReplayFrames      int `json:"ReplayFrames"`  // frames kept per player for replay after a resume (default 100)
		RecoveryLockMS    int `json:"RecoveryLockMS"` // a name refuses recovery this long after 5 wrong codes (default 900000)
		JoinPerSecond     int `json:"JoinPerSecond"`  // join/resume/recover/login/register tokens per second, all connections together (default 1000; -1 no limit)
		JoinBurst         int `json:"JoinBurst"`      // bucket size (default 5000)
		MaxJoinsPerMinutePerIP int `json:"MaxJoinsPerMinutePerIP"` // 0 = off. A flood breaker for staff, not a fairness rule: many players share an address at a venue
		IdleTimeoutMS     int `json:"IdleTimeoutMS"`  // a guest with no command for this long is disconnected (default 1800000; -1 never)
	} `json:"Connection"`
	Database struct {
		DatabaseURL string `json:"DatabaseURL"`
		MaxConnections int `json:"MaxConnections"` // database pool size (default 10)
		StartupWaitSeconds int `json:"StartupWaitSeconds"` // how long to retry an unreachable database at startup (default 30)
		DatabaseHost string `json:"DatabaseHost"`
		DatabasePort string `json:"DatabasePort"`
		DatabaseUsername string `json:"DatabaseUsername"`
		DatabasePassword string `json:"DatabasePassword"`
		DatabaseName string `json:"DatabaseName"`
	} `json:"Database"`
	Game struct {
		RoomTickDurationInMS int32 `json:"RoomTickDurationInMS"`
		PlayerStartHP        int   `json:"PlayerStartHP"`
		PlayerStartMaxHP     int   `json:"PlayerStartMaxHP"`
		PlayerStartMP        int   `json:"PlayerStartMP"`
		PlayerStartMaxMP     int   `json:"PlayerStartMaxMP"`
		PlayerStartAttack    int   `json:"PlayerStartAttack"`
		PlayerStartDefense   int   `json:"PlayerStartDefense"`
	} `json:"Game"`
	// Area copies for crowds; none by default. See crowd.go.
	Crowd CrowdConfig `json:"Crowd"`
}

func (c Configuration) LoadConfig (config_file string) (Configuration) {
	if !filepath.IsAbs(config_file) {
		pwd, _ := os.Getwd()
		config_file = pwd + "/" + config_file
	}
	file, _ := os.Open(config_file)
	defer file.Close()

	decoder := json.NewDecoder(file)
	serverconfig := Configuration{}

	err := decoder.Decode(&serverconfig)
	if err != nil {
		panic(err)
	}

	return serverconfig

}

/*
	Environment variables override the config file, so the same file works for
	local development, tests and the VPS.
		MUD_LISTEN_ADDR    overrides Server.ListenPort (e.g. "127.0.0.1:8080")
		MUD_DATABASE_URL   overrides all Database settings (postgres://user:pass@host:port/db)
*/
func (c *Configuration) ApplyEnvOverrides() {
	if v := os.Getenv("MUD_LISTEN_ADDR"); v != "" {
		c.Server.ListenPort = v
	}
	if v := os.Getenv("MUD_DATABASE_URL"); v != "" {
		c.Database.DatabaseURL = v
	}
}

func (c Configuration) SessionSettings() SessionSettings {
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	return SessionSettings{
		ReadLimitBytes:    c.Connection.ReadLimitBytes,
		CommandsPerSecond: c.Connection.CommandsPerSecond,
		CommandBurst:      c.Connection.CommandBurst,
		SayPerSecond:      c.Connection.SayPerSecond,
		SayBurst:          c.Connection.SayBurst,
		OutboundQueueSize: c.Connection.OutboundQueueSize,
		WriteTimeout:      ms(c.Connection.WriteTimeoutMS),
		PingInterval:      ms(c.Connection.PingIntervalMS),
		PongTimeout:       ms(c.Connection.PongTimeoutMS),
	}
}

// DatabaseConnectionURL returns DatabaseURL if set, otherwise builds one from the individual fields.
func (c Configuration) DatabaseConnectionURL() string {
	if c.Database.DatabaseURL != "" {
		return c.Database.DatabaseURL
	}
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s", c.Database.DatabaseUsername, c.Database.DatabasePassword, c.Database.DatabaseHost, c.Database.DatabasePort, c.Database.DatabaseName)
}
