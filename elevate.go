package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

// This file is RESONANCE's side of the privilege boundary. Nothing here runs
// as root; it decides what to ask the helper for, and the helper — a separate
// binary launched through pkexec — decides whether to do it.
//
// The boundary exists for one reason. /etc and /usr became valid sources in
// v1.3.0, and backing them up needs nothing at all: reading a world-readable
// file and writing into your own vault are both things you can already do.
// Putting one back is the only operation in the program that a user cannot
// perform as themselves, so it is the only operation that crosses this line.
//
// The helper is launched once and spoken to until the app exits, rather than
// launched per file. With a folder of forty tracked files the alternative is
// forty password prompts, which is not an app anyone would use. The cost is a
// root process that lives as long as RESONANCE does, and the answer to that
// cost is a helper small enough to read in one sitting: see
// cmd/resonance-helper/main.go, which never reads a path, proves every path a
// component at a time from a compiled-in root, and dies when this process's
// pipe closes.
//
// Note what does *not* cross the boundary: bytes are read here, as you, from
// a vault you can already read, and sent to the helper. The helper has no
// operation that reads a file. A privileged process that cannot be asked to
// open a caller-named file for reading cannot be turned into a way to read
// /etc/shadow.

// helperPath is where the package puts the helper, and the same path the
// polkit policy authorises. polkit authorises a *path*, so this and the
// policy's org.freedesktop.policykit.exec.path must agree exactly — and a
// helper built into build/bin/ during development is not this path and cannot
// be elevated at all.
//
// A variable rather than a constant for one reason: tests point it at
// somewhere that certainly does not exist, so that `go test` on a machine
// where RESONANCE happens to be installed cannot raise a real password
// prompt. Nothing at runtime reassigns it, and it is not the authority in any
// case — the policy file is, and it names the path above literally.
var helperPath = "/usr/lib/resonance/resonance-helper"

// errNeedsAdmin is returned wherever a write outside $HOME is asked for and
// no helper session can be had.
var errNeedsAdmin = errors.New(
	"this file lives in a folder that belongs to root, so putting it back needs administrator rights")

// errNoHelperInstalled is the honest sentence for a development build. polkit
// will not authorise a binary sitting in build/bin/, so this is not a fault to
// work around — it is the design working, and saying so beats failing
// obscurely at a prompt that never appears.
var errNoHelperInstalled = errors.New(
	"this build can't ask for administrator rights — RESONANCE needs to be installed from its package for that")

// errAdminDeclined separates "you said no" from "something broke", because
// they deserve different sentences and only one of them is a problem.
var errAdminDeclined = errors.New(
	"administrator rights weren't given, so nothing outside your home folder was changed")

// --- the protocol, mirrored ---------------------------------------------
//
// These types are a deliberate copy of the helper's. They are not shared
// through a package because the helper is meant to be auditable on its own,
// without following an import into code that runs unprivileged. Two dozen
// lines duplicated is the price of that, and it is worth paying.

type helperRequest struct {
	Op        string `json:"op"`
	VaultRoot string `json:"vaultRoot,omitempty"`
	Path      string `json:"path,omitempty"`
	To        string `json:"to,omitempty"`
	Link      string `json:"link,omitempty"`
	Data      []byte `json:"data,omitempty"`
	Mode      uint32 `json:"mode,omitempty"`
}

type helperResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// --- the session --------------------------------------------------------

type helperSession struct {
	mu        sync.Mutex
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	enc       *json.Encoder
	dec       *json.Decoder
	stderr    *bytes.Buffer
	vaultRoot string
}

var (
	helperMu      sync.Mutex
	helperCurrent *helperSession
)

// elevatedSession returns a live helper, starting one — and prompting for a
// password — if there isn't one yet.
//
// vaultRoot is "" for the ordinary case of restoring into /etc or /usr from a
// vault you own, and is only set when the vault itself belongs to root. The
// helper is given the least it needs: a session opened without a vault root
// can write to /etc and /usr and nowhere else. If a later operation needs a
// vault the running session wasn't given, the session is replaced, which
// costs one more prompt in a case that is rare by construction.
func elevatedSession(vaultRoot string) (*helperSession, error) {
	helperMu.Lock()
	defer helperMu.Unlock()

	if helperCurrent != nil {
		if helperCurrent.alive() && (vaultRoot == "" || helperCurrent.vaultRoot == vaultRoot) {
			return helperCurrent, nil
		}
		helperCurrent.close()
		helperCurrent = nil
	}

	s, err := startHelper(vaultRoot)
	if err != nil {
		return nil, err
	}
	helperCurrent = s
	return s, nil
}

// closeHelperSession ends the root process early. Not required for
// correctness — the helper exits when this process does and its pipe closes —
// but a session that is no longer needed should not sit there being root.
func closeHelperSession() {
	helperMu.Lock()
	defer helperMu.Unlock()
	if helperCurrent != nil {
		helperCurrent.close()
		helperCurrent = nil
	}
}

