package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/rymdport/portal/filechooser"
)

// uriToPath converts a portal-returned file:// URI into a plain filesystem
// path. url.Parse already percent-decodes the result.
func uriToPath(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("unsupported URI scheme: %s", u.Scheme)
	}
	return u.Path, nil
}

// ChooseVaultPath opens the portal folder picker. Returns "" (not an error)
// if the user cancels.
func (a *App) ChooseVaultPath() (string, error) {
	uris, err := filechooser.OpenFile("", "Choose vault folder", &filechooser.OpenFileOptions{
		Directory: true,
	})
	if err != nil {
		return "", err
	}
	if len(uris) == 0 {
		return "", nil
	}
	return uriToPath(uris[0])
}

// PickFiles opens the portal multi-select file picker. Returns an empty
// slice (not an error) if the user cancels.
func (a *App) PickFiles() ([]string, error) {
	uris, err := filechooser.OpenFile("", "Choose files to back up", &filechooser.OpenFileOptions{
		Multiple: true,
	})
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(uris))
	for _, u := range uris {
		if p, err := uriToPath(u); err == nil {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// AddApp validates every picked path before copying any of them — a
// validation failure partway through must never leave some files already
// copied onto disk with no manifest entry pointing at them.
func (a *App) AddApp(name string, absPaths []string) error {
	settings := a.GetSettings()
	if settings.VaultPath == "" {
		return errors.New("no vault path set")
	}
	if err := validAppName(name); err != nil {
		return err
	}
	if len(absPaths) == 0 {
		return errors.New("choose at least one file")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	m, err := loadManifest(settings.VaultPath)
	if err != nil {
		return err
	}
	for _, app := range m.Apps {
		if strings.EqualFold(app.Name, name) {
			return fmt.Errorf("an app named %q already exists", name)
		}
	}

	// Validate everything before copying anything.
	seen := make(map[string]bool, len(absPaths))
	files := make([]ManifestFile, 0, len(absPaths))
	sources := make([]string, 0, len(absPaths))
	for _, abs := range absPaths {
		if seen[abs] {
			continue
		}
		seen[abs] = true

		info, err := os.Stat(abs)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", abs)
		}
		rel, err := homeRelative(abs, home)
		if err != nil {
			return fmt.Errorf("%s is outside your home folder", abs)
		}
		files = append(files, ManifestFile{Path: rel})
		sources = append(sources, abs)
	}

	appDir := filepath.Join(settings.VaultPath, name)
	for i, f := range files {
		dst := filepath.Join(appDir, filepath.FromSlash(f.Path))
		if err := copyFile(sources[i], dst); err != nil {
			return err
		}
		size, checksum, backedUpAt, err := vaultFileMeta(dst)
		if err != nil {
			return err
		}
		files[i].Size = size
		files[i].Checksum = checksum
		files[i].BackedUpAt = backedUpAt
	}

	m.Apps = append(m.Apps, ManifestApp{Name: name, Files: files})
	stampMachineInfo(&m)
	if err := saveManifest(settings.VaultPath, m); err != nil {
		return err
	}
	recordActivity("add", name, summarizeAddActivity(name, len(files)))
	return nil
}

// summarizeAddActivity builds AddApp's activity-log summary from the file
// count already in scope at its call site — AddApp has no result struct to
// draw from, unlike Update/Restore/Undo.
func summarizeAddActivity(name string, fileCount int) string {
	return fmt.Sprintf("Added %s (%d files)", name, fileCount)
}
