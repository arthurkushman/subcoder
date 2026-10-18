package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Store struct {
	dir string
}

type Summary struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	Directory string `json:"directory,omitempty"`
	ParentID  string `json:"parentID,omitempty"`
	Model     string `json:"model,omitempty"`
	Agent     string `json:"agent,omitempty"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

func NewStore(dir string) *Store {
	return &Store{dir: filepath.Join(dir, "sessions")}
}

func (s *Store) Dir() string {
	return s.dir
}

func (s *Store) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

func (s *Store) Save(sess *Session) error {
	if sess.ID == "" {
		return errors.New("session: missing id")
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	path := s.path(sess.ID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) Load(id string) (*Session, error) {
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, err
	}
	sess := &Session{}
	if err := json.Unmarshal(data, sess); err != nil {
		return nil, err
	}
	return sess, nil
}

func (s *Store) Delete(id string) error {
	err := os.Remove(s.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Store) List() ([]Summary, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	summaries := make([]Summary, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			continue
		}
		var sess Session
		if err := json.Unmarshal(data, &sess); err != nil {
			continue
		}
		summaries = append(summaries, Summary{
			ID:        sess.ID,
			Title:     sess.Title,
			Directory: sess.Directory,
			ParentID:  sess.ParentID,
			Model:     sess.Model,
			Agent:     sess.Agent,
			CreatedAt: sess.CreatedAt.Format(time.RFC3339Nano),
			UpdatedAt: sess.UpdatedAt.Format(time.RFC3339Nano),
		})
	}

	sort.SliceStable(summaries, func(i, j int) bool {
		left, leftErr := time.Parse(time.RFC3339Nano, summaries[i].UpdatedAt)
		right, rightErr := time.Parse(time.RFC3339Nano, summaries[j].UpdatedAt)
		if leftErr == nil && rightErr == nil {
			return left.After(right)
		}
		return summaries[i].UpdatedAt > summaries[j].UpdatedAt
	})
	return summaries, nil
}

// Latest returns the most recently updated session for the given directory.
func (s *Store) Latest(directory string) (*Session, error) {
	summaries, err := s.List()
	if err != nil {
		return nil, err
	}
	for _, summary := range summaries {
		if directory == "" || summary.Directory == directory {
			return s.Load(summary.ID)
		}
	}
	return nil, os.ErrNotExist
}
