// Package store implements ShelfU's persistent storage: a versioned
// state.json plus managed images/ and converted/ directories under a
// single root. It is purely synchronous and owns no goroutines; the
// caller (UI/main thread) drives every operation. One live process at a
// time owns the directory through an exclusive flock on <root>/.lock.
// Managed image items are persisted relative to <root>/images and
// converted items relative to <root>/converted; file items keep
// absolute paths.
package store

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/png" // register PNG decoder for image.DecodeConfig validation
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// Item kinds stored in the shelf.
const (
	KindFile  = "file"
	KindText  = "text"
	KindImage = "image"
)

const (
	stateVersion     = 1
	stateFileName    = "state.json"
	imagesDirName    = "images"
	convertedDirName = "converted"
	lockFileName     = ".lock"
	maxTextItems     = 30       // default cap on unpinned text items; see SetTextLimit
	maxImageDim      = 16384    // max width or height accepted by AddImage
	maxImagePixels   = 64 << 20 // max total pixels accepted by AddImage (64 MP)
)

// SourceClipboard marks items captured from the system clipboard.
const SourceClipboard = "clipboard"

// ErrLocked reports that another live process already holds this data
// directory. Open returns it wrapped with the root path; callers must
// exit instead of sharing the directory, so a second instance can never
// overwrite the first one's history.
var ErrLocked = errors.New("store: data directory is in use by another instance")

