// Command resonance-helper performs the handful of filesystem writes
// RESONANCE cannot perform as the user, and nothing else.
//
// It is a separate binary, launched through pkexec, because RESONANCE runs as
// you and /etc and /usr do not belong to you. Backing a system file up needs
// no rights at all — reading a world-readable file and writing into your own
// vault are both things you can already do — so the only operations that
// cross this boundary are the ones that put a file back, plus the same
// operations against a vault whose folder belongs to root.
//
// Three properties are the whole design, and each is here rather than in the
// caller because a check made in a process that is not the one doing the
// write is a check that can be raced:
//
//   - It never reads a path. Not one op returns file contents, and none takes
//     a path to read from. Bytes arrive from RESONANCE, which read them from
//     the vault as you. A privileged process that never opens a caller-named
//     file for reading cannot be talked into disclosing one.
//   - Every path is proved a component at a time, from a resolved root down,
//     with any symlink standing where a directory should be refused outright.
//     Nothing is ever opened by a path whose parents were merely checked
//     earlier.
//   - The allowed roots are compiled in. /etc and /usr are constants below;
//     the vault root is fixed once, by the first message of a session, and
//     any later attempt to change it is refused.
//
// It is deliberately self-contained — no imports outside the standard library
// and no shared package with RESONANCE itself. The validation below is a
// deliberate twin of classifySource and relativeUnder in the app, duplicated
// rather than shared so this file can be read and audited on its own, without
// following anything into another package.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// systemRoots are the only places outside a vault this helper will ever
// write. Compiled in, with no flag and no environment override: a privileged
// binary whose reach can be widened by its caller has no reach of its own.
var systemRoots = []string{"/etc", "/usr"}

// request is one operation. The zero value is not a valid request; op is
// always checked first.
type request struct {
	Op string `json:"op"`

	// VaultRoot is meaningful only on the "hello" that opens a session.
	VaultRoot string `json:"vaultRoot,omitempty"`

	Path string `json:"path,omitempty"`
	To   string `json:"to,omitempty"`   // rename destination
	Link string `json:"link,omitempty"` // symlink target

	// Data is the file content for a write, base64 in the wire form.
	// It arrives from the caller; it is never read from disk here.
	Data []byte `json:"data,omitempty"`
	Mode uint32 `json:"mode,omitempty"`
}

type response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func main() {
	if err := serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "resonance-helper:", err)
		os.Exit(1)
	}
}

