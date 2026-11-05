package mudserver

import (
	"net/http"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"
)

func request(remote, headerName, headerValue string) *http.Request {
	r, _ := http.NewRequest("GET", "http://example/ws", nil)
	r.RemoteAddr = remote
	if headerName != "" {
		r.Header.Set(headerName, headerValue)
	}
	return r
}

func TestProxyTrustParsing(t *testing.T) {
	def, err := newProxyTrust(nil, "")
	if err != nil || def.header != "X-Client-IP" {
		t.Fatalf("defaults: %v %+v", err, def)
	}
	for _, addr := range []string{"127.0.0.1", "::1", "127.0.0.2"} {
		if got := def.trusts(netip.MustParseAddr(addr)); got != (addr != "127.0.0.2") {
			t.Errorf("default trust of %s = %v, want only 127.0.0.1 and ::1", addr, got)
		}
	}
	none, err := newProxyTrust([]string{}, "")
	if err != nil || none.trusts(netip.MustParseAddr("127.0.0.1")) {
		t.Errorf("an empty list must trust nobody: %v", err)
	}
	mixed, err := newProxyTrust([]string{"10.0.0.0/8", " 192.0.2.7 ", "2001:db8::/32"}, "X-Real")
	if err != nil || mixed.header != "X-Real" {
		t.Fatal(err)
	}
	for addr, want := range map[string]bool{"10.1.2.3": true, "192.0.2.7": true, "192.0.2.8": false, "2001:db8::5": true, "2001:db9::5": false, "11.0.0.1": false} {
		if got := mixed.trusts(netip.MustParseAddr(addr)); got != want {
			t.Errorf("trusts(%s) = %v, want %v", addr, got, want)
		}
	}
	for _, bad := range []string{"not-an-ip", "10.0.0.0/33", "", "10.0.0.0/8 extra"} {
		if _, err := newProxyTrust([]string{bad}, ""); err == nil {
			t.Errorf("trusted proxy %q was accepted", bad)
		}
	}
}

