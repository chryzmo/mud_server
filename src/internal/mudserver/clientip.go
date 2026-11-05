package mudserver

import (
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"
)

/*
Client IPs (plans/guest-sessions.md). Players reach the game through Cloudflare and
then Caddy, so the game server's own peer is always Caddy. Caddy works out the real
client address (it trusts Cloudflare's CF-Connecting-IP only when the connection
really comes from Cloudflare's published ranges; see deploy/Caddyfile) and passes
it on in a header. The game server believes that header only when its own peer is
one of TrustedProxies, which by default is just the loopback addresses Caddy uses
inside the pod. Anyone else who sets the header is ignored.

The address is for logs, abuse forensics and one optional safety net, the per-IP
ceiling below. It is never a fairness rule: venue WiFi and carrier NAT put many
players behind one address, so the ceiling is off unless staff switch it on.
*/

const defaultClientIPHeader = "X-Client-IP"

// defaultTrustedProxies are the addresses Caddy connects from inside the pod.
var defaultTrustedProxies = []string{"127.0.0.1/32", "::1/128"}

// proxyTrust decides which peers may tell us a client's address.
type proxyTrust struct {
	prefixes []netip.Prefix
	header   string
}

// newProxyTrust parses CIDRs (or single addresses). A nil list means the default
// loopback proxies; an empty, non-nil list trusts nobody.
func newProxyTrust(cidrs []string, header string) (*proxyTrust, error) {
	if cidrs == nil {
		cidrs = defaultTrustedProxies
	}
	if header == "" {
		header = defaultClientIPHeader
	}
	t := &proxyTrust{header: header}
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if prefix, err := netip.ParsePrefix(c); err == nil {
			t.prefixes = append(t.prefixes, prefix.Masked())
			continue
		}
		addr, err := netip.ParseAddr(c)
		if err != nil {
			return nil, fmt.Errorf("trusted proxy %q is neither a CIDR nor an address", c)
		}
		t.prefixes = append(t.prefixes, netip.PrefixFrom(addr.Unmap(), addr.Unmap().BitLen()))
	}
	return t, nil
}

func (t *proxyTrust) trusts(addr netip.Addr) bool {
	for _, p := range t.prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// clientIP returns the address of whoever is really connecting: the peer itself,
// or, if the peer is a trusted proxy that supplied a valid address, that address.
// The zero Addr means unknown.
func (t *proxyTrust) clientIP(r *http.Request) netip.Addr {
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}
	}
	addr := peer.Addr().Unmap()
	if t == nil || !t.trusts(addr) {
		return addr
	}
	if forwarded, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get(t.header))); err == nil {
		return forwarded.Unmap()
	}
	return addr
}

// ClientIP is the address of the player behind this websocket request.
func (ms *MudServer) ClientIP(r *http.Request) netip.Addr { return ms.proxies.clientIP(r) }

// ipKey groups an address for per-IP accounting: IPv4 as is, IPv6 by /64 (a
// household or venue gets a whole /64, so one device cannot dodge by rotating
// addresses inside it).
func ipKey(addr netip.Addr) netip.Addr {
	if addr.Is6() && !addr.Is4In6() {
		p, _ := addr.Prefix(64)
		return p.Addr()
	}
	return addr.Unmap()
}

// logIP notes a join-type event with the client's address, for abuse forensics.
// Addresses are personal data; the server can be told not to log them
// (Server.LogClientIPs false).
func (ms *MudServer) logIP(session *Session, format string, args ...any) {
	if ms.Configuration.Server.LogClientIPs != nil && !*ms.Configuration.Server.LogClientIPs {
		return
	}
	ip := "unknown"
	if session.clientIP.IsValid() {
		ip = session.clientIP.String()
	}
	out := ms.Configuration.Server.LogWriter
	if out == nil {
		out = os.Stdout
	}
	fmt.Fprintf(out, "%s (ip %s)\n", fmt.Sprintf(format, args...), ip)
}

// ipLimiter is the optional per-IP ceiling: a token bucket per address group,
// refilling at perMinute/60 a second with a burst of perMinute. Nil means off.
// It is a flood breaker for staff to size by observation, not a fairness rule.
type ipLimiter struct {
	mu        sync.Mutex
	perMinute float64
	buckets   map[netip.Addr]*ipBucket
	now       func() time.Time
	jitter    func() float64
	sweptAt   time.Time
	refused   int64
	loggedAt  time.Time
}

type ipBucket struct {
	tokens float64
	last   time.Time
}

const (
	maxTrackedIPs = 200000
	ipSweepEvery  = time.Minute
	ipBucketIdle  = 2 * time.Minute
	ipRetryJitter = 500 * time.Millisecond
)

func newIPLimiter(perMinute int) *ipLimiter {
	if perMinute <= 0 {
		return nil
	}
	l := &ipLimiter{perMinute: float64(perMinute), buckets: make(map[netip.Addr]*ipBucket), now: time.Now, jitter: rand.Float64}
	l.sweptAt = l.now()
	return l
}

// allow spends one of addr's tokens, or says how long until it has one.
func (l *ipLimiter) allow(addr netip.Addr) (ok bool, retryAfter time.Duration) {
	if l == nil || !addr.IsValid() {
		return true, 0
	}
	key := ipKey(addr)
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)
	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) >= maxTrackedIPs {
			return true, 0 // fail open rather than grow without bound
		}
		b = &ipBucket{tokens: l.perMinute, last: now}
		l.buckets[key] = b
	}
	rate := l.perMinute / 60
	if now.After(b.last) {
		b.tokens = min(l.perMinute, b.tokens+now.Sub(b.last).Seconds()*rate)
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / rate * float64(time.Second))
	retryAfter = max(wait+time.Duration(l.jitter()*float64(ipRetryJitter)), time.Millisecond)
	l.refused++
	if now.Sub(l.loggedAt) > 10*time.Second {
		fmt.Printf("Per-IP join ceiling: refused %d since the last report (latest %s, limit %.0f a minute)\n", l.refused, key, l.perMinute)
		l.refused, l.loggedAt = 0, now
	}
	return false, retryAfter
}

// sweep forgets addresses that have been quiet long enough to be back at full.
func (l *ipLimiter) sweep(now time.Time) {
	if now.Sub(l.sweptAt) < ipSweepEvery {
		return
	}
	l.sweptAt = now
	for key, b := range l.buckets {
		if now.Sub(b.last) > ipBucketIdle {
			delete(l.buckets, key)
		}
	}
}