// Item is one shelf entry. Path is set for file and image items (image
// paths are managed files inside <root>/images); Text is set for text
// items. Pinned items are never evicted by the text limit and are
// skipped by RemoveOlderThan and Clear. Source records how the item was
// added (SourceClipboard for clipboard captures, empty otherwise) and
// SourceApp the bundle id of the app the copy came from, when known.
// OffTray hides the item from the tray page without deleting it (set by
// the tray's remove/clear for clipboard captures); it stays in the
// clipboard list, is cleared by pinning or by re-adding the same
// content, and only the clipboard page can truly remove the item.
// OffClip is the mirror for a capture removed from the clipboard page:
// the item stays on the tray, and a capture off both pages is deleted by
// the next removal. Both flags are cleared by pinning and by re-adding
// the same content.
// Converted marks a KindFile item whose file ShelfU itself created
// inside <root>/converted: removal paths delete that file like a
// managed image, while a plain KindFile item's path is never deleted.
// Missing is runtime-only and never persisted.
type Item struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Path      string    `json:"path,omitempty"`
	Text      string    `json:"text,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	Pinned    bool      `json:"pinned,omitempty"`
	Source    string    `json:"source,omitempty"`
	SourceApp string    `json:"sourceApp,omitempty"`
	OffTray   bool      `json:"offTray,omitempty"`
	OffClip   bool      `json:"offClip,omitempty"`
	Converted bool      `json:"converted,omitempty"`
	Missing   bool      `json:"-"`
	// OCRText is what text recognition found in the image (empty when it
	// found none); OCRDone marks it recognized so it is never processed
	// twice. SetOCR writes both.
	OCRText string `json:"ocrText,omitempty"`
	OCRDone bool   `json:"ocrDone,omitempty"`
}

// Store holds the shelf contents. Items is newest-first; the zero Item
// order is load order from disk. Root is the storage directory. The
// exclusive <root>/.lock flock is held from Open until Close/exit.
type Store struct {
	Items []Item `json:"items"`
	Root  string `json:"-"`
	lock  *os.File

	// fresh marks a data directory that had no history when it was opened:
	// the first run, and only it, shows the welcome card.
	fresh bool

	// migrationBlocked stops Save when no reliable pre-migration backup
	// could be kept. migrateBackup/migrateBytes describe the recovery
	// copy that must still be intact before the first post-migration
	// write is allowed to overwrite the original state.json.
	migrationBlocked error
	migrateBackup    string
	migrateBytes     []byte

	// textLimit caps how many unpinned text items are kept; it is applied
	// on the next text add, never retroactively.
	textLimit int
}

// MigrationError returns why writes were disabled after a failed
// pre-migration backup, or nil when saving is available. Fixing the
// condition and reopening the store clears it.
// Fresh reports whether this data directory had no history when it was
// opened.
func (s *Store) Fresh() bool { return s.fresh }

func (s *Store) MigrationError() error {
	if s == nil {
		return nil
	}
	return s.migrationBlocked
}

// stateFile is the on-disk JSON envelope (versioned).
type stateFile struct {
	Version int    `json:"version"`
	Items   []Item `json:"items"`
}

var idCounter atomic.Uint64

// DefaultRoot returns the default support directory
// (os.UserConfigDir()/ShelfU).
func DefaultRoot() (string, error) {
	return configRoot("ShelfU")
}

// LegacyRoot returns the support directory the app used before it was
// renamed (os.UserConfigDir()/Shelf). Startup migrates it to DefaultRoot
// once and then leaves it as a backup.
func LegacyRoot() (string, error) {
	return configRoot("Shelf")
}

// configRoot returns os.UserConfigDir()/name.
func configRoot(name string) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("store: locating config dir: %w", err)
	}
	return filepath.Join(dir, name), nil
}

// Open creates root/images and root/converted (0700) if needed, takes an
// exclusive advisory lock on <root>/.lock, and loads state.json. A
// second process opening the same directory gets ErrLocked and must
// exit; distinct data directories lock independently. When the
// converted path already exists as a symlink or any non-directory entry
// it is left untouched and everything converted-related is disabled for
// the session (AddConverted refuses, cleanup skips it) rather than
// following into a foreign directory. Missing state starts empty;
// corrupt or wrong-version state returns an error (lock released) and
// is never overwritten. File/image items whose paths are genuinely
// nonexistent are dropped; items whose paths exist but cannot be
// statted (e.g. permission errors) are kept with Missing=true.
// Leftover files and folders inside a real <root>/converted that no
// loaded item references (e.g. from a conversion interrupted before it
// was registered) are deleted best-effort; nothing outside
// <root>/converted is ever touched.
func Open(root string) (*Store, error) {
	if root == "" {
		return nil, fmt.Errorf("store: empty root path")
	}
	// 规范化数据根：相对路径按当前工作目录展开并清理，再解析软链接
	// 得到实际目录身份——同一数据目录无论用原路径还是软链接路径打开，
	// Root、受管路径与锁都指向同一处。
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("store: normalizing root %q: %w", root, err)
	}
	root = filepath.Clean(abs)
	if resolved, rerr := filepath.EvalSymlinks(root); rerr == nil {
		root = resolved
	}
	if err := os.MkdirAll(filepath.Join(root, imagesDirName), 0o700); err != nil {
		return nil, fmt.Errorf("store: creating data directories: %w", err)
	}
	// converted 位置若已是软链接或其它非目录条目，绝不跟随（不在其
	// 目标里建目录，也不删其中的文件）：convertedPath、AddConverted
	// 与孤儿清理随之全部拒绝，转换功能降级但仓库其它部分不受影响。
	fi, lerr := os.Lstat(filepath.Join(root, convertedDirName))
	switch {
	case lerr == nil && !fi.IsDir():
		// 保持原样：软链接或占位文件绝不跟随、绝不改动。
	case lerr == nil:
		// 已是真实目录。
	default:
		// 不存在（或无法判定）时交给 MkdirAll 创建/报告错误。
		if err := os.MkdirAll(filepath.Join(root, convertedDirName), 0o700); err != nil {
			return nil, fmt.Errorf("store: creating data directories: %w", err)
		}
	}
	lock, err := lockRoot(root)
	if err != nil {
		return nil, err
	}
	s := &Store{Root: root, lock: lock, textLimit: maxTextItems}
	ok := false
	defer func() {
		if !ok {
			s.Close()
		}
	}()
	data, err := os.ReadFile(s.statePath())
	if err != nil {
		if os.IsNotExist(err) {
			ok = true
			s.fresh = true // no history yet: the welcome card's turn
			s.cleanOrphanedConverted()
			return s, nil
		}
		return nil, fmt.Errorf("store: reading %s: %w", stateFileName, err)
	}
	var sf stateFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("store: parsing %s: %w", stateFileName, err)
	}
	if sf.Version != stateVersion {
		return nil, fmt.Errorf("store: unsupported state version %d", sf.Version)
	}
	seen := make(map[string]struct{}, len(sf.Items))
	kept := make([]Item, 0, len(sf.Items))
	migrate := false
	for _, it := range sf.Items {
		if _, dup := seen[it.ID]; it.ID == "" || dup {
			it.ID = newID()
		}
		seen[it.ID] = struct{}{}
		if it.Kind == KindText {
			it.Missing = false
			kept = append(kept, it)
			continue
		}
		if it.Kind == KindImage {
			// 旧格式（绝对路径或按当时数据根拼出的相对路径）在下次
			// 保存时改写为相对 images 目录的文件名；先留一份可恢复原件。
			if it.Path != "" && it.Path != filepath.Base(it.Path) {
				migrate = true
			}
			it.Path = s.resolveImagePath(it.Path)
		} else if it.Converted {
			// 转换产物与受管图片同样按文件名持久化，重开时解析回当前
			// converted 目录，保证数据目录整体搬迁后仍然有效。
			it.Path = s.resolveConvertedPath(it.Path)
		}
		_, statErr := os.Stat(it.Path)
		switch {
		case statErr == nil:
			it.Missing = false
			kept = append(kept, it)
		case !os.IsNotExist(statErr):
			// Path exists but errored (permission, IO): keep, flag it.
			it.Missing = true
			kept = append(kept, it)
		default:
			if it.Converted {
				// 转换产物的记录不因文件被删而静默丢弃：保留并标记
				// Missing，让用户知道曾经有过这项输出。
				it.Missing = true
				kept = append(kept, it)
				continue
			}
			// Stat says not-exist. Keep dangling symlinks (the link
			// itself still exists) and odd lstat errors as Missing.
			if _, lerr := os.Lstat(it.Path); !os.IsNotExist(lerr) {
				it.Missing = true
				kept = append(kept, it)
			}
			// else: genuinely nonexistent → cleaned.
		}
	}
	s.Items = kept
	if migrate {
		// 没有成功保留可靠恢复点之前，不允许后续 Save 覆盖待迁移原件。
		if path, berr := s.ensureMigrationBackup(data); berr != nil {
			s.migrationBlocked = berr
		} else {
			s.migrateBackup = path
			s.migrateBytes = data
		}
	}
	ok = true
	s.cleanOrphanedConverted()
	return s, nil
}

// resolveImagePath turns a persisted image entry into an absolute path
// under the current root. New state stores managed images relative to
// <root>/images (just the file name); older state may hold an absolute
// path or one built from a relative root at the time it was written.
// A managed image's stable identity is its file name inside the current
// images directory, so absolute paths that resolve into the managed
// directory — including entries written through a symlinked root — are
// unified to the current root's actual location. Absolute paths pointing
// elsewhere are kept as they were (an image item deliberately pointing
// outside must keep working).
func (s *Store) resolveImagePath(stored string) string {
	if stored == "" {
		return ""
	}
	if filepath.IsAbs(stored) {
		clean := filepath.Clean(stored)
		if resolved, err := filepath.EvalSymlinks(clean); err == nil && s.managedPath(resolved) {
			return filepath.Join(s.imagesDir(), filepath.Base(resolved))
		}
		return clean
	}
	return filepath.Join(s.imagesDir(), filepath.Base(stored))
}

// resolveConvertedPath turns a persisted converted entry into an absolute
// path under the current root, mirroring resolveImagePath: new state
// stores converted files relative to <root>/converted (just the file
// name), while an absolute path that resolves into the converted
// directory — including entries written through a symlinked root — is
// unified to the current root's actual location.
func (s *Store) resolveConvertedPath(stored string) string {
	if stored == "" {
		return ""
	}
	if filepath.IsAbs(stored) {
		clean := filepath.Clean(stored)
		if resolved, err := filepath.EvalSymlinks(clean); err == nil && s.convertedPath(resolved) {
			return filepath.Join(s.convertedDir(), filepath.Base(resolved))
		}
		return clean
	}
	return filepath.Join(s.convertedDir(), filepath.Base(stored))
}

// cleanOrphanedConverted deletes unreferenced direct children of
// <root>/converted — leftovers from a conversion interrupted between
// writing the output and registering it. It runs on Open only, is
// best-effort (errors are ignored; a future Open retries), and only ever
// touches entries directly inside a real <root>/converted: if converted
// is a symlink (or anything but a real directory) cleanup is skipped so a
// foreign directory's contents are never touched. Regular files and
// symlinks are unlinked themselves (never followed); folders — extracted
// archives — are removed with os.RemoveAll, which likewise never follows
// a link inside them; other special entries are left alone.
func (s *Store) cleanOrphanedConverted() {
	if !s.convertedDirReal() {
		return
	}
	dir := s.convertedDir()
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, ent := range ents {
		fi, err := ent.Info() // lstat 语义：软链接按链接本身判定。
		if err != nil {
			continue
		}
		p := filepath.Join(dir, ent.Name())
		referenced := false
		for _, it := range s.Items {
			if it.Path != "" && sameFilePath(it.Path, p) {
				referenced = true
				break
			}
		}
		if referenced {
			continue
		}
		switch {
		case fi.Mode().IsRegular() || fi.Mode()&os.ModeSymlink != 0:
			_ = os.Remove(p)
		case fi.IsDir():
			_ = os.RemoveAll(p)
		}
	}
}

// migrationBackupNames are tried in order for the pre-migration recovery
// copy: the conventional name first, then independent alternates so an
// existing backup is never overwritten.
var migrationBackupNames = []string{
	stateFileName + ".bak",
	stateFileName + ".bak-2",
	stateFileName + ".bak-3",
	stateFileName + ".bak-4",
	stateFileName + ".bak-5",
}

// ensureMigrationBackup guarantees a readable recovery copy of the state
// that the upcoming format migration will overwrite. An existing backup
// is reused only when it is byte-identical to the pending original; a
// directory, unreadable file, or unrelated/truncated copy is not a valid
// recovery point, so a new independent backup file is created instead of
// being overwritten. Every create/write/sync/close error is reported and
// a failed attempt leaves no partial file behind.
func (s *Store) ensureMigrationBackup(data []byte) (string, error) {
	first := filepath.Join(s.Root, migrationBackupNames[0])
	if existing, err := os.ReadFile(first); err == nil && bytes.Equal(existing, data) {
		return first, nil
	}
	var lastErr error
	for _, name := range migrationBackupNames {
		path := filepath.Join(s.Root, name)
		if _, err := os.Lstat(path); err == nil {
			continue // 已存在（文件、目录或链接）：不覆盖，换下一个名字。
		}
		if err := writeFileNew(path, data); err != nil {
			lastErr = err
			continue
		}
		return path, nil
	}
	if lastErr == nil {
		lastErr = errors.New("every backup name is taken")
	}
	return "", fmt.Errorf("cannot write a recovery backup for the migration: %w", lastErr)
}

// writeFileNew creates path exclusively and writes data, reporting every
// create/write/sync/close error. On failure the partial file is removed
// so a half-written backup can never be mistaken for a recovery point.
func writeFileNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// lockRoot takes the exclusive advisory lock that gives one process
// ownership of a data directory. flock is tied to the open file, so the
// kernel releases it when the descriptor closes — including abnormal
// process death — and a leftover .lock file never blocks later runs.
// Two Stores in the same process conflict too, which is the intended
// contract: one live handle per directory.
func lockRoot(root string) (*os.File, error) {
	lockPath := filepath.Join(root, lockFileName)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("store: opening lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			holder := ""
			if data, rerr := os.ReadFile(lockPath); rerr == nil {
				if pid := strings.TrimSpace(string(data)); pid != "" {
					holder = " (pid " + pid + ")"
				}
			}
			return nil, fmt.Errorf("%w%s: %s", ErrLocked, holder, root)
		}
		return nil, fmt.Errorf("store: locking %s: %w", root, err)
	}
	// Best-effort diagnostics: record the holder pid for the conflict
	// message above. The lock, not this text, is what enforces exclusion.
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(fmt.Sprintf("%d\n", os.Getpid())), 0)
	return f, nil
}

// Close releases the directory lock. It is safe to call more than once
// and on a nil Store.
func (s *Store) Close() error {
	if s == nil || s.lock == nil {
		return nil
	}
	err := s.lock.Close()
	s.lock = nil
	return err
}

// AddFiles adds path references (no copying). Each path is normalized
// to an absolute path; a file item with the same normalized path is
// moved to the front instead of duplicated. Paths are processed in
// order, so the last path ends up newest. The change is persisted
// atomically; on save failure memory is rolled back.
func (s *Store) AddFiles(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	old := slices.Clone(s.Items)
	for _, p := range paths {
		if p == "" {
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			s.Items = old
			return fmt.Errorf("store: normalizing path %q: %w", p, err)
		}
		_, statErr := os.Stat(abs)
		missing := statErr != nil
		idx := slices.IndexFunc(s.Items, func(it Item) bool {
			return it.Kind == KindFile && it.Path == abs
		})
		if idx >= 0 {
			s.moveToFront(idx)
			s.Items[0].Missing = missing
			continue
		}
		s.Items = append([]Item{{
			ID:        newID(),
			Kind:      KindFile,
			Path:      abs,
			CreatedAt: time.Now(),
			Missing:   missing,
		}}, s.Items...)
	}
	if err := s.Save(); err != nil {
		s.Items = old
		return err
	}
	return nil
}

// ConvertedDir returns the managed directory (<root>/converted) where
// ShelfU writes files it creates by conversion. Open creates it.
func (s *Store) ConvertedDir() string { return filepath.Join(s.Root, convertedDirName) }

// AddConverted registers files ShelfU produced inside <root>/converted.
// Every path must resolve to an existing regular file inside that
// directory (spellings through symlinks are resolved first, so a file
// merely pointing outside is rejected); otherwise nothing is added and
// an error names the offending path. One KindFile item per path is
// added at the front in the given order — paths[0] becomes Items[0] —
// with Converted set so removal paths delete the file like a managed
// image. The change is persisted atomically; on save failure memory is
// rolled back and the files are left for the caller. Returns the new
// item IDs in the same order as paths.
func (s *Store) AddConverted(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	// 先整体验证，再改动内存：任一路径不合格都不落任何项。
	resolved := make([]string, len(paths))
	for i, p := range paths {
		if p == "" {
			return nil, fmt.Errorf("store: converted path %d is empty", i)
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("store: normalizing path %q: %w", p, err)
		}
		if r, rerr := filepath.EvalSymlinks(abs); rerr == nil {
			abs = r
		}
		if !s.convertedPath(abs) {
			return nil, fmt.Errorf("store: converted path %q is not inside %s", p, s.convertedDir())
		}
		fi, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("store: converted file %q: %w", p, err)
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("store: converted path %q is not a regular file", p)
		}
		resolved[i] = abs
	}
	now := time.Now()
	old := slices.Clone(s.Items)
	newItems := make([]Item, 0, len(resolved))
	ids := make([]string, len(resolved))
	for i, p := range resolved {
		ids[i] = newID()
		newItems = append(newItems, Item{
			ID:        ids[i],
			Kind:      KindFile,
			Path:      p,
			CreatedAt: now,
			Converted: true,
		})
	}
	s.Items = append(newItems, s.Items...)
	if err := s.Save(); err != nil {
		s.Items = old
		return nil, err
	}
	return ids, nil
}

// AddText adds a text item to the front. Identical existing text is
// moved to the front instead of duplicated. Only the textLimit newest
// unpinned text items are kept. Persisted atomically; save failure
// rolls back.
func (s *Store) AddText(text string) error {
	return s.addText(text, false, "")
}

// AddClipText adds text copied from the clipboard, recording Source and
// SourceApp so captures can be told apart from dropped or typed text.
// Behaves like AddText otherwise: identical existing text moves to the
// front keeping its ID and Pinned flag, and its provenance is refreshed
// to this capture's.
func (s *Store) AddClipText(text, sourceApp string) error {
	return s.addText(text, true, sourceApp)
}

func (s *Store) addText(text string, clip bool, sourceApp string) error {
	old := slices.Clone(s.Items)
	idx := slices.IndexFunc(s.Items, func(it Item) bool {
		return it.Kind == KindText && it.Text == text
	})
	if idx >= 0 {
		s.moveToFront(idx)
		s.Items[0].OffTray = false // 重新收录同一条内容会把它带回托盘
		s.Items[0].OffClip = false // 也会把它带回剪贴板页
		if clip {
			s.Items[0].Source = SourceClipboard
			s.Items[0].SourceApp = sourceApp
		}
	} else {
		it := Item{
			ID:        newID(),
			Kind:      KindText,
			Text:      text,
			CreatedAt: time.Now(),
		}
		if clip {
			it.Source = SourceClipboard
			it.SourceApp = sourceApp
		}
		s.Items = append([]Item{it}, s.Items...)
		s.trimText()
	}
	if err := s.Save(); err != nil {
		s.Items = old
		return err
	}
	return nil
}

// AddImage validates png bytes (PNG format, sane bounds), writes them as
// a managed 0600 file under <root>/images, and adds an image item.
// Identical content (SHA-256 of the bytes) is deduplicated by moving the
// existing item to the front. Persisted atomically; on save failure memory
// is rolled back and only a file this call newly created and that the
// rolled-back list does not reference is removed — pre-existing or
// still-referenced images are never deleted.
func (s *Store) AddImage(png []byte) error {
	return s.addImage(png, false, "")
}

// AddClipImage adds an image copied from the clipboard with the same
// validation, dedup and managed-file guarantees as AddImage, recording
// Source and SourceApp for the capture.
func (s *Store) AddClipImage(png []byte, sourceApp string) error {
	return s.addImage(png, true, sourceApp)
}

func (s *Store) addImage(png []byte, clip bool, sourceApp string) error {
	if len(png) == 0 {
		return fmt.Errorf("store: empty image data")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(png))
	if err != nil {
		return fmt.Errorf("store: decoding image: %w", err)
	}
	if format != "png" {
		return fmt.Errorf("store: unsupported image format %q, want png", format)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxImageDim || cfg.Height > maxImageDim {
		return fmt.Errorf("store: image dimensions %dx%d exceed limit %d", cfg.Width, cfg.Height, maxImageDim)
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxImagePixels {
		return fmt.Errorf("store: image %dx%d exceeds %d pixel limit", cfg.Width, cfg.Height, maxImagePixels)
	}
	// DecodeConfig 只看头部；必须完整解码，才能拒绝“合法签名 + IHDR
	// 但没有像素数据”的截断/损坏图片，避免把坏图当成功图片保存。
	if _, _, err := image.Decode(bytes.NewReader(png)); err != nil {
		return fmt.Errorf("store: decoding image data: %w", err)
	}
	sum := sha256.Sum256(png)
	id := fmt.Sprintf("%x", sum)
	name := id + ".png"
	path := filepath.Join(s.imagesDir(), name)
	old := slices.Clone(s.Items)
	for i, it := range s.Items {
		// 同一张图片可能以不同拼写（软链接路径、旧绝对路径）被记录，
		// 按实际文件身份去重，避免出现两条同 ID 的图片项。
		if it.Kind == KindImage && it.Path != "" && sameFilePath(it.Path, path) {
			// 按内容命名，不在每次截图时重新读取全部历史图片。
			created, err := s.ensureImageFile(name, png, sum)
			if err != nil {
				return fmt.Errorf("store: writing image: %w", err)
			}
			s.moveToFront(i)
			s.Items[0].Missing = false
			s.Items[0].OffTray = false // 重新收录同一张图片会把它带回托盘
			s.Items[0].OffClip = false // 也会把它带回剪贴板页
			if clip {
				s.Items[0].Source = SourceClipboard
				s.Items[0].SourceApp = sourceApp
			}
			if err := s.Save(); err != nil {
				s.Items = old
				if created && !s.pathReferenced(path) {
					_ = os.Remove(path)
				}
				return err
			}
			return nil
		}
	}
	created, err := s.ensureImageFile(name, png, sum)
	if err != nil {
		return fmt.Errorf("store: writing image: %w", err)
	}
	newItem := Item{
		ID:        id,
		Kind:      KindImage,
		Path:      path,
		CreatedAt: time.Now(),
	}
	if clip {
		newItem.Source = SourceClipboard
		newItem.SourceApp = sourceApp
	}
	s.Items = append([]Item{newItem}, s.Items...)
	if err := s.Save(); err != nil {
		s.Items = old
		// 只清理本次新建、且回滚后已无人引用的文件。文件早已存在
		// （例如被移除的图片文件仍被列表里的文件引用指着）时绝不删除，
		// 否则会破坏既有引用并丢失托盘图片。
		if created && !s.pathReferenced(path) {
			_ = os.Remove(path)
		}
		return err
	}
	return nil
}

// ensureImageFile guarantees images/<name> holds exactly the already-verified
// png whose SHA-256 is sum (the content-addressed name is derived from it).
// An existing file is reused only after proving identical: regular file,
// same size, and matching SHA-256 — a mere name hit is never trusted, since
// the managed file may have been replaced or truncated out from under us.
// On any mismatch the supplied png is restored atomically (temp + rename).
// Stat errors other than nonexistence and read errors are reported instead
// of being treated as "missing", so an unverifiable file is never blindly
// overwritten. It reports whether this call introduced a previously
// nonexistent file so failure cleanup removes only genuinely new files — a
// restored pre-existing path still counts as pre-existing and must never be
// deleted on rollback.
func (s *Store) ensureImageFile(name string, png []byte, sum [32]byte) (created bool, err error) {
	path := filepath.Join(s.imagesDir(), name)
	fi, statErr := os.Stat(path)
	switch {
	case statErr == nil:
		if fi.Mode().IsRegular() && fi.Size() == int64(len(png)) {
			f, err := os.Open(path)
			if err != nil {
				return false, fmt.Errorf("store: reading image %s: %w", name, err)
			}
			h := sha256.New()
			_, copyErr := io.Copy(h, f)
			closeErr := f.Close()
			if copyErr != nil {
				return false, fmt.Errorf("store: reading image %s: %w", name, copyErr)
			}
			if closeErr != nil {
				return false, fmt.Errorf("store: closing image %s: %w", name, closeErr)
			}
			if bytes.Equal(h.Sum(nil), sum[:]) {
				return false, nil
			}
		}
		if err := writeFileAtomic(s.imagesDir(), name, png, 0o600); err != nil {
			return false, err
		}
		return false, nil
	case os.IsNotExist(statErr):
		if err := writeFileAtomic(s.imagesDir(), name, png, 0o600); err != nil {
			return false, err
		}
		return true, nil
	default:
		return false, fmt.Errorf("store: checking image %s: %w", name, statErr)
	}
}

// Remove deletes the item with the given id and persists atomically
// (memory rolls back on save failure). If the removed item owned a
// managed file — a managed image inside <root>/images or a converted
// file inside <root>/converted — that is no longer referenced by any
// remaining item, the file is deleted after the save succeeds; deletion
// is only ever attempted inside those managed directories, and a
// cleanup failure is returned as a visible error. Original user files
// are never deleted.
func (s *Store) Remove(id string) error {
	idx := slices.IndexFunc(s.Items, func(it Item) bool { return it.ID == id })
	if idx < 0 {
		return fmt.Errorf("store: no item with id %q", id)
	}
	removed := s.Items[idx]
	old := slices.Clone(s.Items)
	next := make([]Item, 0, len(s.Items)-1)
	next = append(next, s.Items[:idx]...)
	next = append(next, s.Items[idx+1:]...)
	s.Items = next
	if err := s.Save(); err != nil {
		s.Items = old
		return err
	}
	failed, err := s.deleteManagedFiles([]Item{removed})
	if err != nil {
		return fmt.Errorf("store: item removed, but deleting managed file %s failed: %w", failed, err)
	}
	return nil
}

// SetPinned marks or unmarks the item with the given id as pinned and
// persists atomically (memory rolls back on save failure). Pinning also
// clears OffTray and OffClip: a pinned item belongs at the top of both
// pages. An unknown id is an error; setting the flag to its current value
// is a no-op unless pinning an item hidden from a page, which still saves
// to unhide it.
func (s *Store) SetPinned(id string, pinned bool) error {
	idx := slices.IndexFunc(s.Items, func(it Item) bool { return it.ID == id })
	if idx < 0 {
		return fmt.Errorf("store: no item with id %q", id)
	}
	if s.Items[idx].Pinned == pinned && (!pinned || (!s.Items[idx].OffTray && !s.Items[idx].OffClip)) {
		return nil
	}
	old := s.Items[idx]
	s.Items[idx].Pinned = pinned
	if pinned {
		s.Items[idx].OffTray = false
		s.Items[idx].OffClip = false
	}
	if err := s.Save(); err != nil {
		s.Items[idx] = old
		return err
	}
	return nil
}

// SetTextLimit sets how many unpinned text items are kept; values below
// 1 restore the default. The new limit applies on the next text add and
// does not trim existing items.
func (s *Store) SetTextLimit(n int) {
	if n < 1 {
		n = maxTextItems
	}
	s.textLimit = n
}

// RemoveMany removes the items with the given IDs and saves once. Unknown
// IDs are ignored; nothing to remove => no write. Returns how many were
// removed. Guarantees match Clear: on save failure memory is rolled back
// and no file is deleted; afterwards managed images and converted files
// no longer referenced by any remaining item are deleted; files
// referenced by plain KindFile items are never touched.
func (s *Store) RemoveMany(ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	want := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		want[id] = struct{}{}
	}
	return s.removeMatching(func(it Item) bool {
		_, ok := want[it.ID]
		return ok
	}, "removed")
}

// HideFromTray marks the items with the given IDs OffTray and saves once.
// The items stay in the store — the clipboard page still lists them and
// managed files are never touched here; they only stop showing on the
// tray. Unknown IDs and items already off the tray are ignored; nothing
// to hide => no write, (0, nil). On save failure memory is rolled back.
// Returns how many items were newly hidden.
func (s *Store) HideFromTray(ids []string) (int, error) {
	return s.hideFrom(ids, func(it *Item) *bool { return &it.OffTray })
}

// HideFromClipboard marks the items with the given IDs OffClip and saves
// once — the clipboard page's mirror of HideFromTray: the capture stays
// on the tray, and no managed file is touched. Unknown IDs and items
// already off the clipboard are ignored; nothing to hide => no write,
// (0, nil). On save failure memory is rolled back. Returns how many items
// were newly hidden.
func (s *Store) HideFromClipboard(ids []string) (int, error) {
	return s.hideFrom(ids, func(it *Item) *bool { return &it.OffClip })
}

// hideFrom sets the flag off picks on the items with the given IDs and
// saves once; it is the shared body of the two HideFrom* methods.
func (s *Store) hideFrom(ids []string, off func(*Item) *bool) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	want := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		want[id] = struct{}{}
	}
	old := slices.Clone(s.Items)
	n := 0
	for i := range s.Items {
		if _, ok := want[s.Items[i].ID]; !ok {
			continue
		}
		if p := off(&s.Items[i]); !*p {
			*p = true
			n++
		}
	}
	if n == 0 {
		return 0, nil
	}
	if err := s.Save(); err != nil {
		s.Items = old
		return 0, err
	}
	return n, nil
}

// RemoveOlderThan removes every unpinned item added before cutoff and
// saves once; pinned items are kept regardless of age. It returns how
// many items it removed. Guarantees match Clear: on save failure memory
// is rolled back and no file is deleted; afterwards managed images and
// converted files no longer referenced by any remaining item are
// deleted; files referenced by plain KindFile items are never touched.
// Re-adding an existing item refreshes its timestamp, so "added" means
// last surfaced: items at the cutoff or newer are kept.
func (s *Store) RemoveOlderThan(cutoff time.Time) (int, error) {
	return s.removeMatching(func(it Item) bool {
		return !it.Pinned && it.CreatedAt.Before(cutoff)
	}, "removed")
}

// removeMatching drops every item match accepts and persists once.
// Nothing matched => no write, (0, nil). On save failure memory is
// rolled back and nothing is deleted. After the save succeeds, managed
// image and converted files the removed items owned are deleted only
// when no remaining item still references them. Returns how many items
// were removed; a cleanup failure reports that count plus the first
// error, worded with verb ("removed"/"cleared").
func (s *Store) removeMatching(match func(Item) bool, verb string) (int, error) {
	old := s.Items
	kept := make([]Item, 0, len(s.Items))
	var removed []Item
	for _, it := range s.Items {
		if match(it) {
			removed = append(removed, it)
		} else {
			kept = append(kept, it)
		}
	}
	if len(removed) == 0 {
		return 0, nil
	}
	if kept == nil {
		kept = []Item{}
	}
	s.Items = kept
	if err := s.Save(); err != nil {
		s.Items = old
		return 0, err
	}
	failed, err := s.deleteManagedFiles(removed)
	if err != nil {
		return len(removed), fmt.Errorf("store: items %s, but deleting managed file %s failed: %w", verb, failed, err)
	}
	return len(removed), nil
}

// managedFile reports whether removing item it should also delete its
// file, and returns that file's path: KindImage items own their managed
// file inside <root>/images, and KindFile items flagged Converted own
// theirs inside <root>/converted. Everything else — plain KindFile
// references in particular — is never deleted.
func (s *Store) managedFile(it Item) string {
	switch {
	case it.Kind == KindImage && it.Path != "" && s.managedPath(it.Path):
		return it.Path
	case it.Kind == KindFile && it.Converted && it.Path != "" && s.convertedEntryPath(it.Path):
		return it.Path
	}
	return ""
}

// convertedEntryPath reports whether p names an entry directly inside a
// real <root>/converted, judged by where the path is spelled, never by
// where p itself resolves: a symlink there is shelf-owned as a link and is
// unlinked without following it. The parent is resolved so a converted
// folder reached through a symlinked spelling is still recognised.
func (s *Store) convertedEntryPath(p string) bool {
	if p == "" || !s.convertedDirReal() {
		return false
	}
	p = filepath.Clean(p)
	parent, err := filepath.EvalSymlinks(filepath.Dir(p))
	if err != nil {
		return false
	}
	dir, err := filepath.EvalSymlinks(s.convertedDir())
	if err != nil {
		return false
	}
	return parent == dir && p != dir
}

// deleteManagedFiles deletes the managed file each item in removed owns
// (see managedFile) — only paths inside the managed directories, each
// distinct path once, and only when no remaining item still references
// the same file (kept items, including pinned ones, always protect their
// files). A converted entry that is a real folder directly inside
// <root>/converted (an extracted archive) is removed with os.RemoveAll,
// tree and all; everything else is unlinked. Returns the path of the
// first deletion error for callers to embed in their message; the other
// files are still removed.
func (s *Store) deleteManagedFiles(removed []Item) (string, error) {
	var firstErr error
	var failedPath string
	seen := map[string]bool{}
	for _, it := range removed {
		p := s.managedFile(it)
		if p == "" {
			continue
		}
		p = filepath.Clean(p)
		if seen[p] {
			continue
		}
		seen[p] = true
		if s.pathReferenced(it.Path) {
			continue
		}
		var err error
		if s.removableConvertedDir(p) {
			err = os.RemoveAll(p)
		} else {
			err = os.Remove(p)
		}
		if err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr, failedPath = err, it.Path
		}
	}
	return failedPath, firstErr
}

// removableConvertedDir reports whether p is a real folder sitting
// directly inside a real <root>/converted, so deleting it may take the
// whole tree with os.RemoveAll. The check never follows a symlink: p
// itself must lstat as a directory (a link is unlinked instead) and its
// parent must resolve to converted itself. Nothing outside converted can
// ever satisfy both.
func (s *Store) removableConvertedDir(p string) bool {
	if !s.convertedDirReal() {
		return false
	}
	fi, err := os.Lstat(p)
	if err != nil || !fi.IsDir() {
		return false
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(p))
	if err != nil {
		return false
	}
	dir, err := filepath.EvalSymlinks(s.convertedDir())
	if err != nil {
		return false
	}
	return parent == dir
}

// Clear removes every unpinned item and persists atomically (memory
// rolls back on save failure); pinned items and the managed files they
// reference are kept. When everything is pinned or the store is empty
// it is a no-op. After the save succeeds, managed image files and
// converted files or folders the cleared items owned are deleted — only
// paths inside <root>/images or <root>/converted are ever touched, and a
// plain KindFile item's path is never deleted even if it points there.
// Cleanup failures are reported while still removing the rest.
func (s *Store) Clear() error {
	_, err := s.removeMatching(func(it Item) bool { return !it.Pinned }, "cleared")
	return err
}

// Refresh re-stats every non-text item and updates Missing. It never
// removes items; only Open cleans up nonexistent paths.
func (s *Store) Refresh() {
	for i := range s.Items {
		it := &s.Items[i]
		if it.Kind == KindText {
			continue
		}
		_, err := os.Stat(it.Path)
		it.Missing = err != nil
	}
}

// Save persists the current items to state.json atomically
// (temp file + rename, 0600). Managed image items are written relative
// to <root>/images and converted items relative to <root>/converted
// (their file names) so reopening from a different working directory
// resolves them identically; file items keep their absolute paths.
// On failure the previous state.json is left untouched.
func (s *Store) Save() error {
	if s.migrationBlocked != nil {
		return fmt.Errorf("store: no recovery backup before the migration, so writing stopped rather than overwrite a recoverable original: %w", s.migrationBlocked)
	}
	if s.migrateBackup != "" {
		// 首次迁移写入前再次确认恢复点仍然可用，避免备份被删除或改写后
		// 原件被无保护地覆盖。
		backup, err := os.ReadFile(s.migrateBackup)
		if err != nil || !bytes.Equal(backup, s.migrateBytes) {
			return fmt.Errorf("store: the migration recovery backup %s is gone, refusing to overwrite the original", s.migrateBackup)
		}
	}
	items := s.persistItems()
	if items == nil {
		items = []Item{}
	}
	data, err := json.MarshalIndent(stateFile{Version: stateVersion, Items: items}, "", "  ")
	if err != nil {
		return fmt.Errorf("store: encoding state: %w", err)
	}
	if err := writeFileAtomic(s.Root, stateFileName, data, 0o600); err != nil {
		return fmt.Errorf("store: writing %s: %w", stateFileName, err)
	}
	// 原件已被新格式替换且备份仍保留在同目录；迁移保护结束。
	s.migrateBackup = ""
	s.migrateBytes = nil
	return nil
}

// persistItems clones the item list for the on-disk format: managed
// images become relative to <root>/images and converted files relative
// to <root>/converted; everything else is written as-is.
func (s *Store) persistItems() []Item {
	out := slices.Clone(s.Items)
	for i := range out {
		it := &out[i]
		switch {
		case it.Kind == KindImage && it.Path != "" && s.managedPath(it.Path):
			it.Path = filepath.Base(it.Path)
		case it.Converted && it.Path != "" && s.convertedPath(it.Path):
			it.Path = filepath.Base(it.Path)
		}
	}
	return out
}

func (s *Store) statePath() string { return filepath.Join(s.Root, stateFileName) }
func (s *Store) imagesDir() string { return filepath.Join(s.Root, imagesDirName) }

// convertedDir is the private spelling of ConvertedDir.
func (s *Store) convertedDir() string { return filepath.Join(s.Root, convertedDirName) }

// managedPath reports whether p is inside <root>/images, so cleanup only
// ever deletes files we created. Symlinked spellings are resolved first,
// so the same directory or file reached through another path is judged
// by where it actually lives.
func (s *Store) managedPath(p string) bool {
	return insideDir(s.imagesDir(), p)
}

// convertedPath reports whether p is inside a real <root>/converted,
// with the same symlink-resolved semantics as managedPath: removal
// paths only ever delete converted files we created. When the converted
// path itself is a symlink or anything but a real directory it reports
// false for every input, so nothing through it is ever treated as
// managed.
func (s *Store) convertedPath(p string) bool {
	return s.convertedDirReal() && insideDir(s.convertedDir(), p)
}

// convertedDirReal reports whether <root>/converted lstats as a real
// directory. A symlink (or any other entry) there is never followed:
// AddConverted, cleanup and orphan removal all refuse to operate inside
// it rather than risk touching a foreign directory's files.
func (s *Store) convertedDirReal() bool {
	fi, err := os.Lstat(s.convertedDir())
	return err == nil && fi.IsDir()
}

// insideDir reports whether p is inside dir, resolving symlinked
// spellings of both first so the same directory or file reached through
// another path is judged by where it actually lives.
func insideDir(dir, p string) bool {
	if p == "" {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	rel, err := filepath.Rel(dir, p)
	if err != nil || rel == "." || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// sameFilePath reports whether two paths denote the same file: equal
// after cleaning, or the same device+inode (os.SameFile). Paths that
// cannot be resolved fall back to the cleaned-string comparison only.
func sameFilePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	if ca == cb {
		return true
	}
	fa, errA := os.Stat(ca)
	if errA != nil {
		return false
	}
	fb, errB := os.Stat(cb)
	if errB != nil {
		return false
	}
	return os.SameFile(fa, fb)
}

// pathReferenced reports whether any remaining item references the same
// actual file as p. Identity is established by os.SameFile, so an item
// pointing at the file through a symlinked directory or a different
// spelling still counts as a reference. Each remaining item is
// classified explicitly:
//
//   - same cleaned path, or same device+inode → referenced;
//   - stat succeeds with a different file, or the path genuinely does
//     not exist → confirmed not a reference;
//   - any other stat failure (permissions, I/O) → cannot be confirmed,
//     so the path counts as referenced and the caller must not delete.
//
// The same conservative rule applies when the target itself cannot be
// statted: never delete on uncertainty.
func (s *Store) pathReferenced(p string) bool {
	if p == "" {
		return false
	}
	clean := filepath.Clean(p)
	target, err := os.Stat(clean)
	if err != nil {
		// 目标状态无法确认：保守视为仍被引用，宁可不删。
		return true
	}
	for _, it := range s.Items {
		if it.Path == "" {
			continue
		}
		if filepath.Clean(it.Path) == clean {
			return true
		}
		fi, serr := os.Stat(it.Path)
		if serr == nil {
			if os.SameFile(target, fi) {
				return true
			}
			continue // 确认是另一个文件。
		}
		if os.IsNotExist(serr) {
			continue // 路径确实不存在：不可能是（存在的）目标文件。
		}
		// 权限、I/O 等错误只能说明“无法确认”是否仍指向目标；
		// 保守视为被引用，宁可暂时留下托管文件也不丢数据。
		return true
	}
	return false
}

// moveToFront moves Items[i] to index 0 and refreshes its timestamp,
// preserving the item ID.
func (s *Store) moveToFront(i int) {
	it := s.Items[i]
	it.CreatedAt = time.Now()
	next := make([]Item, 0, len(s.Items))
	next = append(next, it)
	next = append(next, s.Items[:i]...)
	next = append(next, s.Items[i+1:]...)
	s.Items = next
}

// trimText keeps only the textLimit newest unpinned text items; pinned
// texts are never evicted and do not count towards the limit. Items
// hidden from a page (OffTray, OffClip) still count: hiding a row never
// lets the history grow past the limit.
func (s *Store) trimText() {
	limit := s.textLimit
	if limit < 1 {
		limit = maxTextItems
	}
	n := 0
	next := make([]Item, 0, len(s.Items))
	for _, it := range s.Items {
		if it.Kind == KindText && !it.Pinned {
			if n >= limit {
				continue
			}
			n++
		}
		next = append(next, it)
	}
	s.Items = next
}

// writeFileAtomic writes data to dir/name via a temp file + rename.
func writeFileAtomic(dir, name string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(dir, "."+name+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filepath.Join(dir, name))
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("i%d-%d", time.Now().UnixNano(), idCounter.Add(1))
	}
	return fmt.Sprintf("%x-%x-%d", time.Now().UnixNano(), b, idCounter.Add(1))
}
