// Package plugintrust persists approvals for Lua plugin content.
package plugintrust

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/bjarneo/cliamp/internal/fileutil"
)

const manifestName = ".trust.json"

var (
	ErrUntrusted    = errors.New("plugin is not trusted")
	ErrHashMismatch = errors.New("plugin content changed since approval")
)

type Manifest struct {
	Version int               `json:"version"`
	Plugins map[string]string `json:"plugins"`
}

// ManifestPath returns the path of the trust manifest in the plugin dir.
func ManifestPath(dir string) string {
	return filepath.Join(dir, manifestName)
}

func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open plugin: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash plugin: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Hash returns the hash of plugin content, in the form that HashFile returns.
func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func Load(dir string) (Manifest, error) {
	m := Manifest{Version: 1, Plugins: make(map[string]string)}
	data, err := os.ReadFile(ManifestPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return m, fmt.Errorf("read plugin trust manifest: %w", err)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("parse plugin trust manifest: %w", err)
	}
	if m.Version != 1 || m.Plugins == nil {
		return m, errors.New("unsupported plugin trust manifest")
	}
	return m, nil
}

func Save(dir string, m Manifest) error {
	m.Version = 1
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode plugin trust manifest: %w", err)
	}
	data = append(data, '\n')
	return fileutil.WriteFileAtomic(ManifestPath(dir), data, 0o600)
}

// Approve approves the current content of the plugin file at path.
func Approve(dir, name, path string) (string, error) {
	hash, err := HashFile(path)
	if err != nil {
		return "", err
	}
	if err := ApproveHash(dir, name, path, hash); err != nil {
		return "", err
	}
	return hash, nil
}

// ApproveHash approves the content with hash, the hash that the user saw. It
// fails with ErrHashMismatch when the file at path no longer has that hash.
// Thus a change to the file while the prompt waits is not approved.
func ApproveHash(dir, name, path, hash string) error {
	got, err := HashFile(path)
	if err != nil {
		return err
	}
	if got != hash {
		return ErrHashMismatch
	}
	m, err := Load(dir)
	if err != nil {
		return err
	}
	m.Plugins[name] = hash
	return Save(dir, m)
}

// Revoke removes the approval of name. It leaves the manifest as it is when
// the manifest has no approval for name.
func Revoke(dir, name string) error {
	m, err := Load(dir)
	if err != nil {
		return err
	}
	if _, ok := m.Plugins[name]; !ok {
		return nil
	}
	delete(m.Plugins, name)
	return Save(dir, m)
}

func Verify(m Manifest, name, path string) error {
	want, ok := m.Plugins[name]
	if !ok {
		return ErrUntrusted
	}
	got, err := HashFile(path)
	if err != nil {
		return err
	}
	if got != want {
		return ErrHashMismatch
	}
	return nil
}
