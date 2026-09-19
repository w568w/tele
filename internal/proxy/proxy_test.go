package proxy_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/proxy"
)

func TestParse_RoutesThatDialNobody(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  proxy.Config
		want string
	}{
		{"an empty section is auto", proxy.Config{}, proxy.TypeAuto},
		{"auto", proxy.Config{Type: "auto"}, proxy.TypeAuto},
		{"direct", proxy.Config{Type: "direct"}, proxy.TypeDirect},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := proxy.Parse(tc.cfg)
			require.NoError(t, err)
			assert.Equal(t, tc.want, r.Type)
			assert.Empty(t, r.Addr, "there is nothing to dial")
		})
	}
}

func TestParse_MTProto(t *testing.T) {
	r, err := proxy.Parse(proxy.Config{
		Type:   "mtproto",
		Server: "127.0.0.1",
		Port:   1443,
		Secret: eeHex,
	})
	require.NoError(t, err)

	assert.Equal(t, proxy.TypeMTProto, r.Type)
	assert.Equal(t, "127.0.0.1:1443", r.Addr)
	assert.Equal(t, proxy.SecretEE, r.Secret.Kind)
}

func TestParse_SOCKS5(t *testing.T) {
	t.Run("without credentials", func(t *testing.T) {
		r, err := proxy.Parse(proxy.Config{Type: "socks5", Server: "10.0.0.1", Port: 1080})
		require.NoError(t, err)
		assert.Equal(t, "10.0.0.1:1080", r.Addr)
		assert.Empty(t, r.Username)
	})

	t.Run("with them", func(t *testing.T) {
		r, err := proxy.Parse(proxy.Config{
			Type: "socks5", Server: "10.0.0.1", Port: 1080,
			Username: "me", Password: "secret",
		})
		require.NoError(t, err)
		assert.Equal(t, "me", r.Username)
		assert.Equal(t, "secret", r.Password)
	})
}

// A route tele cannot take stops the start, so every refusal has to say which
// key is wrong and what would be right - it is read on a terminal by somebody
// who has no tele in front of them.
func TestParse_Refusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  proxy.Config
		says string
	}{
		{"a type nobody declared", proxy.Config{Type: "http"}, "auto, direct, mtproto, socks5"},
		{"a type spelled differently", proxy.Config{Type: "MTProto"}, "auto, direct, mtproto, socks5"},
		{"no server", proxy.Config{Type: "socks5", Port: 1080}, "proxy.server"},
		{"no port", proxy.Config{Type: "socks5", Server: "10.0.0.1"}, "proxy.port"},
		{"a port that is not one", proxy.Config{Type: "socks5", Server: "10.0.0.1", Port: 70000}, "proxy.port"},
		{"mtproto without a secret", proxy.Config{Type: "mtproto", Server: "10.0.0.1", Port: 443}, "proxy.secret"},
		{"mtproto with a secret that is not one", proxy.Config{Type: "mtproto", Server: "10.0.0.1", Port: 443, Secret: "zzz"}, "proxy.secret"},
		{
			"socks5 with half a login",
			proxy.Config{Type: "socks5", Server: "10.0.0.1", Port: 1080, Username: "me"},
			"proxy.username is set without proxy.password",
		},
		{
			"socks5 with the other half",
			proxy.Config{Type: "socks5", Server: "10.0.0.1", Port: 1080, Password: "secret"},
			"proxy.password is set without proxy.username",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := proxy.Parse(tc.cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.says)
		})
	}
}

// A value that belongs to another type changes nothing about the connection,
// which is exactly why it has to be said out loud: whoever wrote it believes it
// is in use.
func TestNotices_NamesValuesThisRouteIgnores(t *testing.T) {
	t.Setenv("ALL_PROXY", "")

	for _, tc := range []struct {
		name  string
		cfg   proxy.Config
		says  []string
		quiet []string
	}{
		{
			name:  "a secret under socks5",
			cfg:   proxy.Config{Type: "socks5", Server: "10.0.0.1", Port: 1080, Secret: plainHex},
			says:  []string{"proxy.secret", "ignored"},
			quiet: []string{"proxy.server", "proxy.port"},
		},
		{
			name:  "credentials under mtproto",
			cfg:   proxy.Config{Type: "mtproto", Server: "10.0.0.1", Port: 443, Secret: plainHex, Username: "me", Password: "secret"},
			says:  []string{"proxy.username", "proxy.password"},
			quiet: []string{"proxy.secret"},
		},
		{
			name: "an address under direct",
			cfg:  proxy.Config{Type: "direct", Server: "10.0.0.1", Port: 1080},
			says: []string{"proxy.server", "proxy.port"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			notices := proxy.Notices(tc.cfg)
			require.Len(t, notices, 1)
			assert.Empty(t, notices[0].ID, "still true in the file, so it is said at every launch")
			for _, want := range tc.says {
				assert.Contains(t, notices[0].Text, want)
			}
			for _, unwanted := range tc.quiet {
				assert.NotContains(t, notices[0].Text, unwanted, "this key is in use, not ignored")
			}
		})
	}
}

func TestNotices_SaysNothingAboutARouteThatUsesEverythingItWasGiven(t *testing.T) {
	t.Setenv("ALL_PROXY", "")

	assert.Empty(t, proxy.Notices(proxy.Config{
		Type: "socks5", Server: "10.0.0.1", Port: 1080, Username: "me", Password: "secret",
	}))
}

func TestNotices_ALLPROXY(t *testing.T) {
	t.Run("in use, so it is announced once", func(t *testing.T) {
		t.Setenv("ALL_PROXY", "socks5://10.0.0.1:1080")

		notices := proxy.Notices(proxy.Config{Type: "auto"})
		require.Len(t, notices, 1)
		assert.NotEmpty(t, notices[0].ID, "a line that wants moving is said once, not at every launch")
		assert.Contains(t, notices[0].Text, "deprecated")
	})

	// x/net reads this variable, refuses the scheme, and connects directly
	// without a word. Somebody believing their traffic is proxied is the whole
	// problem, so this one is said every launch.
	t.Run("set to something that cannot be dialled", func(t *testing.T) {
		t.Setenv("ALL_PROXY", "http://10.0.0.1:8080")

		notices := proxy.Notices(proxy.Config{Type: "auto"})
		require.Len(t, notices, 1)
		assert.Empty(t, notices[0].ID)
		assert.Contains(t, notices[0].Text, "goes direct")
	})

	t.Run("unset", func(t *testing.T) {
		t.Setenv("ALL_PROXY", "")

		assert.Empty(t, proxy.Notices(proxy.Config{Type: "auto"}))
	})

	// The config outranks the variable, and a route named in the file is not
	// the place to bring it up.
	t.Run("not mentioned when the file names a route", func(t *testing.T) {
		t.Setenv("ALL_PROXY", "socks5://10.0.0.1:1080")

		assert.Empty(t, proxy.Notices(proxy.Config{Type: "socks5", Server: "10.0.0.2", Port: 1080}))
		assert.Empty(t, proxy.Notices(proxy.Config{Type: "direct"}))
	})
}
