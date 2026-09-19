package proxy

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/gotd/td/telegram/dcs"
	xproxy "golang.org/x/net/proxy"
)

// ProbeTimeout bounds the one dial tele makes to a declared proxy before it
// draws anything. Long enough for a proxy on the other side of the world,
// short enough that a port nobody listens on is reported rather than waited
// for.
const ProbeTimeout = 5 * time.Second

// Resolver builds what gotd dials Telegram through. One resolver serves every
// data centre, primary and media alike, so a photo cannot take a different road
// than the message it came with.
func Resolver(r Route) (dcs.Resolver, error) {
	switch r.Type {
	case TypeAuto:
		// What tele has done since v1.3.0: whatever ALL_PROXY says, and a
		// direct connection when it says nothing. x/net decides that for
		// itself, including NO_PROXY, and is left to.
		return dcs.Plain(dcs.PlainOptions{Dial: dialFunc(xproxy.FromEnvironment())}), nil
	case TypeDirect:
		return dcs.Plain(dcs.PlainOptions{}), nil
	case TypeSOCKS5:
		var auth *xproxy.Auth
		if r.Username != "" {
			auth = &xproxy.Auth{User: r.Username, Password: r.Password}
		}
		d, err := xproxy.SOCKS5("tcp", r.Addr, auth, xproxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("socks5 proxy %s: %w", r.Addr, err)
		}
		return dcs.Plain(dcs.PlainOptions{Dial: dialFunc(d)}), nil
	case TypeMTProto:
		// gotd reads the shape of the secret again here and picks the
		// obfuscation from it - Obfuscated2 for a plain or dd secret, fake TLS
		// for an ee one - so the secret is handed over whole.
		return dcs.MTProxy(r.Addr, r.Secret.Bytes, dcs.MTProxyOptions{})
	default:
		return nil, fmt.Errorf("proxy.type is %q; it is one of %s", r.Type, joinTypes())
	}
}

// Probe dials the proxy once, so that a server nobody is listening on is a
// message on the terminal rather than an endless reconnect behind a connecting
// screen. It says nothing about the secret or the credentials: those are proved
// against Telegram, and failing them is an ordinary connection failure with the
// interface already up.
//
// Only a declared proxy is probed. A route that dials nobody has nothing to
// probe, and ALL_PROXY was not set by this config - refusing to start over a
// variable somebody exported for another program would be tele overreaching.
func Probe(ctx context.Context, r Route) error {
	if r.Addr == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", r.Addr)
	if err != nil {
		return fmt.Errorf("the %s proxy at %s is not answering: %w", r.Type, r.Addr, err)
	}
	return conn.Close()
}

// Describe is the line the log carries about the route in force. It names the
// address, because a wrong port has to be visible in a log somebody pastes into
// an issue, and it names the kind of secret, because that is the part people
// get wrong. It never names the secret or the password.
func (r Route) Describe() string {
	switch r.Type {
	case TypeAuto:
		u := envProxyURL()
		if u == nil {
			return "auto: no ALL_PROXY, connecting directly"
		}
		port := u.Port()
		if port == "" {
			port = "1080"
		}
		return fmt.Sprintf("auto: ALL_PROXY socks5 via %s (auth: %s)",
			net.JoinHostPort(u.Hostname(), port), yesNo(u.User != nil))
	case TypeDirect:
		return "direct"
	case TypeSOCKS5:
		return fmt.Sprintf("socks5 via %s (auth: %s)", r.Addr, yesNo(r.Username != ""))
	case TypeMTProto:
		return fmt.Sprintf("mtproto via %s (secret: %s)", r.Addr, r.Secret.Kind)
	default:
		return r.Type
	}
}

// dialFunc adapts a x/net dialer to what gotd asks for. The type assertion is
// how that package hands out context support: SOCKS5 and Direct both implement
// it, and the fallback is there for a dialer that does not.
func dialFunc(d xproxy.Dialer) dcs.DialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if cd, ok := d.(xproxy.ContextDialer); ok {
			return cd.DialContext(ctx, network, addr)
		}
		return d.Dial(network, addr)
	}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
