package cache

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/asamgx/storix/internal/mac"
	"github.com/asamgx/storix/internal/walk"
)

// Store is a directory of scan cache files plus a `latest` symlink.
type Store struct{ Dir string }

// File naming.
const (
	// fileExt is the cache file extension.
	fileExt = ".strx"
	// filePrefix starts every cache file name.
	filePrefix = "scan-"
	// timeLayout is the sortable timestamp in a cache file name.
	timeLayout = "20060102-150405"
	// LatestLink is the symlink in the store directory that points at the
	// most recently written scan.
	LatestLink = "latest"
	// dirMode is the mode of the storix directories: private to the user.
	dirMode = 0o700
	// fileMode is the mode of a cache file.
	fileMode = 0o600
)

// DefaultStore returns the per-user store,
// ~/Library/Application Support/storix/scans.
//
// Under sudo it resolves the invoking user's home, not root's, so a privileged
// scan writes where the unprivileged one reads.
func DefaultStore() (Store, error) {
	home, err := invokingHome()
	if err != nil {
		return Store{}, err
	}
	return Store{Dir: filepath.Join(home, "Library", "Application Support", "storix", "scans")}, nil
}

func invokingHome() (string, error) {
	uid, _, viaSudo := mac.InvokingUser()
	if viaSudo {
		u, err := user.LookupId(strconv.Itoa(uid))
		if err != nil {
			return "", fmt.Errorf("cache: resolving the home of uid %d: %w", uid, err)
		}
		if u.HomeDir == "" {
			return "", fmt.Errorf("cache: uid %d has no home directory", uid)
		}
		return u.HomeDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cache: resolving the home directory: %w", err)
	}
	return home, nil
}

// Entry is one cache file in a store listing.
type Entry struct {
	Path string
	Meta Meta
	Size int64
	// Err is set when the file exists but its metadata could not be read; the
	// entry is still listed so the user can see and prune it.
	Err error
}

// Save writes a scan to the store and points `latest` at it.
//
// The file is written to a temporary name in the same directory and renamed,
// so a reader never sees a partial file, and `latest` is replaced by renaming
// a fresh symlink over it. Under sudo everything the store creates is chowned
// to the invoking user.
func (s Store) Save(meta Meta, t *walk.Tree) (string, error) {
	if s.Dir == "" {
		return "", errors.New("cache: store has no directory")
	}
	if err := s.ensureDir(); err != nil {
		return "", err
	}
	if meta.Written.IsZero() {
		meta.Written = time.Now()
	}

	path, err := s.freeName(meta.Written)
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(s.Dir, ".scan-*.tmp")
	if err != nil {
		return "", fmt.Errorf("cache: creating a temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := writeFile(tmp, meta, t); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("cache: closing %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, fileMode); err != nil {
		return "", fmt.Errorf("cache: setting the mode of %s: %w", tmpName, err)
	}
	if err := chownToInvoker(tmpName); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", fmt.Errorf("cache: installing %s: %w", path, err)
	}
	if err := s.linkLatest(filepath.Base(path)); err != nil {
		return path, err
	}
	return path, nil
}

// writeFile encodes the scan and flushes it to stable storage.
func writeFile(f *os.File, meta Meta, t *walk.Tree) error {
	if err := Write(f, meta, t); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("cache: syncing %s: %w", f.Name(), err)
	}
	return nil
}

// freeName returns the path for a scan written at t, adding a counter if a
// file with that timestamp already exists.
func (s Store) freeName(at time.Time) (string, error) {
	base := filePrefix + at.Format(timeLayout)
	for i := 0; i < 100; i++ {
		name := base + fileExt
		if i > 0 {
			name = base + "-" + strconv.Itoa(i+1) + fileExt
		}
		p := filepath.Join(s.Dir, name)
		if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
			return p, nil
		} else if err != nil {
			return "", fmt.Errorf("cache: checking %s: %w", p, err)
		}
	}
	return "", fmt.Errorf("cache: too many scans stored for %s", base)
}

