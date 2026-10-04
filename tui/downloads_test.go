package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateDownloads gives the test a scratch $HOME and XDG config dir, and
// clears every downloads override (flag and env), so the resolver never sees
// the real user-dirs.dirs or ~/Download. Returns the scratch home.
func isolateDownloads(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv(downloadsEnv, "")
	orig := downloadsFlag
	downloadsFlag = ""
	t.Cleanup(func() { downloadsFlag = orig })
	return home
}

// writeUserDirs writes $XDG_CONFIG_HOME/user-dirs.dirs with the given
// XDG_DOWNLOAD_DIR value (already quoted as the file would have it).
func writeUserDirs(t *testing.T, home, value string) {
	t.Helper()
	cfg := filepath.Join(home, ".config")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# written by xdg-user-dirs-update\nXDG_DESKTOP_DIR=\"$HOME/Desktop\"\n" +
		"XDG_DOWNLOAD_DIR=" + value + "\n"
	if err := os.WriteFile(filepath.Join(cfg, "user-dirs.dirs"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// TestResolveDownloadsDir pins the resolver's order (flag → env → XDG →
// ~/Downloads → ~/Download) and its refusals: never $HOME (however it's
// spelled — "$HOME/", a symlink to it), never / or an ancestor of $HOME, and
// nothing outside $HOME unless the flag names it.
func TestResolveDownloadsDir(t *testing.T) {
	type env struct{ home, outside string }
	cases := []struct {
		name    string
		setup   func(t *testing.T, e env) // creates dirs, sets flag/env/XDG
		want    func(e env) string        // expected dir (before EvalSymlinks)
		wantErr string                    // substring of the error, if any
	}{
		{
			name: "XDG says $HOME/ and only ~/Download exists (this machine)",
			setup: func(t *testing.T, e env) {
				writeUserDirs(t, e.home, `"$HOME/"`)
				mkdirs(t, filepath.Join(e.home, "Download"))
			},
			want: func(e env) string { return filepath.Join(e.home, "Download") },
		},
		{
			name: "XDG says $HOME/ and nothing else exists",
			setup: func(t *testing.T, e env) {
				writeUserDirs(t, e.home, `"$HOME/"`)
			},
			wantErr: "No downloads folder found",
		},
		{
			name: "~/Download only",
			setup: func(t *testing.T, e env) {
				mkdirs(t, filepath.Join(e.home, "Download"))
			},
			want: func(e env) string { return filepath.Join(e.home, "Download") },
		},
		{
			name: "~/Downloads wins over ~/Download",
			setup: func(t *testing.T, e env) {
				mkdirs(t, filepath.Join(e.home, "Downloads"), filepath.Join(e.home, "Download"))
			},
			want: func(e env) string { return filepath.Join(e.home, "Downloads") },
		},
		{
			name: "XDG dir is used when it is a real folder in $HOME",
			setup: func(t *testing.T, e env) {
				writeUserDirs(t, e.home, `"$HOME/dl"`)
				mkdirs(t, filepath.Join(e.home, "dl"), filepath.Join(e.home, "Downloads"))
			},
			want: func(e env) string { return filepath.Join(e.home, "dl") },
		},
		{
			name: "XDG ${HOME} spelling expands too",
			setup: func(t *testing.T, e env) {
				writeUserDirs(t, e.home, `"${HOME}/dl"`)
				mkdirs(t, filepath.Join(e.home, "dl"))
			},
			want: func(e env) string { return filepath.Join(e.home, "dl") },
		},
		{
			name: "XDG dir outside $HOME is skipped",
			setup: func(t *testing.T, e env) {
				writeUserDirs(t, e.home, `"`+e.outside+`"`)
				mkdirs(t, filepath.Join(e.home, "Download"))
			},
			want: func(e env) string { return filepath.Join(e.home, "Download") },
		},
		{
			name: "~/Downloads symlinked to $HOME is refused",
			setup: func(t *testing.T, e env) {
				if err := os.Symlink(e.home, filepath.Join(e.home, "Downloads")); err != nil {
					t.Fatal(err)
				}
				mkdirs(t, filepath.Join(e.home, "Download"))
			},
			want: func(e env) string { return filepath.Join(e.home, "Download") },
		},
		{
			name: "~/Downloads symlinked to a folder inside $HOME resolves",
			setup: func(t *testing.T, e env) {
				mkdirs(t, filepath.Join(e.home, "data", "dl"))
				if err := os.Symlink(filepath.Join(e.home, "data", "dl"), filepath.Join(e.home, "Downloads")); err != nil {
					t.Fatal(err)
				}
			},
			want: func(e env) string { return filepath.Join(e.home, "data", "dl") },
		},
		{
			name: "~/Downloads as a plain file is skipped",
			setup: func(t *testing.T, e env) {
				if err := os.WriteFile(filepath.Join(e.home, "Downloads"), nil, 0o644); err != nil {
					t.Fatal(err)
				}
				mkdirs(t, filepath.Join(e.home, "Download"))
			},
			want: func(e env) string { return filepath.Join(e.home, "Download") },
		},
		{
			name: "env beats XDG and ~/Downloads",
			setup: func(t *testing.T, e env) {
				mkdirs(t, filepath.Join(e.home, "mine"), filepath.Join(e.home, "Downloads"))
				writeUserDirs(t, e.home, `"$HOME/Downloads"`)
				t.Setenv(downloadsEnv, filepath.Join(e.home, "mine"))
			},
			want: func(e env) string { return filepath.Join(e.home, "mine") },
		},
		{
			name: "env outside $HOME is skipped",
			setup: func(t *testing.T, e env) {
				t.Setenv(downloadsEnv, e.outside)
				mkdirs(t, filepath.Join(e.home, "Downloads"))
			},
			want: func(e env) string { return filepath.Join(e.home, "Downloads") },
		},
		{
			name: "env of / or an ancestor of $HOME is skipped",
			setup: func(t *testing.T, e env) {
				t.Setenv(downloadsEnv, filepath.Dir(e.home))
				writeUserDirs(t, e.home, `"/"`)
			},
			wantErr: "No downloads folder found",
		},
		{
			name: "env equal to $HOME is skipped",
			setup: func(t *testing.T, e env) {
				t.Setenv(downloadsEnv, e.home+"/")
			},
			wantErr: "No downloads folder found",
		},
		{
			name: "flag outside $HOME is allowed",
			setup: func(t *testing.T, e env) {
				mkdirs(t, filepath.Join(e.home, "Downloads"))
				downloadsFlag = e.outside
			},
			want: func(e env) string { return e.outside },
		},
		{
			name: "flag beats env",
			setup: func(t *testing.T, e env) {
				mkdirs(t, filepath.Join(e.home, "a"), filepath.Join(e.home, "b"))
				t.Setenv(downloadsEnv, filepath.Join(e.home, "a"))
				downloadsFlag = filepath.Join(e.home, "b")
			},
			want: func(e env) string { return filepath.Join(e.home, "b") },
		},
		{
			name: "flag naming $HOME is refused, not skipped",
			setup: func(t *testing.T, e env) {
				mkdirs(t, filepath.Join(e.home, "Downloads"))
				downloadsFlag = e.home
			},
			wantErr: "contains your home folder",
		},
		{
			name: "flag naming / is refused",
			setup: func(t *testing.T, e env) {
				downloadsFlag = "/"
			},
			wantErr: "root folder",
		},
		{
			name: "flag naming a missing folder is refused, not skipped",
			setup: func(t *testing.T, e env) {
				mkdirs(t, filepath.Join(e.home, "Downloads"))
				downloadsFlag = filepath.Join(e.home, "nope")
			},
			wantErr: "no such folder",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := env{home: isolateDownloads(t), outside: t.TempDir()}
			tc.setup(t, e)

			got, err := resolveDownloadsDir()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("resolveDownloadsDir() = %q, %v; want error containing %q", got, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveDownloadsDir() error: %v", err)
			}
			want, err := filepath.EvalSymlinks(tc.want(e))
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("resolveDownloadsDir() = %q, want %q", got, want)
			}
		})
	}
}

// TestResolveDownloadsDirSymlinkedHome: when $HOME itself is a symlink (e.g.
// /home → /var/home), folders inside its real location still count as inside
// $HOME, and the real location itself is still refused.
func TestResolveDownloadsDirSymlinkedHome(t *testing.T) {
	isolateDownloads(t)
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", link)
	t.Setenv("XDG_CONFIG_HOME", "")
	mkdirs(t, filepath.Join(real, "Download"))

	got, err := resolveDownloadsDir()
	want, _ := filepath.EvalSymlinks(filepath.Join(real, "Download"))
	if err != nil || got != want {
		t.Fatalf("resolveDownloadsDir() = %q, %v; want %q", got, err, want)
	}

	t.Setenv(downloadsEnv, real)
	if got, _ := resolveDownloadsDir(); got != want {
		t.Fatalf("env naming the real $HOME was used: got %q, want fallback %q", got, want)
	}
}

// TestDiscoverDownloadsNamesFolder: the notice names the folder it looked in
// (as ~/…), and with no folder at all it says how to set one.
func TestDiscoverDownloadsNamesFolder(t *testing.T) {
	home := isolateDownloads(t)

	msg := discoverDownloads()().(discoveryMsg)
	if !strings.Contains(msg.notice, "No downloads folder found (set --downloads or ARCHMAID_DOWNLOADS)") {
		t.Fatalf("notice = %q, want the no-folder notice", msg.notice)
	}

	mkdirs(t, filepath.Join(home, "Download"))
	msg = discoverDownloads()().(discoveryMsg)
	if !strings.Contains(msg.notice, "~/Download") {
		t.Fatalf("notice = %q, want it to name ~/Download", msg.notice)
	}
}

// TestParseArgsDownloads: --downloads takes its folder as the next argument
// or after "=", and refuses to be given none.
func TestParseArgsDownloads(t *testing.T) {
	orig, origDry := downloadsFlag, dryRun
	defer func() { downloadsFlag, dryRun = orig, origDry }()

	for _, tc := range []struct {
		args    []string
		want    string
		wantErr bool
	}{
		{args: []string{"--downloads", "/data/dl"}, want: "/data/dl"},
		{args: []string{"--dryrun", "--downloads=/data/dl"}, want: "/data/dl"},
		{args: []string{"--downloads"}, wantErr: true},
		{args: []string{"--downloads="}, wantErr: true},
		{args: []string{"--dryrun"}, want: ""},
	} {
		downloadsFlag = ""
		err := parseArgs(tc.args)
		if (err != nil) != tc.wantErr || (!tc.wantErr && downloadsFlag != tc.want) {
			t.Fatalf("parseArgs(%q): flag=%q err=%v; want flag=%q err=%v",
				tc.args, downloadsFlag, err, tc.want, tc.wantErr)
		}
	}
}