func TestClientIPBelievesTheHeaderOnlyFromATrustedPeer(t *testing.T) {
	trust, _ := newProxyTrust([]string{"127.0.0.1/32", "::1/128"}, "")
	for name, tc := range map[string]struct {
		r    *http.Request
		want string
	}{
		"trusted peer, header":                  {request("127.0.0.1:5000", "X-Client-IP", "203.0.113.9"), "203.0.113.9"},
		"trusted peer, ipv6 header":             {request("127.0.0.1:5000", "X-Client-IP", "2001:db8::7"), "2001:db8::7"},
		"trusted peer, spaces around the value": {request("127.0.0.1:5000", "X-Client-IP", " 203.0.113.9 "), "203.0.113.9"},
		"trusted peer, v4-mapped header":        {request("127.0.0.1:5000", "X-Client-IP", "::ffff:203.0.113.9"), "203.0.113.9"},
		"trusted ipv6 peer":                     {request("[::1]:5000", "X-Client-IP", "203.0.113.9"), "203.0.113.9"},
		"trusted peer, no header":               {request("127.0.0.1:5000", "", ""), "127.0.0.1"},
		"trusted peer, garbage header":          {request("127.0.0.1:5000", "X-Client-IP", "not an ip"), "127.0.0.1"},
		"trusted peer, a list":                  {request("127.0.0.1:5000", "X-Client-IP", "1.1.1.1, 2.2.2.2"), "127.0.0.1"},
		"untrusted peer spoofing the header":    {request("198.51.100.4:5000", "X-Client-IP", "203.0.113.9"), "198.51.100.4"},
		"untrusted ipv6 peer":                   {request("[2001:db8::1]:5000", "X-Client-IP", "203.0.113.9"), "2001:db8::1"},
		"unparseable peer":                      {request("garbage", "X-Client-IP", "203.0.113.9"), ""},
	} {
		got := trust.clientIP(tc.r)
		if (tc.want == "" && got.IsValid()) || (tc.want != "" && got.String() != tc.want) {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
	var nilTrust *proxyTrust
	if got := nilTrust.clientIP(request("198.51.100.4:5000", "X-Client-IP", "203.0.113.9")); got.String() != "198.51.100.4" {
		t.Errorf("no trust configured: got %v, want the peer", got)
	}
}

func TestIPKeyGroupsIPv6By64(t *testing.T) {
	a, b, c := ipKey(netip.MustParseAddr("2001:db8:1:2::1")), ipKey(netip.MustParseAddr("2001:db8:1:2:ffff::9")), ipKey(netip.MustParseAddr("2001:db8:1:3::1"))
	if a != b || a == c {
		t.Errorf("v6 keys: %v %v %v; want the same /64 grouped and a different /64 apart", a, b, c)
	}
	if v4 := ipKey(netip.MustParseAddr("::ffff:203.0.113.9")); v4.String() != "203.0.113.9" {
		t.Errorf("a v4-mapped address keys as %v, want plain IPv4", v4)
	}
	if v4 := ipKey(netip.MustParseAddr("203.0.113.9")); v4.String() != "203.0.113.9" {
		t.Errorf("IPv4 keyed as %v", v4)
	}
}

func ipLimiterAt(perMinute int) (*ipLimiter, *time.Time) {
	now := time.Unix(9000, 0)
	l := newIPLimiter(perMinute)
	l.now = func() time.Time { return now }
	l.sweptAt = now
	l.jitter = func() float64 { return 0 }
	return l, &now
}

func TestIPLimiterIsPerAddressGroupAndRefills(t *testing.T) {
	l, now := ipLimiterAt(6) // 6 a minute: a burst of 6, then one every 10 s
	a, b := netip.MustParseAddr("203.0.113.1"), netip.MustParseAddr("203.0.113.2")
	for i := 0; i < 6; i++ {
		if ok, _ := l.allow(a); !ok {
			t.Fatalf("burst request %d refused", i+1)
		}
	}
	ok, retry := l.allow(a)
	if ok || retry != 10*time.Second {
		t.Fatalf("beyond the burst: ok=%v retry=%v, want a refusal with a 10 s hint", ok, retry)
	}
	if ok, _ := l.allow(b); !ok {
		t.Error("another address was limited by the first one's flood")
	}
	*now = now.Add(10 * time.Second)
	if ok, _ := l.allow(a); !ok {
		t.Error("a token should have refilled after 10 s")
	}
	if ok, _ := l.allow(a); ok {
		t.Error("only one token refilled")
	}

	// IPv6 addresses in one /64 share a bucket.
	l2, _ := ipLimiterAt(2)
	if ok, _ := l2.allow(netip.MustParseAddr("2001:db8:5:5::1")); !ok {
		t.Fatal()
	}
	if ok, _ := l2.allow(netip.MustParseAddr("2001:db8:5:5::2")); !ok {
		t.Fatal()
	}
	if ok, _ := l2.allow(netip.MustParseAddr("2001:db8:5:5:9::3")); ok {
		t.Error("three addresses in one /64 should have shared two tokens")
	}
}

func TestIPLimiterOffUnknownAddressesAndMemory(t *testing.T) {
	var off *ipLimiter
	if ok, _ := off.allow(netip.MustParseAddr("203.0.113.1")); !ok {
		t.Error("a nil limiter must allow")
	}
	if newIPLimiter(0) != nil || newIPLimiter(-5) != nil {
		t.Error("zero or negative must mean off")
	}
	l, now := ipLimiterAt(1)
	if ok, _ := l.allow(netip.Addr{}); !ok {
		t.Error("an unknown address must not be limited (it would lump everyone together)")
	}
	l.allow(netip.MustParseAddr("203.0.113.1"))
	l.allow(netip.MustParseAddr("203.0.113.2"))
	if len(l.buckets) != 2 {
		t.Fatalf("tracking %d addresses", len(l.buckets))
	}
	*now = now.Add(ipSweepEvery + ipBucketIdle)
	l.allow(netip.MustParseAddr("203.0.113.3"))
	if len(l.buckets) != 1 {
		t.Errorf("quiet addresses were not forgotten: %d tracked", len(l.buckets))
	}

	// Past the tracking cap it fails open instead of growing without bound.
	full, _ := ipLimiterAt(1)
	for i := 0; i < maxTrackedIPs; i++ {
		full.buckets[netip.AddrFrom4([4]byte{10, byte(i >> 16), byte(i >> 8), byte(i)})] = &ipBucket{tokens: 1, last: full.now()}
	}
	for i := 0; i < 3; i++ {
		if ok, _ := full.allow(netip.MustParseAddr("203.0.113.200")); !ok {
			t.Fatal("a new address was refused after the table filled")
		}
	}
	if len(full.buckets) != maxTrackedIPs {
		t.Errorf("table grew to %d", len(full.buckets))
	}
}

func TestAllowJoinChecksTheAddressBeforeTheSharedBucket(t *testing.T) {
	ms := &MudServer{joinLimit: newJoinLimiter(1, 100), ipLimit: newIPLimiter(2)}
	flooder := &Session{out: make(chan []byte, 8), closed: make(chan struct{}), clientIP: netip.MustParseAddr("203.0.113.1")}
	other := &Session{out: make(chan []byte, 8), closed: make(chan struct{}), clientIP: netip.MustParseAddr("203.0.113.2")}
	allowed := 0
	for i := 0; i < 50; i++ {
		if ms.allowJoin(flooder) {
			allowed++
		}
	}
	if allowed != 2 {
		t.Fatalf("flooding address was allowed %d times, want its 2", allowed)
	}
	// Its refused requests did not spend the shared bucket (only the 2 allowed ones did),
	// so everyone else still gets in.
	if tokens := ms.joinLimit.tokens; tokens < 97 {
		t.Errorf("the shared bucket was drained to %.0f by one flooding address", tokens)
	}
	if !ms.allowJoin(other) {
		t.Error("a different address was refused while the shared bucket was nearly full")
	}
	frame := string(<-flooder.out)
	if !strings.Contains(frame, `"code":"rate_limited"`) || !strings.Contains(frame, `"retry_ms":`) {
		t.Errorf("refusal = %s", frame)
	}
}

// Caddy and the game server must agree on the header that carries the address,
// and Caddy must trust Cloudflare's ranges for it; deploy/test-client-ip.sh proves
// the behaviour with the real image, this keeps the two names from drifting.
func TestCaddyfileAndServerAgreeOnTheClientIPHeader(t *testing.T) {
	caddyfile, err := os.ReadFile("../../../deploy/Caddyfile")
	if err != nil {
		t.Fatal(err)
	}
	if want := "header_up " + defaultClientIPHeader + " {client_ip}"; !strings.Contains(string(caddyfile), want) {
		t.Errorf("deploy/Caddyfile does not contain %q", want)
	}
	for _, want := range []string{"import cloudflare_ips", "client_ip_headers CF-Connecting-IP"} {
		if !strings.Contains(string(caddyfile), want) {
			t.Errorf("deploy/Caddyfile does not contain %q", want)
		}
	}
	ranges, err := os.ReadFile("../../../deploy/cloudflare-ips.caddy")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ranges), "(cloudflare_ips)") || strings.Count(string(ranges), "/") < 10 {
		t.Error("deploy/cloudflare-ips.caddy does not define the cloudflare_ips snippet with the ranges")
	}
}
