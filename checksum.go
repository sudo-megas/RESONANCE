package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"time"
)

// fileChecksum hashes a file's contents. Streamed via io.Copy rather than
// read-into-memory, so an arbitrarily large dotfile (a fat .bash_history, a
// cache dump) doesn't blow up memory. Shared by drift computation (drift.go,
// comparing live source against a historical stored checksum) and vault
// migration verification (migration.go, comparing two current filesystem
// trees) — two distinct uses of the same primitive, never conflated.
func fileChecksum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// vaultFileMeta stats and hashes a vault-side file, producing exactly the
// three fields ManifestFile stores per backed-up file. The reported
// timestamp is the file's own modification time, not time.Now() — for a
// file copyFile just wrote, that's genuinely the moment it was written
// (copyFile never calls os.Chtimes), so this is recovered fact, not a
// fabricated value. Shared by AddApp (vault.go), UpdateFromSource, and
// backfillChecksums (drift.go) so all three record backup metadata the
// same way.
func vaultFileMeta(path string) (size int64, checksum string, backedUpAt string, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, "", "", err
	}
	sum, err := fileChecksum(path)
	if err != nil {
		return 0, "", "", err
	}
	return info.Size(), sum, info.ModTime().UTC().Format(time.RFC3339), nil
}
