package slackdesktop

import (
	"path/filepath"
	"testing"
)

func TestConfigDirForOS(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	cases := []struct {
		goos string
		env  map[string]string
		want string
	}{
		{"linux", map[string]string{"HOME": "/home/x"}, "/home/x/.config/Slack"},
		{"linux", map[string]string{"HOME": "/home/x", "XDG_CONFIG_DIR": "/cfg"}, "/cfg/Slack"},
		{"windows", map[string]string{"APPDATA": `C:\Users\x\AppData\Roaming`}, filepath.Join(`C:\Users\x\AppData\Roaming`, "Slack")},
	}
	for _, c := range cases {
		got := configDirForOS(c.goos, env(c.env), func(string) bool { return false })
		if got != c.want {
			t.Errorf("configDirForOS(%s) = %q, want %q", c.goos, got, c.want)
		}
	}
}

func TestConfigDirForOSDarwinPrefersFirstExisting(t *testing.T) {
	home := "/Users/x"
	first := filepath.Join(home, "Library", "Application Support", "Slack")
	got := configDirForOS("darwin", func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}, func(p string) bool { return p == first })
	if got != first {
		t.Errorf("darwin config dir = %q, want %q", got, first)
	}
}