// linkLatest atomically repoints the `latest` symlink at name.
func (s Store) linkLatest(name string) error {
	link := filepath.Join(s.Dir, LatestLink)
	tmp := link + ".tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink(name, tmp); err != nil {
		return fmt.Errorf("cache: creating the latest symlink: %w", err)
	}
	if err := chownToInvoker(tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("cache: installing the latest symlink: %w", err)
	}
	return nil
}

// Latest returns the newest scan: the target of the `latest` symlink, or the
// newest listed file when the link is missing or dangling.
func (s Store) Latest() (string, Meta, error) {
	link := filepath.Join(s.Dir, LatestLink)
	if target, err := os.Readlink(link); err == nil {
		if !filepath.IsAbs(target) {
			target = filepath.Join(s.Dir, target)
		}
		if m, err := readMetaFile(target); err == nil {
			return target, m, nil
		}
	}
	entries, err := s.List()
	if err != nil {
		return "", Meta{}, err
	}
	for _, e := range entries {
		if e.Err == nil {
			return e.Path, e.Meta, nil
		}
	}
	return "", Meta{}, fmt.Errorf("cache: no usable scan in %s: %w", s.Dir, os.ErrNotExist)
}

// Load reads a whole cache file.
func (s Store) Load(path string) (Meta, *walk.Tree, error) {
	f, err := os.Open(path)
	if err != nil {
		return Meta{}, nil, fmt.Errorf("cache: %w", err)
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return Meta{}, nil, fmt.Errorf("cache: %w", err)
	}
	return Read(f, st.Size())
}

// List returns the stored scans, newest first. Files whose metadata cannot be
// read are listed with Err set rather than hidden.
func (s Store) List() ([]Entry, error) {
	dirEntries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cache: reading %s: %w", s.Dir, err)
	}
	out := make([]Entry, 0, len(dirEntries))
	for _, de := range dirEntries {
		name := de.Name()
		if !de.Type().IsRegular() || !isCacheName(name) {
			continue
		}
		e := Entry{Path: filepath.Join(s.Dir, name)}
		if info, err := de.Info(); err == nil {
			e.Size = info.Size()
		}
		if m, err := readMetaFile(e.Path); err != nil {
			e.Err = err
		} else {
			e.Meta = m
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Meta.Written.Equal(out[j].Meta.Written) {
			return out[i].Meta.Written.After(out[j].Meta.Written)
		}
		return out[i].Path > out[j].Path
	})
	return out, nil
}

func isCacheName(name string) bool {
	return len(name) > len(filePrefix)+len(fileExt) &&
		name[:len(filePrefix)] == filePrefix &&
		filepath.Ext(name) == fileExt
}

func readMetaFile(path string) (Meta, error) {
	f, err := os.Open(path)
	if err != nil {
		return Meta{}, fmt.Errorf("cache: %w", err)
	}
	defer func() { _ = f.Close() }()
	return ReadMeta(f)
}

// ensureDir creates the store directory tree with private permissions and
// hands whatever it created to the invoking user.
func (s Store) ensureDir() error {
	var created []string
	for p := filepath.Clean(s.Dir); ; p = filepath.Dir(p) {
		if _, err := os.Stat(p); err == nil {
			break
		}
		created = append(created, p)
		if parent := filepath.Dir(p); parent == p {
			break
		}
	}
	if err := os.MkdirAll(s.Dir, dirMode); err != nil {
		return fmt.Errorf("cache: creating %s: %w", s.Dir, err)
	}
	for _, p := range created {
		if err := os.Chmod(p, dirMode); err != nil {
			return fmt.Errorf("cache: setting the mode of %s: %w", p, err)
		}
		if err := chownToInvoker(p); err != nil {
			return err
		}
	}
	return nil
}

// chownToInvoker gives a path to the user who invoked storix. It is a no-op
// unless the process is running as root through sudo.
func chownToInvoker(path string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	uid, gid, viaSudo := mac.InvokingUser()
	if !viaSudo {
		return nil
	}
	if err := os.Lchown(path, uid, gid); err != nil {
		return fmt.Errorf("cache: giving %s to uid %d: %w", path, uid, err)
	}
	return nil
}