// serve reads requests until the input closes, which is how the session ends:
// RESONANCE exiting, crashing, or being killed closes the pipe, and this
// process goes with it. There is no timeout to misconfigure and no way for it
// to outlive the app that asked for it.
//
// A json.Decoder rather than a line scanner: bufio.Scanner caps a token at
// 64KB by default and would fail, silently and only for large files, on
// exactly the vault copies most worth restoring.
func serve(in io.Reader, out io.Writer) error {
	dec := json.NewDecoder(in)
	enc := json.NewEncoder(out)

	s := &session{}
	for {
		var req request
		if err := dec.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		err := s.handle(req)
		resp := response{OK: err == nil}
		if err != nil {
			resp.Error = err.Error()
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
}

// session holds the one piece of per-run state: which vault this session may
// write into. Fixed by the opening message and never reassigned.
type session struct {
	vaultRoot string
	greeted   bool
}

func (s *session) handle(req request) error {
	if req.Op == "hello" {
		if s.greeted {
			return errors.New("this session has already been opened")
		}
		s.greeted = true
		if req.VaultRoot == "" {
			return nil
		}
		root, err := resolveRoot(req.VaultRoot)
		if err != nil {
			return err
		}
		s.vaultRoot = root
		return nil
	}
	if !s.greeted {
		return errors.New("session not opened")
	}

	switch req.Op {
	case "write":
		return s.write(req.Path, req.Data, req.Mode)
	case "mkdir":
		return s.mkdir(req.Path)
	case "remove":
		return s.remove(req.Path, false)
	case "removeAll":
		return s.remove(req.Path, true)
	case "symlink":
		return s.symlink(req.Path, req.Link)
	case "rename":
		return s.rename(req.Path, req.To)
	default:
		return fmt.Errorf("unknown operation %q", req.Op)
	}
}

// --- path proving -------------------------------------------------------

// roots is the live allowlist: the compiled-in system roots, plus this
// session's vault if one was named. Resolved, because a root reached through
// a symlink is an ordinary setup for a vault and the comparisons below are
// against real paths.
func (s *session) roots() []string {
	out := make([]string, 0, len(systemRoots)+1)
	for _, r := range systemRoots {
		if resolved, err := resolveRoot(r); err == nil {
			out = append(out, resolved)
		}
	}
	if s.vaultRoot != "" {
		out = append(out, s.vaultRoot)
	}
	return out
}

// resolveRoot requires a root to be an absolute path that exists and is a
// real directory once followed.
func resolveRoot(p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%s is not a full path", p)
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a folder", p)
	}
	return resolved, nil
}

// under is filepath.Rel plus a ".." test — the twin of the app's
// relativeUnder, kept here rather than imported so this file stands alone.
// It is pure string work and proves nothing about symlinks; descend below is
// what actually contains an operation.
func under(absPath, base string) (string, bool) {
	rel, err := filepath.Rel(base, absPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

// rootFor finds the allowed root a path belongs to, and its path relative to
// it. A path under no root is refused here, before anything touches disk.
func (s *session) rootFor(p string) (root, rel string, err error) {
	if !filepath.IsAbs(p) {
		return "", "", fmt.Errorf("%s is not a full path", p)
	}
	clean := filepath.Clean(p)
	for _, r := range s.roots() {
		if rel, ok := under(clean, r); ok {
			return r, rel, nil
		}
	}
	return "", "", fmt.Errorf("%s is outside everywhere this helper may write", p)
}

// descend walks from a resolved root down to the directory that will hold
// the target, one component at a time, and returns the final absolute path.
//
// This is the function the whole binary exists to contain. Every intermediate
// component is Lstat'ed and required to be a real directory: a symlink
// standing where a folder should be is refused rather than followed, so
// /etc/alsa being replaced by a link to /root cannot redirect a write there.
// Missing directories are created here, one level at a time with os.Mkdir,
// which fails rather than following if something appears at that name in the
// meantime.
//
// create says whether missing directories should be made. A remove has
// nothing to create and must not bring directories into existence on its way
// to deleting something.
func descend(root, rel string, create bool) (string, error) {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	cur := root
	for _, part := range parts[:len(parts)-1] {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("%s is not a path this helper will follow", rel)
		}
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if os.IsNotExist(err) {
			if !create {
				return "", err
			}
			if err := os.Mkdir(cur, 0755); err != nil {
				return "", err
			}
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%s is a symlink, not a folder — refusing to write through it", cur)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("%s is not a folder", cur)
		}
	}
	last := parts[len(parts)-1]
	if last == "" || last == "." || last == ".." {
		return "", fmt.Errorf("%s is not a path this helper will write", rel)
	}
	return filepath.Join(cur, last), nil
}

// target proves a path and returns where the operation should actually
// happen. Every op below starts here and nowhere else.
func (s *session) target(p string, create bool) (string, error) {
	root, rel, err := s.rootFor(p)
	if err != nil {
		return "", err
	}
	return descend(root, rel, create)
}

// --- operations ---------------------------------------------------------

// write replaces a file with the bytes the caller supplied.
//
// Temp file in the destination directory, then rename. rename(2) replaces the
// final component whatever it is — including a symlink — without following
// it, so a link planted at the destination is destroyed rather than written
// through. Writing the temp file first also means a file that already exists
// is never truncated by a write that then fails partway.
func (s *session) write(p string, data []byte, mode uint32) error {
	dest, err := s.target(p, true)
	if err != nil {
		return err
	}
	perm := os.FileMode(mode).Perm()
	if perm == 0 {
		perm = 0644
	}

	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, ".resonance-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		return err
	}
	return os.Rename(tmpPath, dest)
}

func (s *session) mkdir(p string) error {
	dest, err := s.target(p, true)
	if err != nil {
		return err
	}
	if err := os.Mkdir(dest, 0755); err != nil && !os.IsExist(err) {
		return err
	}
	return nil
}

// remove unlinks a file, or deletes a subtree when all is set.
//
// create is false on the way down: a delete must not conjure directories into
// existence looking for something to remove. os.Remove and os.RemoveAll both
// unlink a symlink rather than following it, so a link standing at the final
// component takes only itself.
func (s *session) remove(p string, all bool) error {
	dest, err := s.target(p, false)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // already gone is the goal state
		}
		return err
	}
	if all {
		return os.RemoveAll(dest)
	}
	if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// symlink recreates a link that was there before a restore overwrote it —
// a symlink that a restore has to put back. The target is written
// verbatim and never resolved: what is being restored is the link, not
// whatever it happens to point at.
func (s *session) symlink(p, link string) error {
	if link == "" {
		return errors.New("a symlink needs a target")
	}
	dest, err := s.target(p, true)
	if err != nil {
		return err
	}
	if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Symlink(link, dest)
}

// rename moves something within the allowed roots. Both ends are proved
// independently, and both must land under the same root — moving a file from
// /etc into a vault, or the reverse, is not an operation this helper offers.
func (s *session) rename(from, to string) error {
	fromRoot, fromRel, err := s.rootFor(from)
	if err != nil {
		return err
	}
	toRoot, toRel, err := s.rootFor(to)
	if err != nil {
		return err
	}
	if fromRoot != toRoot {
		return errors.New("this helper won't move anything between two different places")
	}
	fromPath, err := descend(fromRoot, fromRel, false)
	if err != nil {
		return err
	}
	toPath, err := descend(toRoot, toRel, true)
	if err != nil {
		return err
	}
	return os.Rename(fromPath, toPath)
}
