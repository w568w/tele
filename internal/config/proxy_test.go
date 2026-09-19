package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/config"
)

// The proxy section is the one part of the config that reads the environment,
// so the environment is taken out of the picture for the whole package. A
// developer who has ALL_PROXY exported - which is the only way to use a proxy
// with tele before this - would otherwise see launch notices appear in tests
// about themes.
func TestMain(m *testing.M) {
	for _, name := range []string{"ALL_PROXY", "all_proxy"} {
		if err := os.Unsetenv(name); err != nil {
			panic(err)
		}
	}
	os.Exit(m.Run())
}

func readAll(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(raw)
}

func proxyConfig(t *testing.T, section string) (*config.Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, os.WriteFile(path, []byte("telegram:\n  api_id: 1\n  api_hash: x\n"+section), 0600))
	return config.Load(path, t.TempDir())
}

func TestLoad_ProxySection(t *testing.T) {
	cfg, err := proxyConfig(t, `
proxy:
  type: mtproto
  server: 127.0.0.1
  port: 1443
  secret: ee0123456789abcdef0123456789abcdef6578616d706c652e636f6d
`)
	require.NoError(t, err)

	assert.Equal(t, "mtproto", cfg.Proxy.Type)
	assert.Equal(t, "127.0.0.1", cfg.Proxy.Server)
	assert.Equal(t, 1443, cfg.Proxy.Port)
	assert.Empty(t, cfg.Warnings)
}

// Absence is auto, which is tele doing what it did before this section existed.
func TestLoad_NoProxySection(t *testing.T) {
	cfg, err := proxyConfig(t, "")
	require.NoError(t, err)

	assert.Equal(t, "auto", cfg.Proxy.Type)
	assert.Empty(t, cfg.Warnings)
}

// Every other section is repaired to its default and reported. This one stops
// the load, because its default is a direct connection to Telegram and arriving
// there by accident is what the section exists to prevent (ADR 0017).
func TestLoad_RefusesARouteTeleCannotTake(t *testing.T) {
	for _, tc := range []struct {
		name, section, says string
	}{
		{"a type nobody declared", "proxy:\n  type: sneakernet\n", "proxy.type"},
		{"mtproto with no secret", "proxy:\n  type: mtproto\n  server: 127.0.0.1\n  port: 1443\n", "proxy.secret"},
		{"mtproto with a secret that is not one", "proxy:\n  type: mtproto\n  server: 127.0.0.1\n  port: 1443\n  secret: nonsense!\n", "proxy.secret"},
		{"socks5 with no address", "proxy:\n  type: socks5\n  port: 1080\n", "proxy.server"},
		{"a port that is not one", "proxy:\n  type: socks5\n  server: 10.0.0.1\n  port: 70000\n", "proxy.port"},
		{"socks5 with half a login", "proxy:\n  type: socks5\n  server: 10.0.0.1\n  port: 1080\n  username: me\n", "proxy.password"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := proxyConfig(t, tc.section)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.says, "the refusal names the key")
			assert.Contains(t, err.Error(), "config.yml", "and the file it is in")
			assert.Contains(t, err.Error(), "proxy.type: direct", "and the one edit that connects without a proxy")
		})
	}
}

