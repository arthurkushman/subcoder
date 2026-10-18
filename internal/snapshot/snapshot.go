package snapshot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Snapshot identifies a point-in-time state of the project work tree.
type Snapshot struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
}

// Manager keeps a shadow git repository per project and creates commits of
// the work tree without touching the user's own repository.
type Manager struct {
	workTree string
	gitDir   string
	mu       sync.Mutex
}

// New creates a snapshot manager for workTree, storing its shadow repository
// inside dataDir. It fails when git is not available.
func New(workTree, dataDir string) (*Manager, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("snapshot: git is required: %w", err)
	}
	abs, err := filepath.Abs(workTree)
	if err != nil {
		return nil, err
	}
	absData, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(abs))
	gitDir := filepath.Join(absData, "snapshots", hex.EncodeToString(hash[:8]))
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		return nil, err
	}
	m := &Manager{workTree: abs, gitDir: gitDir}
	if _, err := os.Stat(filepath.Join(gitDir, "HEAD")); err != nil {
		if err := m.init(); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *Manager) init() error {
	cmd := exec.Command("git", "init", "--bare", "--quiet", m.gitDir)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("snapshot: git init: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (m *Manager) git(ctx context.Context, args ...string) (string, error) {
	base := []string{
		"--git-dir", m.gitDir,
		"--work-tree", m.workTree,
		"-c", "user.name=SubCoder",
		"-c", "user.email=subcoder@localhost",
		"-c", "commit.gpgsign=false",
		"-c", "core.hooksPath=" + os.DevNull,
	}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	cmd.Dir = m.workTree
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("snapshot: git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

// Create commits the current state of the work tree and returns the snapshot.
func (m *Manager) Create(ctx context.Context) (*Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.git(ctx, "add", "-A", "."); err != nil {
		return nil, err
	}
	message := "snapshot " + time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := m.git(ctx, "commit", "--allow-empty", "--no-verify", "--quiet", "-m", message); err != nil {
		return nil, err
	}
	id, err := m.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	return &Snapshot{ID: id, CreatedAt: time.Now().UTC()}, nil
}

// Restore brings the work tree back to the given snapshot. Files created
// after the snapshot are removed, modified files are restored, and deleted
// files are recreated. Git-ignored files are left untouched.
func (m *Manager) Restore(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("snapshot: empty id")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.git(ctx, "cat-file", "-e", id+"^{commit}"); err != nil {
		return fmt.Errorf("snapshot: unknown snapshot %s", id)
	}
	if _, err := m.git(ctx, "add", "-A", "."); err != nil {
		return err
	}
	if _, err := m.git(ctx, "reset", "--hard", "--quiet", id); err != nil {
		return err
	}
	return nil
}

// Exists reports whether the snapshot commit is present in the shadow repo.
func (m *Manager) Exists(ctx context.Context, id string) bool {
	if id == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, err := m.git(ctx, "cat-file", "-e", id+"^{commit}")
	return err == nil
}

// ShortID returns a compact form of a snapshot id for display.
func ShortID(id string) string {
	if len(id) > 7 {
		return id[:7]
	}
	return id
}
