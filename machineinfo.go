package main

import (
	"bufio"
	"os"
	"os/user"
	"strings"
	"syscall"
)

// MachineInfo describes the machine that most recently wrote into a vault —
// stamped by AddApp and UpdateFromSource, the two "write into the vault"
// actions. A vault carries one current snapshot, not a history: the second
// machine to touch a shared vault simply overwrites the first's info.
type MachineInfo struct {
	Kernel   string `json:"kernel"`
	OS       string `json:"os"`
	Hostname string `json:"hostname"`
	Username string `json:"username"`
}

// captureMachineInfo is best-effort per field — a card with three known
// fields is more useful than no card at all, so one field failing to
// resolve never blocks the others.
func captureMachineInfo() MachineInfo {
	return MachineInfo{
		Kernel:   kernelVersion(),
		OS:       osPrettyName(),
		Hostname: hostname(),
		Username: username(),
	}
}

func kernelVersion() string {
	var uts syscall.Utsname
	if err := syscall.Uname(&uts); err != nil {
		return ""
	}
	sysname := utsnameToString(uts.Sysname[:])
	release := utsnameToString(uts.Release[:])
	if sysname == "" {
		return release
	}
	if release == "" {
		return sysname
	}
	return sysname + " " + release
}

// utsnameToString converts a Utsname byte-array field to a string. The
// array element type is int8 on some architectures and uint8 on others, so
// this takes a generic slice and does the widening itself rather than
// assuming either.
func utsnameToString[T int8 | uint8](field []T) string {
	b := make([]byte, 0, len(field))
	for _, c := range field {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	return string(b)
}

// osPrettyName reads /etc/os-release's PRETTY_NAME. Falls back to "" (not
// an error) if the file is missing or the key isn't present — some minimal
// distros don't ship it.
func osPrettyName() string {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "PRETTY_NAME=") {
			continue
		}
		value := strings.TrimPrefix(line, "PRETTY_NAME=")
		return strings.Trim(value, `"`)
	}
	return ""
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

func username() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.Username
}

// GetMachineInfo returns the vault's currently stored machine info — a
// vault last touched by pre-STEP4 code has none, which the frontend renders
// as an "unknown" state rather than an error.
func (a *App) GetMachineInfo() (MachineInfo, error) {
	settings := a.GetSettings()
	if settings.VaultPath == "" {
		return MachineInfo{}, nil
	}
	m, err := loadManifest(settings.VaultPath)
	if err != nil {
		return MachineInfo{}, err
	}
	return m.MachineInfo, nil
}

// stampMachineInfo records the current machine as the vault's most recent
// writer. Called by AddApp and UpdateFromSource after a successful write,
// before the manifest is saved.
func stampMachineInfo(m *Manifest) {
	m.MachineInfo = captureMachineInfo()
}
