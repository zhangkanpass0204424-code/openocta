package knowledge

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tmc/langchaingo/embeddings"
)

type sharedEntry struct {
	mu       sync.Mutex
	engine   *Engine
	refs     int
	dataDir  string
	embedder embeddings.Embedder
}

var (
	sharedMu      sync.Mutex
	sharedEngines = map[string]*sharedEntry{}
)

func normalizeDataDir(dataDir string) (string, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return "", fmt.Errorf("skylark: dataDir is empty")
	}
	return filepath.Clean(dataDir), nil
}

func getOrCreateEntry(dataDir string, emb embeddings.Embedder) (*sharedEntry, error) {
	dataDir, err := normalizeDataDir(dataDir)
	if err != nil {
		return nil, err
	}

	sharedMu.Lock()
	defer sharedMu.Unlock()

	ent, ok := sharedEngines[dataDir]
	if !ok {
		ent = &sharedEntry{dataDir: dataDir, embedder: emb}
		sharedEngines[dataDir] = ent
	} else if ent.embedder == nil && emb != nil {
		ent.embedder = emb
	}
	return ent, nil
}

func openSharedEngine(ent *sharedEntry) (*Engine, error) {
	ent.mu.Lock()
	defer ent.mu.Unlock()
	if ent.engine != nil {
		return ent.engine, nil
	}
	eng, err := NewEngine(ent.dataDir, ent.embedder)
	if err != nil {
		return nil, err
	}
	ent.engine = eng
	return eng, nil
}

// PreloadEngine opens (or creates) the Bleve index for dataDir at process startup.
// The engine stays cached for subsequent AcquireEngine calls.
func PreloadEngine(dataDir string, emb embeddings.Embedder) error {
	ent, err := getOrCreateEntry(dataDir, emb)
	if err != nil {
		return err
	}
	_, err = openSharedEngine(ent)
	return err
}

// AcquireEngine returns a process-wide shared Engine for dataDir and a release
// callback to drop the runtime's reference. The underlying index is not closed
// when refs reach zero; use RebuildShared or ResetSharedEnginesForTests to tear down.
func AcquireEngine(dataDir string, emb embeddings.Embedder) (*Engine, func(), error) {
	ent, err := getOrCreateEntry(dataDir, emb)
	if err != nil {
		return nil, nil, err
	}
	eng, err := openSharedEngine(ent)
	if err != nil {
		return nil, nil, err
	}

	ent.mu.Lock()
	ent.refs++
	ent.mu.Unlock()

	release := func() {
		ent.mu.Lock()
		if ent.refs > 0 {
			ent.refs--
		}
		if ent.refs == 0 && ent.engine != nil {
			_ = ent.engine.Close()
			ent.engine = nil
		}
		ent.mu.Unlock()
	}
	return eng, release, nil
}

// RebuildShared rescans docs into the shared engine for dataDir. Only one rebuild
// per dataDir runs at a time; concurrent callers block until it finishes.
func RebuildShared(ctx context.Context, dataDir string, emb embeddings.Embedder, docs []Document) error {
	ent, err := getOrCreateEntry(dataDir, emb)
	if err != nil {
		return err
	}
	eng, err := openSharedEngine(ent)
	if err != nil {
		return err
	}
	return eng.Rebuild(ctx, docs)
}

// ResetSharedEnginesForTests closes and removes all cached engines (test only).
func ResetSharedEnginesForTests() {
	sharedMu.Lock()
	entries := sharedEngines
	sharedEngines = map[string]*sharedEntry{}
	sharedMu.Unlock()

	for _, ent := range entries {
		ent.mu.Lock()
		ent.refs = 0
		if ent.engine != nil {
			_ = ent.engine.Close()
			ent.engine = nil
		}
		ent.mu.Unlock()
	}
}
