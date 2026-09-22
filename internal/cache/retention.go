package cache

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Prune deletes all but the newest keep scans and reports what it removed.
// The target of `latest` is never removed. keep == 0 empties the store.
func (s Store) Prune(keep int) ([]string, error) {
	if keep < 0 {
		return nil, fmt.Errorf("cache: keep must not be negative, got %d", keep)
	}
	entries, err := s.List()
	if err != nil {
		return nil, err
	}
	latest, _, _ := s.Latest()
	var removed []string
	for i, e := range entries {
		if i < keep || (latest != "" && e.Path == latest) {
			continue
		}
		if err := os.Remove(e.Path); err != nil {
			return removed, fmt.Errorf("cache: removing %s: %w", e.Path, err)
		}
		removed = append(removed, e.Path)
	}
	return removed, nil
}

// Clear deletes every stored scan and the `latest` symlink, and reports what
// it removed.
func (s Store) Clear() ([]string, error) {
	entries, err := s.List()
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, e := range entries {
		if err := os.Remove(e.Path); err != nil {
			return removed, fmt.Errorf("cache: removing %s: %w", e.Path, err)
		}
		removed = append(removed, e.Path)
	}
	link := filepath.Join(s.Dir, LatestLink)
	if err := os.Remove(link); err != nil && !errors.Is(err, os.ErrNotExist) {
		return removed, fmt.Errorf("cache: removing %s: %w", link, err)
	}
	return removed, nil
}
