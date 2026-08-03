package cheat

import (
	"fmt"
	"sync"
)

// Entry is a catalog cheat together with its current engine state.
type Entry struct {
	Cheat   Cheat `json:"cheat"`
	Enabled bool  `json:"enabled"`
}

type libraryEntry struct {
	cheat   Cheat
	codeIDs []string
	enabled bool
}

// Library applies catalog cheats to an engine as whole units. A cheat that
// needs several patches is enabled all-or-nothing: a patch that fails its
// expected-original check rolls the already-applied patches back.
type Library struct {
	mu      sync.Mutex
	engine  *Engine
	title   Title
	entries map[string]*libraryEntry
	order   []string
}

func NewLibrary(engine *Engine) (*Library, error) {
	if engine == nil {
		return nil, fmt.Errorf("cheat library engine is nil")
	}
	return &Library{
		engine:  engine,
		entries: make(map[string]*libraryEntry),
	}, nil
}

func (l *Library) Engine() *Engine {
	return l.engine
}

func (l *Library) Title() Title {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.title
}

// Import replaces the library contents with one catalog. Cheats already
// registered are disabled and removed first; whether that restores the guest
// bytes follows each cheat's RestoreOnDisable setting.
func (l *Library) Import(catalog Catalog) error {
	if err := catalog.Validate(); err != nil {
		return err
	}
	target, err := normalizeSHA256(catalog.Title.SHA256)
	if err != nil {
		return err
	}
	engineTarget := l.engine.TargetSHA256()
	if engineTarget == "" || target != engineTarget {
		return fmt.Errorf(
			"cheat catalog: %w: got %s, want %s",
			ErrWrongTarget,
			target,
			engineTarget,
		)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.clearLocked(); err != nil {
		return err
	}
	for _, entry := range catalog.Cheats {
		codes, err := entry.Codes(target)
		if err != nil {
			return err
		}
		stored := &libraryEntry{
			cheat:   entry,
			codeIDs: make([]string, 0, len(codes)),
		}
		for _, code := range codes {
			if _, err := l.engine.AddCode(code); err != nil {
				// Leave the partially registered cheat out of the library so
				// the engine and the library never disagree about a code.
				l.removeCodesLocked(stored.codeIDs)
				return fmt.Errorf("import cheat %q: %w", entry.ID, err)
			}
			stored.codeIDs = append(stored.codeIDs, code.ID)
		}
		l.entries[entry.ID] = stored
		l.order = append(l.order, entry.ID)
	}
	l.title = catalog.Title
	return nil
}

// Clear disables and removes every cheat in the library.
func (l *Library) Clear() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.clearLocked()
}

func (l *Library) clearLocked() error {
	var firstErr error
	for index := len(l.order) - 1; index >= 0; index-- {
		stored := l.entries[l.order[index]]
		if stored == nil {
			continue
		}
		if stored.enabled {
			if err := l.setEnabledLocked(stored, false); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		l.removeCodesLocked(stored.codeIDs)
	}
	l.entries = make(map[string]*libraryEntry)
	l.order = nil
	l.title = Title{}
	return firstErr
}

func (l *Library) removeCodesLocked(ids []string) {
	for index := len(ids) - 1; index >= 0; index-- {
		_ = l.engine.RemoveCode(ids[index])
	}
}

func (l *Library) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	entries := make([]Entry, 0, len(l.order))
	for _, id := range l.order {
		stored := l.entries[id]
		entries = append(entries, Entry{Cheat: stored.cheat, Enabled: stored.enabled})
	}
	return entries
}

func (l *Library) Entry(id string) (Entry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	stored := l.entries[id]
	if stored == nil {
		return Entry{}, false
	}
	return Entry{Cheat: stored.cheat, Enabled: stored.enabled}, true
}

// SetEnabled applies or removes every patch of one cheat.
func (l *Library) SetEnabled(id string, enabled bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	stored := l.entries[id]
	if stored == nil {
		return fmt.Errorf("%w: %s", ErrCheatNotFound, id)
	}
	return l.setEnabledLocked(stored, enabled)
}

func (l *Library) setEnabledLocked(stored *libraryEntry, enabled bool) error {
	if stored.enabled == enabled {
		return nil
	}
	if !enabled {
		var firstErr error
		for index := len(stored.codeIDs) - 1; index >= 0; index-- {
			if err := l.engine.DisableCode(stored.codeIDs[index]); err != nil &&
				firstErr == nil {
				firstErr = err
			}
		}
		stored.enabled = false
		if firstErr != nil {
			return fmt.Errorf("disable cheat %q: %w", stored.cheat.ID, firstErr)
		}
		return nil
	}
	for index, codeID := range stored.codeIDs {
		if err := l.engine.EnableCode(codeID); err != nil {
			// Restore from the catalog's expected bytes rather than relying on
			// RestoreOnDisable, so a half-applied cheat never survives the
			// failure that stopped it.
			for rollback := index - 1; rollback >= 0; rollback-- {
				_ = l.engine.DisableCode(stored.codeIDs[rollback])
				patch := stored.cheat.Patches[rollback]
				_ = l.engine.WriteBytes(
					uint32(patch.Address),
					patch.Expected,
					nil,
				)
			}
			return fmt.Errorf("enable cheat %q: %w", stored.cheat.ID, err)
		}
	}
	stored.enabled = true
	return nil
}