// A value of the wrong shape never reaches the route: the file refuses to
// decode into the section at all, the way it would for any other field. The
// message is viper's rather than ours, and it still names the key.
func TestLoad_RefusesAPortThatIsNotANumber(t *testing.T) {
	_, err := proxyConfig(t, "proxy:\n  type: socks5\n  server: 10.0.0.1\n  port: nineteen\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "proxy.port")
}

// The contrast in one file: a toast zone tele will not accept is put back to
// its default and reported, and a proxy in the same file is not.
func TestLoad_RepairsEverythingButTheRoute(t *testing.T) {
	repaired, err := proxyConfig(t, "ui:\n  toasts:\n    error_zone: bottom-middle\n")
	require.NoError(t, err)
	assert.Equal(t, "bottom-right", repaired.UI.Toasts.ErrorZone)
	assert.NotEmpty(t, repaired.Warnings)

	_, err = proxyConfig(t, "proxy:\n  type: bottom-middle\nui:\n  toasts:\n    error_zone: bottom-middle\n")
	assert.Error(t, err, "the same file does not load once the route is the broken part")
}

// A value the route will not read changes nothing, which is exactly why it is
// said out loud: whoever wrote it believes it is in use.
func TestLoad_WarnsAboutValuesTheRouteIgnores(t *testing.T) {
	cfg, err := proxyConfig(t, "proxy:\n  type: socks5\n  server: 10.0.0.1\n  port: 1080\n  secret: 0123456789abcdef0123456789abcdef\n")
	require.NoError(t, err)

	require.Len(t, cfg.Warnings, 1)
	assert.Contains(t, cfg.Warnings[0].Text, "proxy.secret")
	assert.Empty(t, cfg.Warnings[0].ID, "still true in the file, so it is said at every launch")
}

func TestLoad_AnnouncesALLPROXYOnce(t *testing.T) {
	t.Setenv("ALL_PROXY", "socks5://10.0.0.1:1080")

	cfg, err := proxyConfig(t, "")
	require.NoError(t, err)

	require.Len(t, cfg.Warnings, 1)
	assert.NotEmpty(t, cfg.Warnings[0].ID, "a line that only wants moving is said once")
	assert.Contains(t, cfg.Warnings[0].Text, "deprecated")
}

// The overlay writes the file and reads it back afterwards, so a value the
// config will not load has to be refused before it is written - the screen that
// would fix it lives inside a tele that would no longer start.
func TestStore_SetRefusesAProxyValueTeleCouldNotStartWith(t *testing.T) {
	s, path := storeAt(t, "proxy:\n  type: socks5\n  server: 10.0.0.1\n  port: 1080\n")
	before := readAll(t, path)

	err := s.Set("proxy.port", 70000)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "proxy.port")
	assert.Equal(t, before, readAll(t, path), "and the file is untouched")
	assert.Equal(t, 1080, s.Current().Proxy.Port)
}

func TestStore_SetRefusesASecretThatIsNotOne(t *testing.T) {
	s, path := storeAt(t, "proxy:\n  type: mtproto\n  server: 10.0.0.1\n  port: 1443\n  secret: 0123456789abcdef0123456789abcdef\n")
	before := readAll(t, path)

	require.Error(t, s.Set("proxy.secret", "nonsense!"))
	assert.Equal(t, before, readAll(t, path))
}

// Judging the whole section rather than the one key makes the order of edits
// matter: the address and the secret go in while the type still dials nobody.
// The refusal says so, because otherwise it reads as "mtproto is not allowed".
func TestStore_SetTypeLastAndTheSectionIsReadyForIt(t *testing.T) {
	s, _ := storeAt(t, "")

	err := s.Set("proxy.type", "mtproto")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fill in the rest of the section first")

	require.NoError(t, s.Set("proxy.server", "127.0.0.1"))
	require.NoError(t, s.Set("proxy.port", 1443))
	require.NoError(t, s.Set("proxy.secret", "dd0123456789abcdef0123456789abcdef"))
	require.NoError(t, s.Set("proxy.type", "mtproto"))

	assert.Equal(t, "mtproto", s.Current().Proxy.Type)
	assert.Equal(t, 1443, s.Current().Proxy.Port)
}

// Resetting is writing absence, and absence of a route is auto.
func TestStore_SetNilPutsTheRouteBackToAuto(t *testing.T) {
	s, _ := storeAt(t, "proxy:\n  type: direct\n")

	require.NoError(t, s.Set("proxy.type", nil))

	assert.Equal(t, "auto", s.Current().Proxy.Type)
	assert.True(t, s.IsDefault("proxy.type"))
}
