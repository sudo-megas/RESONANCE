package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestClassifySource_NearMissesAreRefused is the whole point of having a
// classifier rather than a strings.HasPrefix. /etc and /etcpasswd share a
// prefix and nothing else, and a prefix test would hand the second one to the
// backup routine as a system file. relativeUnder is what makes the difference
// — filepath.Rel("/etc", "/etcpasswd") is "../etcpasswd", which fails the
// ".." test — so the near misses are worth pinning against a later
// "simplification" back to a prefix check.
func TestClassifySource_NearMissesAreRefused(t *testing.T) {
	home := "/home/megas"

	refused := []string{
		"/etcpasswd",          // shares a prefix with /etc, is not under it
		"/usrlocal",           // same, for /usr
		"/etc",                // the root itself, not a file under it
		"/usr",                // same
		"/etc/../root",        // climbs out once cleaned
		"/usr/../../etc",      // climbs past the filesystem root and back
		"/var/log/pacman",     // a real path under no allowed root
		"/",                   // the filesystem root
		"/home/megas",         // $HOME itself is not a file in it
		"/home/other/.bashrc", // another user's home
		"/proc/self/environ",  // never a file to back up
	}
	for _, p := range refused {
		if scope, stored, err := classifySource(p, home); err == nil {
			t.Errorf("classifySource(%q) must refuse, got scope=%v stored=%q", p, scope, stored)
		}
	}
}

// TestClassifySource_AcceptsBothScopes proves the two scopes store genuinely
// different strings: home files stay $HOME-relative (CORE §4, the username
// caveat), system files stay absolute because they have no per-user form.
func TestClassifySource_AcceptsBothScopes(t *testing.T) {
	home := "/home/megas"

	cases := []struct {
		path   string
		scope  pathScope
		stored string
	}{
		{"/home/megas/.bashrc", scopeHome, ".bashrc"},
		{"/home/megas/.config/nvim/init.lua", scopeHome, ".config/nvim/init.lua"},
		{"/etc/alsa/alsa.conf", scopeSystem, "/etc/alsa/alsa.conf"},
		{"/etc/pacman.conf", scopeSystem, "/etc/pacman.conf"},
		{"/usr/share/applications/foo.desktop", scopeSystem, "/usr/share/applications/foo.desktop"},
		// Cleaned, not rejected: the path resolves to something legitimately
		// under an allowed root.
		{"/etc/alsa/../pacman.conf", scopeSystem, "/etc/pacman.conf"},
	}
	for _, c := range cases {
		scope, stored, err := classifySource(c.path, home)
		if err != nil {
			t.Errorf("classifySource(%q): unexpected refusal: %v", c.path, err)
			continue
		}
		if scope != c.scope {
			t.Errorf("classifySource(%q) scope = %v, want %v", c.path, scope, c.scope)
		}
		if stored != c.stored {
			t.Errorf("classifySource(%q) stored = %q, want %q", c.path, stored, c.stored)
		}
	}
}

// TestClassifySource_ReservedSegment covers the one refusal the .system vault
// layout costs. ~/.system/foo would be stored at vault/<app>/.system/foo,
// which is exactly where /etc and /usr files live — so it is refused rather
// than allowed to collide. The refusal must be a whole path segment: a file
// called ~/.systemd-units is not ~/.system/anything and must still work.
func TestClassifySource_ReservedSegment(t *testing.T) {
	home := "/home/megas"

	for _, p := range []string{"/home/megas/.system", "/home/megas/.system/etc/alsa.conf"} {
		_, _, err := classifySource(p, home)
		if err == nil {
			t.Errorf("classifySource(%q) must refuse the reserved segment", p)
			continue
		}
		if !strings.Contains(err.Error(), systemVaultSegment) {
			t.Errorf("classifySource(%q) refusal should name %s, said: %v", p, systemVaultSegment, err)
		}
	}

	// Same prefix, different segment — must not be caught by the refusal.
	for _, p := range []string{"/home/megas/.systemd-units", "/home/megas/.system-backup/x"} {
		if _, _, err := classifySource(p, home); err != nil {
			t.Errorf("classifySource(%q) must be allowed, got: %v", p, err)
		}
	}
}

// TestSystemVaultRel_StaysInsideTheReservedSubtree proves the mapping can't
// be walked back out of .system by a path that climbs, and that it lands
// where the layout says it does.
func TestSystemVaultRel_StaysInsideTheReservedSubtree(t *testing.T) {
	cases := map[string]string{
		"/etc/alsa/alsa.conf":                 ".system/etc/alsa/alsa.conf",
		"/usr/share/applications/foo.desktop": ".system/usr/share/applications/foo.desktop",
		"/etc/pacman.conf":                    ".system/etc/pacman.conf",
	}
	for in, want := range cases {
		if got := systemVaultRel(in); got != want {
			t.Errorf("systemVaultRel(%q) = %q, want %q", in, got, want)
		}
	}

	// Whatever it is handed, the result must stay under the reserved segment
	// — this is the guard that keeps a system file from being written over
	// another app's home-side backup.
	for _, p := range []string{"/etc/alsa/alsa.conf", "/etc/../etc/x", "/usr/./share/y"} {
		got := systemVaultRel(p)
		if _, err := relativeUnder(filepath.Join("/vault/app", got), "/vault/app/"+systemVaultSegment); err != nil {
			t.Errorf("systemVaultRel(%q) = %q escapes the reserved subtree", p, got)
		}
	}
}

// TestOutsideAllowedRoots_NamesEveryRoot keeps the refusal honest. A user
// told only "outside your home folder" after /etc became valid would have no
// way to learn that /etc is now allowed.
func TestOutsideAllowedRoots_NamesEveryRoot(t *testing.T) {
	msg := outsideAllowedRoots("/var/log/x").Error()
	for _, want := range []string{"/var/log/x", "home folder", "/etc", "/usr"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal should mention %q, said: %s", want, msg)
		}
	}
}