// helperInstalled reports whether there is a helper to elevate at all. The
// checks mirror pkexec's own, so the refusal comes from here with a sentence
// worth reading rather than from pkexec's stderr.
func helperInstalled() error {
	if _, err := exec.LookPath("pkexec"); err != nil {
		return errNoHelperInstalled
	}
	info, err := os.Stat(helperPath)
	if err != nil || !info.Mode().IsRegular() {
		return errNoHelperInstalled
	}
	// pkexec refuses a program that is not owned by root, or that group or
	// others can write to — it would be a way to become root by editing a
	// file. Checking here means the same refusal, explained.
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || info.Mode().Perm()&0022 != 0 {
		return errNoHelperInstalled
	}
	return nil
}

// helperCommand builds the process that runs the helper as root.
//
// A variable so tests can drive the locally built helper directly and
// unprivileged: `go test` cannot answer a polkit dialog, but the protocol,
// the path proving and every refusal are the same binary either way, so
// pointing this at build/bin/resonance-helper exercises the real code and
// only skips becoming root.
var helperCommand = func() (*exec.Cmd, error) {
	if err := helperInstalled(); err != nil {
		return nil, err
	}
	return exec.Command("pkexec", helperPath), nil
}

func startHelper(vaultRoot string) (*helperSession, error) {
	cmd, err := helperCommand()
	if err != nil {
		return nil, err
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, errNoHelperInstalled
	}

	s := &helperSession{
		cmd:       cmd,
		stdin:     stdin,
		enc:       json.NewEncoder(stdin),
		dec:       json.NewDecoder(stdout),
		stderr:    &stderr,
		vaultRoot: vaultRoot,
	}

	// The password prompt happens here, inside this first exchange: pkexec
	// does not return until the dialog is answered, so the hello is what
	// blocks and what reports being refused.
	if err := s.send(helperRequest{Op: "hello", VaultRoot: vaultRoot}); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

func (s *helperSession) alive() bool {
	return s.cmd != nil && s.cmd.ProcessState == nil
}

func (s *helperSession) close() {
	if s.stdin != nil {
		s.stdin.Close() // the helper exits when its input closes
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Wait()
	}
}

// send performs one operation and waits for its answer. Requests are
// serialised: the protocol is one reply per request in order, and two
// restores running at once would otherwise read each other's answers.
func (s *helperSession) send(req helperRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.enc.Encode(req); err != nil {
		return s.diagnose(err)
	}
	var resp helperResponse
	if err := s.dec.Decode(&resp); err != nil {
		return s.diagnose(err)
	}
	if !resp.OK {
		if resp.Error == "" {
			return errNeedsAdmin
		}
		return errors.New(resp.Error)
	}
	return nil
}

// diagnose turns a broken pipe into the reason the pipe broke. A helper that
// went away almost always went away because pkexec never started it, and the
// difference between "you dismissed the dialog" and "something is wrong"
// matters enough to be worth the exit code lookup.
func (s *helperSession) diagnose(cause error) error {
	if s.cmd == nil || s.cmd.Process == nil {
		return cause
	}
	_ = s.cmd.Wait()
	code := s.cmd.ProcessState.ExitCode()

	switch code {
	case 126:
		// pkexec: the authentication dialog was dismissed.
		return errAdminDeclined
	case 127:
		// pkexec: not authorised, or the program could not be run.
		return errNoHelperInstalled
	}

	if msg := bytes.TrimSpace(s.stderr.Bytes()); len(msg) > 0 {
		return fmt.Errorf("the administrator helper stopped: %s", msg)
	}
	return fmt.Errorf("the administrator helper stopped unexpectedly: %w", cause)
}

// --- what the rest of the program asks for ------------------------------

// restoreSystemFile writes one vault copy back to a destination under /etc or
// /usr. The unlink-then-write happens inside the helper rather than here: at
// this end the app cannot unlink a planted symlink in /etc anyway, and
// checking a path here and writing it a moment later at root is exactly the
// race the helper's own component-by-component descent exists to avoid.
//
// The bytes are read here, unprivileged, from a vault copy whose symlink
// check has already run in writeRestoredFile. Whole-file rather than
// streamed: these are configuration files, and a single message that either
// lands or doesn't is a better shape for a privileged write than a stream
// that can stop halfway.
func restoreSystemFile(vaultFile, destPath string) error {
	data, err := os.ReadFile(vaultFile)
	if err != nil {
		return err
	}
	info, err := os.Stat(vaultFile)
	if err != nil {
		return err
	}
	s, err := elevatedSession("")
	if err != nil {
		return err
	}
	return s.send(helperRequest{
		Op:   "write",
		Path: destPath,
		Data: data,
		Mode: uint32(info.Mode().Perm()),
	})
}
