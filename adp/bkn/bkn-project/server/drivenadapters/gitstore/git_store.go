// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package gitstore keeps the definitions of each release lineage in a bare git
// repository at <rootDir>/<lineage_id>.git, where the lineage id is the base
// network id. Every commit has this layout:
//
//	bkn/          the files of the bkn-backend export tar, paths kept as in the tar
//	functions/    the toolboxes the network uses, one directory per box id
//	skills/       the released Skills the network uses, one directory per skill id
//	project/      reserved: project metadata
//	release.yaml  manifest with the id, name, content digest and networks of each function and Skill
//
// CommitFiles replaces bkn/ and CommitCapabilities replaces functions/, skills/
// and release.yaml; each keeps every other top-level entry as it is. Only main
// carries the capability entries forward: a merge into a project branch takes
// them from main. A bare repository has no worktree, so commits are built
// directly from blob, tree and commit objects.
package gitstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// BKN_DIR is the top-level directory that holds the network definitions.
const BKN_DIR = "bkn"

// lineageIDPattern is the bkn-backend network id rule. Checking it also keeps
// the repository path inside the root directory.
var lineageIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)

var (
	gsOnce   sync.Once
	gsAccess interfaces.GitStore
)

type gitStore struct {
	rootDir string
	locks   *lineageLocks
	// held is set only on the store WithLineageLock hands to its callback: the
	// lineage whose write lock the callback already holds. That store serves no
	// other lineage and does not take the lock again.
	held string
}

// lineageLocks serialises writes per lineage. Publishing a release will take the
// same lock, so a release and a drift commit never interleave.
type lineageLocks struct {
	mu    sync.Mutex
	locks map[string]*sync.RWMutex
}

// rwLocker is the part of sync.RWMutex the store uses.
type rwLocker interface {
	Lock()
	Unlock()
	RLock()
	RUnlock()
}

// heldLock stands in for the lineage lock the caller of WithLineageLock already holds.
type heldLock struct{}

func (heldLock) Lock()    {}
func (heldLock) Unlock()  {}
func (heldLock) RLock()   {}
func (heldLock) RUnlock() {}

func NewGitStore(appSetting *common.AppSetting) interfaces.GitStore {
	gsOnce.Do(func() {
		gsAccess = newGitStore(appSetting.GitStoreSetting.RootDir)
	})
	return gsAccess
}

func newGitStore(rootDir string) *gitStore {
	return &gitStore{rootDir: rootDir, locks: &lineageLocks{locks: map[string]*sync.RWMutex{}}}
}

func (gs *gitStore) lock(lineageID string) rwLocker {
	if lineageID == gs.held {
		return heldLock{}
	}
	gs.locks.mu.Lock()
	defer gs.locks.mu.Unlock()
	l, ok := gs.locks.locks[lineageID]
	if !ok {
		l = &sync.RWMutex{}
		gs.locks.locks[lineageID] = l
	}
	return l
}

// repoPath is checked by every operation before it takes a lock. An id that cannot
// name a repository reports git.ErrRepositoryNotExists: no such repository can exist.
func (gs *gitStore) repoPath(lineageID string) (string, error) {
	if !lineageIDPattern.MatchString(lineageID) {
		return "", fmt.Errorf("invalid lineage id %q: %w", lineageID, git.ErrRepositoryNotExists)
	}
	// The store given to a WithLineageLock callback holds one lineage lock only.
	// Refusing other lineages keeps it from taking a second lock in an order
	// another caller could reverse.
	if gs.held != "" && lineageID != gs.held {
		return "", fmt.Errorf("store is bound to lineage %s, not %s", gs.held, lineageID)
	}
	return filepath.Join(gs.rootDir, lineageID+".git"), nil
}

func (gs *gitStore) open(lineageID string) (*git.Repository, error) {
	p, err := gs.repoPath(lineageID)
	if err != nil {
		return nil, err
	}
	repo, err := git.PlainOpen(p)
	if err != nil {
		return nil, fmt.Errorf("open repository of lineage %s: %w", lineageID, err)
	}
	return repo, nil
}

func (gs *gitStore) InitLineage(ctx context.Context, lineageID string, files map[string][]byte,
	message string, author interfaces.GitAuthor) (string, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "InitLineage")
	defer span.End()

	p, err := gs.repoPath(lineageID)
	if err != nil {
		span.SetStatus(codes.Error, "Invalid lineage id")
		return "", err
	}
	// The only lock taken for a lineage that does not exist yet. Its entry stays
	// even if the init fails: dropping a mutex another caller may be waiting on
	// would let two callers hold the lineage at once. The entries stay bounded,
	// since the lineage service inits only ids bkn-backend has just exported.
	l := gs.lock(lineageID)
	l.Lock()
	defer l.Unlock()

	if _, err := os.Stat(p); err == nil {
		span.SetStatus(codes.Error, "Repository exists")
		return "", fmt.Errorf("init repository of lineage %s: %w", lineageID, git.ErrRepositoryAlreadyExists)
	}
	// Build the repository in a temporary directory and move it into place in
	// one rename, so a crash mid-way never leaves a half-made <id>.git behind.
	// Temporary names start with "." and so never match the lineage id rule.
	gs.removeStaleTemps(lineageID)
	tmp, err := os.MkdirTemp(gs.rootDir, tempPrefix(lineageID)+"*.git")
	if err != nil {
		span.SetStatus(codes.Error, "Create temporary directory failed")
		return "", fmt.Errorf("init repository of lineage %s: %w", lineageID, err)
	}
	defer os.RemoveAll(tmp) // no-op once renamed

	hash, err := initBareRepository(tmp, files, message, author)
	if err != nil {
		span.SetStatus(codes.Error, "Init repository failed")
		return "", fmt.Errorf("init repository of lineage %s: %w", lineageID, err)
	}
	if err := os.Rename(tmp, p); err != nil {
		span.SetStatus(codes.Error, "Move repository failed")
		return "", fmt.Errorf("move repository of lineage %s into place: %w", lineageID, err)
	}
	span.SetStatus(codes.Ok, "")
	return hash.String(), nil
}

// tempPrefix ends in ".", which lineage ids cannot contain, so the temporaries of lineage
// "orders" never match those of "orders-v2", which may be mid-init under its own lock.
func tempPrefix(lineageID string) string {
	return ".tmp-" + lineageID + "."
}

// removeStaleTemps deletes temporary directories left by an InitLineage that
// was killed before its rename. The caller holds the lineage write lock.
func (gs *gitStore) removeStaleTemps(lineageID string) {
	stale, _ := filepath.Glob(filepath.Join(gs.rootDir, tempPrefix(lineageID)+"*.git"))
	for _, dir := range stale {
		_ = os.RemoveAll(dir)
	}
}

// initBareRepository creates a bare repository at dir with main at a first
// commit holding files under bkn/.
func initBareRepository(dir string, files map[string][]byte, message string, author interfaces.GitAuthor) (plumbing.Hash, error) {
	repo, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{
		InitOptions: git.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName(interfaces.GIT_MAIN_BRANCH)},
		Bare:        true,
	})
	if err != nil {
		return plumbing.ZeroHash, err
	}
	hash, err := commitBknDir(repo, nil, files, message, author)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	ref := plumbing.NewHashReference(plumbing.NewBranchReferenceName(interfaces.GIT_MAIN_BRANCH), hash)
	if err := repo.Storer.SetReference(ref); err != nil {
		return plumbing.ZeroHash, err
	}
	return hash, nil
}

func (gs *gitStore) CommitFiles(ctx context.Context, lineageID string, branch string, files map[string][]byte,
	message string, author interfaces.GitAuthor) (string, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "CommitFiles")
	defer span.End()

	repo, err := gs.open(lineageID)
	if err != nil {
		span.SetStatus(codes.Error, "Open repository failed")
		return "", err
	}
	l := gs.lock(lineageID)
	l.Lock()
	defer l.Unlock()

	branchRef := plumbing.NewBranchReferenceName(branch)
	head, err := repo.Reference(branchRef, true)
	if err != nil {
		span.SetStatus(codes.Error, "Read branch failed")
		return "", fmt.Errorf("read branch %s of lineage %s: %w", branch, lineageID, err)
	}
	parent, err := repo.CommitObject(head.Hash())
	if err != nil {
		span.SetStatus(codes.Error, "Read commit failed")
		return "", err
	}
	hash, err := commitBknDir(repo, parent, files, message, author)
	if err != nil {
		span.SetStatus(codes.Error, "Commit failed")
		return "", err
	}
	if hash != parent.Hash {
		if err := repo.Storer.SetReference(plumbing.NewHashReference(branchRef, hash)); err != nil {
			span.SetStatus(codes.Error, "Set reference failed")
			return "", err
		}
	}
	span.SetStatus(codes.Ok, "")
	return hash.String(), nil
}

func (gs *gitStore) CreateBranch(ctx context.Context, lineageID string, branch string, from string) error {
	_, span := oteltrace.StartNamedClientSpan(ctx, "CreateBranch")
	defer span.End()

	repo, err := gs.open(lineageID)
	if err != nil {
		span.SetStatus(codes.Error, "Open repository failed")
		return err
	}
	l := gs.lock(lineageID)
	l.Lock()
	defer l.Unlock()

	branchRef := plumbing.NewBranchReferenceName(branch)
	if err := branchRef.Validate(); err != nil {
		span.SetStatus(codes.Error, "Invalid branch name")
		return err
	}
	if _, err := repo.Reference(branchRef, false); err == nil {
		span.SetStatus(codes.Error, "Branch exists")
		return fmt.Errorf("branch %s of lineage %s: %w", branch, lineageID, git.ErrBranchExists)
	}
	hash, err := resolveCommit(repo, from)
	if err != nil {
		span.SetStatus(codes.Error, "Resolve ref failed")
		return err
	}
	if err := repo.Storer.SetReference(plumbing.NewHashReference(branchRef, hash)); err != nil {
		span.SetStatus(codes.Error, "Set reference failed")
		return err
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

func (gs *gitStore) Tag(ctx context.Context, lineageID string, name string, ref string) error {
	_, span := oteltrace.StartNamedClientSpan(ctx, "Tag")
	defer span.End()

	repo, err := gs.open(lineageID)
	if err != nil {
		span.SetStatus(codes.Error, "Open repository failed")
		return err
	}
	l := gs.lock(lineageID)
	l.Lock()
	defer l.Unlock()

	hash, err := resolveCommit(repo, ref)
	if err != nil {
		span.SetStatus(codes.Error, "Resolve ref failed")
		return err
	}
	if _, err := repo.CreateTag(name, hash, nil); err != nil {
		span.SetStatus(codes.Error, "Create tag failed")
		return fmt.Errorf("tag %s of lineage %s: %w", name, lineageID, err)
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

func (gs *gitStore) ReadTree(ctx context.Context, lineageID string, ref string) (map[string][]byte, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "ReadTree")
	defer span.End()

	repo, err := gs.open(lineageID)
	if err != nil {
		span.SetStatus(codes.Error, "Open repository failed")
		return nil, err
	}
	l := gs.lock(lineageID)
	l.RLock()
	defer l.RUnlock()

	tree, err := bknTree(repo, ref)
	if err != nil {
		span.SetStatus(codes.Error, "Read tree failed")
		return nil, err
	}
	files := map[string][]byte{}
	if err := readFiles(tree, "", files); err != nil {
		span.SetStatus(codes.Error, "Read files failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return files, nil
}

func (gs *gitStore) ReadCapabilities(ctx context.Context, lineageID string, ref string) (map[string][]byte, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "ReadCapabilities")
	defer span.End()

	repo, err := gs.open(lineageID)
	if err != nil {
		span.SetStatus(codes.Error, "Open repository failed")
		return nil, err
	}
	l := gs.lock(lineageID)
	l.RLock()
	defer l.RUnlock()

	commit, err := commitAt(repo, ref)
	if err != nil {
		span.SetStatus(codes.Error, "Resolve ref failed")
		return nil, err
	}
	root, err := commit.Tree()
	if err != nil {
		span.SetStatus(codes.Error, "Read tree failed")
		return nil, err
	}
	files := map[string][]byte{}
	for _, e := range root.Entries {
		if !isCapabilityEntry(e.Name) {
			continue
		}
		switch {
		case e.Mode == filemode.Dir:
			tree, err := repo.TreeObject(e.Hash)
			if err == nil {
				err = readFiles(tree, e.Name+"/", files)
			}
			if err != nil {
				span.SetStatus(codes.Error, "Read files failed")
				return nil, err
			}
		case e.Mode.IsFile():
			data, err := readBlob(repo, e.Hash)
			if err != nil {
				span.SetStatus(codes.Error, "Read files failed")
				return nil, err
			}
			files[e.Name] = data
		}
	}
	span.SetStatus(codes.Ok, "")
	return files, nil
}

func (gs *gitStore) CommitCapabilities(ctx context.Context, lineageID string, branch string, files map[string][]byte,
	message string, author interfaces.GitAuthor) (string, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "CommitCapabilities")
	defer span.End()

	repo, err := gs.open(lineageID)
	if err != nil {
		span.SetStatus(codes.Error, "Open repository failed")
		return "", err
	}
	l := gs.lock(lineageID)
	l.Lock()
	defer l.Unlock()

	dirs := map[string]map[string][]byte{interfaces.GIT_FUNCTIONS_DIR: {}, interfaces.GIT_SKILLS_DIR: {}}
	manifest, hasManifest := files[interfaces.GIT_MANIFEST_FILE]
	for p, data := range files {
		dir, rest, nested := strings.Cut(p, "/")
		if p == interfaces.GIT_MANIFEST_FILE {
			continue
		}
		if _, ok := dirs[dir]; !ok || !nested {
			span.SetStatus(codes.Error, "Invalid path")
			return "", fmt.Errorf("path %q is not under %s/ or %s/", p, interfaces.GIT_FUNCTIONS_DIR, interfaces.GIT_SKILLS_DIR)
		}
		dirs[dir][rest] = data
	}

	branchRef := plumbing.NewBranchReferenceName(branch)
	head, err := branchHead(repo, branchRef)
	if err != nil {
		span.SetStatus(codes.Error, "Read branch failed")
		return "", fmt.Errorf("read branch %s of lineage %s: %w", branch, lineageID, err)
	}
	root, err := head.Tree()
	if err != nil {
		span.SetStatus(codes.Error, "Read tree failed")
		return "", err
	}
	replaced := map[string]bool{interfaces.GIT_FUNCTIONS_DIR: true, interfaces.GIT_SKILLS_DIR: true}
	entries := []object.TreeEntry{}
	if hasManifest {
		replaced[interfaces.GIT_MANIFEST_FILE] = true
		hash, err := writeBlob(repo, manifest)
		if err != nil {
			span.SetStatus(codes.Error, "Write blob failed")
			return "", err
		}
		entries = append(entries, object.TreeEntry{Name: interfaces.GIT_MANIFEST_FILE, Mode: filemode.Regular, Hash: hash})
	}
	for _, e := range root.Entries {
		if !replaced[e.Name] {
			entries = append(entries, e)
		}
	}
	for dir, dirFiles := range dirs {
		if len(dirFiles) == 0 {
			continue
		}
		hash, err := writeTree(repo, dirFiles)
		if err != nil {
			span.SetStatus(codes.Error, "Write tree failed")
			return "", err
		}
		entries = append(entries, object.TreeEntry{Name: dir, Mode: filemode.Dir, Hash: hash})
	}
	rootHash, err := encodeTree(repo, entries)
	if err != nil {
		span.SetStatus(codes.Error, "Write tree failed")
		return "", err
	}
	if rootHash == head.TreeHash {
		span.SetStatus(codes.Ok, "")
		return head.Hash.String(), nil
	}
	hash, err := writeCommit(repo, rootHash, []plumbing.Hash{head.Hash}, message, author)
	if err != nil {
		span.SetStatus(codes.Error, "Commit failed")
		return "", err
	}
	if err := repo.Storer.SetReference(plumbing.NewHashReference(branchRef, hash)); err != nil {
		span.SetStatus(codes.Error, "Set reference failed")
		return "", err
	}
	span.SetStatus(codes.Ok, "")
	return hash.String(), nil
}

func (gs *gitStore) Diff(ctx context.Context, lineageID string, refA string, refB string) (*interfaces.PathDiff, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "Diff")
	defer span.End()

	repo, err := gs.open(lineageID)
	if err != nil {
		span.SetStatus(codes.Error, "Open repository failed")
		return nil, err
	}
	l := gs.lock(lineageID)
	l.RLock()
	defer l.RUnlock()

	a, err := blobHashes(repo, refA)
	if err != nil {
		span.SetStatus(codes.Error, "Read tree failed")
		return nil, err
	}
	b, err := blobHashes(repo, refB)
	if err != nil {
		span.SetStatus(codes.Error, "Read tree failed")
		return nil, err
	}
	diff := &interfaces.PathDiff{Added: []string{}, Changed: []string{}, Removed: []string{}}
	for p, hb := range b {
		ha, ok := a[p]
		switch {
		case !ok:
			diff.Added = append(diff.Added, p)
		case ha != hb:
			diff.Changed = append(diff.Changed, p)
		}
	}
	for p := range a {
		if _, ok := b[p]; !ok {
			diff.Removed = append(diff.Removed, p)
		}
	}
	sort.Strings(diff.Added)
	sort.Strings(diff.Changed)
	sort.Strings(diff.Removed)
	span.SetStatus(codes.Ok, "")
	return diff, nil
}

func (gs *gitStore) MergeBase(ctx context.Context, lineageID string, refA string, refB string) (string, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "MergeBase")
	defer span.End()

	repo, err := gs.open(lineageID)
	if err != nil {
		span.SetStatus(codes.Error, "Open repository failed")
		return "", err
	}
	l := gs.lock(lineageID)
	l.RLock()
	defer l.RUnlock()

	a, err := commitAt(repo, refA)
	if err != nil {
		span.SetStatus(codes.Error, "Resolve ref failed")
		return "", err
	}
	b, err := commitAt(repo, refB)
	if err != nil {
		span.SetStatus(codes.Error, "Resolve ref failed")
		return "", err
	}
	bases, err := a.MergeBase(b)
	if err != nil {
		span.SetStatus(codes.Error, "Merge base failed")
		return "", err
	}
	if len(bases) == 0 {
		span.SetStatus(codes.Error, "No merge base")
		return "", fmt.Errorf("%s and %s of lineage %s have no common ancestor", refA, refB, lineageID)
	}
	span.SetStatus(codes.Ok, "")
	return bases[0].Hash.String(), nil
}

func (gs *gitStore) ResolveRef(ctx context.Context, lineageID string, ref string) (string, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "ResolveRef")
	defer span.End()

	repo, err := gs.open(lineageID)
	if err != nil {
		span.SetStatus(codes.Error, "Open repository failed")
		return "", err
	}
	l := gs.lock(lineageID)
	l.RLock()
	defer l.RUnlock()

	hash, err := resolveCommit(repo, ref)
	if err != nil {
		span.SetStatus(codes.Error, "Resolve ref failed")
		return "", err
	}
	span.SetStatus(codes.Ok, "")
	return hash.String(), nil
}

func (gs *gitStore) FastForward(ctx context.Context, lineageID string, branch string, to string) (string, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "FastForward")
	defer span.End()

	repo, err := gs.open(lineageID)
	if err != nil {
		span.SetStatus(codes.Error, "Open repository failed")
		return "", err
	}
	l := gs.lock(lineageID)
	l.Lock()
	defer l.Unlock()

	branchRef := plumbing.NewBranchReferenceName(branch)
	head, err := branchHead(repo, branchRef)
	if err != nil {
		span.SetStatus(codes.Error, "Read branch failed")
		return "", fmt.Errorf("read branch %s of lineage %s: %w", branch, lineageID, err)
	}
	target, err := commitAt(repo, to)
	if err != nil {
		span.SetStatus(codes.Error, "Resolve ref failed")
		return "", err
	}
	ok, err := head.IsAncestor(target)
	if err != nil {
		span.SetStatus(codes.Error, "Ancestry check failed")
		return "", err
	}
	if !ok && head.Hash != target.Hash {
		span.SetStatus(codes.Error, "Not a fast-forward")
		return "", fmt.Errorf("branch %s of lineage %s cannot fast-forward to %s", branch, lineageID, to)
	}
	if err := repo.Storer.SetReference(plumbing.NewHashReference(branchRef, target.Hash)); err != nil {
		span.SetStatus(codes.Error, "Set reference failed")
		return "", err
	}
	span.SetStatus(codes.Ok, "")
	return target.Hash.String(), nil
}

func (gs *gitStore) CommitMerge(ctx context.Context, lineageID string, branch string, other string,
	files map[string][]byte, message string, author interfaces.GitAuthor) (string, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "CommitMerge")
	defer span.End()

	repo, err := gs.open(lineageID)
	if err != nil {
		span.SetStatus(codes.Error, "Open repository failed")
		return "", err
	}
	l := gs.lock(lineageID)
	l.Lock()
	defer l.Unlock()

	branchRef := plumbing.NewBranchReferenceName(branch)
	head, err := branchHead(repo, branchRef)
	if err != nil {
		span.SetStatus(codes.Error, "Read branch failed")
		return "", fmt.Errorf("read branch %s of lineage %s: %w", branch, lineageID, err)
	}
	otherCommit, err := commitAt(repo, other)
	if err != nil {
		span.SetStatus(codes.Error, "Resolve ref failed")
		return "", err
	}
	rootHash, err := mergeRootTree(repo, head, otherCommit, files)
	if err != nil {
		span.SetStatus(codes.Error, "Write tree failed")
		return "", err
	}
	hash, err := writeCommit(repo, rootHash, []plumbing.Hash{head.Hash, otherCommit.Hash}, message, author)
	if err != nil {
		span.SetStatus(codes.Error, "Commit failed")
		return "", err
	}
	if err := repo.Storer.SetReference(plumbing.NewHashReference(branchRef, hash)); err != nil {
		span.SetStatus(codes.Error, "Set reference failed")
		return "", err
	}
	span.SetStatus(codes.Ok, "")
	return hash.String(), nil
}

// WithLineageLock takes the lineage write lock once and hands fn a store whose
// operations on that lineage skip the lock. sync.RWMutex is not reentrant, so
// this is how fn reads and writes without deadlocking on the lock it runs under.
//
// The lineage must exist before its lock is taken: lock entries are never
// removed, so a lock per unknown id would let callers grow the map at will.
func (gs *gitStore) WithLineageLock(ctx context.Context, lineageID string, fn func(store interfaces.GitStore) error) error {
	if _, err := gs.open(lineageID); err != nil {
		return err
	}
	l := gs.lock(lineageID)
	l.Lock()
	defer l.Unlock()

	return fn(&gitStore{rootDir: gs.rootDir, locks: gs.locks, held: lineageID})
}

func branchHead(repo *git.Repository, branchRef plumbing.ReferenceName) (*object.Commit, error) {
	ref, err := repo.Reference(branchRef, true)
	if err != nil {
		return nil, err
	}
	return repo.CommitObject(ref.Hash())
}

func resolveCommit(repo *git.Repository, ref string) (plumbing.Hash, error) {
	hash, err := repo.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("resolve %s: %w", ref, err)
	}
	return *hash, nil
}

func commitAt(repo *git.Repository, ref string) (*object.Commit, error) {
	hash, err := resolveCommit(repo, ref)
	if err != nil {
		return nil, err
	}
	return repo.CommitObject(hash)
}

// bknTree returns the bkn/ tree of the commit at ref, or nil when the commit has none.
func bknTree(repo *git.Repository, ref string) (*object.Tree, error) {
	commit, err := commitAt(repo, ref)
	if err != nil {
		return nil, err
	}
	root, err := commit.Tree()
	if err != nil {
		return nil, err
	}
	tree, err := root.Tree(BKN_DIR)
	if errors.Is(err, object.ErrDirectoryNotFound) {
		return nil, nil
	}
	return tree, err
}

func blobHashes(repo *git.Repository, ref string) (map[string]plumbing.Hash, error) {
	tree, err := bknTree(repo, ref)
	if err != nil {
		return nil, err
	}
	hashes := map[string]plumbing.Hash{}
	if tree == nil {
		return hashes, nil
	}
	err = tree.Files().ForEach(func(f *object.File) error {
		hashes[f.Name] = f.Hash
		return nil
	})
	return hashes, err
}

// commitBknDir writes a commit whose bkn/ directory holds exactly files and
// whose other top-level entries are those of parent. It returns parent's hash
// without writing anything when the tree would not change.
func commitBknDir(repo *git.Repository, parent *object.Commit, files map[string][]byte,
	message string, author interfaces.GitAuthor) (plumbing.Hash, error) {
	rootHash, err := rootTreeWithBkn(repo, parent, files)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	if parent == nil {
		return writeCommit(repo, rootHash, nil, message, author)
	}
	if parent.TreeHash == rootHash {
		return parent.Hash, nil
	}
	return writeCommit(repo, rootHash, []plumbing.Hash{parent.Hash}, message, author)
}

func writeCommit(repo *git.Repository, rootHash plumbing.Hash, parents []plumbing.Hash,
	message string, author interfaces.GitAuthor) (plumbing.Hash, error) {
	sig := object.Signature{Name: author.Name, Email: author.Email, When: time.Now()}
	commit := &object.Commit{Author: sig, Committer: sig, Message: message, TreeHash: rootHash, ParentHashes: parents}
	obj := repo.Storer.NewEncodedObject()
	if err := commit.Encode(obj); err != nil {
		return plumbing.ZeroHash, err
	}
	return repo.Storer.SetEncodedObject(obj)
}

// rootTreeWithBkn writes a root tree whose bkn/ directory holds exactly files and
// whose other top-level entries are those of parent, and returns its hash.
func rootTreeWithBkn(repo *git.Repository, parent *object.Commit, files map[string][]byte) (plumbing.Hash, error) {
	entries := []object.TreeEntry{}
	if parent != nil {
		root, err := parent.Tree()
		if err != nil {
			return plumbing.ZeroHash, err
		}
		for _, e := range root.Entries {
			if e.Name != BKN_DIR {
				entries = append(entries, e)
			}
		}
	}
	if len(files) > 0 {
		bknHash, err := writeTree(repo, files)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		entries = append(entries, object.TreeEntry{Name: BKN_DIR, Mode: filemode.Dir, Hash: bknHash})
	}
	return encodeTree(repo, entries)
}

// mergeRootTree writes the root tree of a merge into a project branch: bkn/ holds
// files, the capability entries come from other (main, the only branch that writes
// them) and every other top-level entry from head.
func mergeRootTree(repo *git.Repository, head *object.Commit, other *object.Commit,
	files map[string][]byte) (plumbing.Hash, error) {
	headRoot, err := head.Tree()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	otherRoot, err := other.Tree()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	entries := []object.TreeEntry{}
	for _, e := range headRoot.Entries {
		if e.Name != BKN_DIR && !isCapabilityEntry(e.Name) {
			entries = append(entries, e)
		}
	}
	for _, e := range otherRoot.Entries {
		if isCapabilityEntry(e.Name) {
			entries = append(entries, e)
		}
	}
	if len(files) > 0 {
		bknHash, err := writeTree(repo, files)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		entries = append(entries, object.TreeEntry{Name: BKN_DIR, Mode: filemode.Dir, Hash: bknHash})
	}
	return encodeTree(repo, entries)
}

// isCapabilityEntry tells the top-level entries that hold functions and Skills.
func isCapabilityEntry(name string) bool {
	return name == interfaces.GIT_FUNCTIONS_DIR || name == interfaces.GIT_SKILLS_DIR || name == interfaces.GIT_MANIFEST_FILE
}

// readFiles adds the files of tree, recursively, to files under prefix.
func readFiles(tree *object.Tree, prefix string, files map[string][]byte) error {
	if tree == nil {
		return nil
	}
	return tree.Files().ForEach(func(f *object.File) error {
		r, err := f.Reader()
		if err != nil {
			return err
		}
		defer func() { _ = r.Close() }()
		data, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		files[prefix+f.Name] = data
		return nil
	})
}

func readBlob(repo *git.Repository, hash plumbing.Hash) ([]byte, error) {
	blob, err := repo.BlobObject(hash)
	if err != nil {
		return nil, err
	}
	r, err := blob.Reader()
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	return io.ReadAll(r)
}

// writeTree writes files as nested trees and returns the hash of the top one.
func writeTree(repo *git.Repository, files map[string][]byte) (plumbing.Hash, error) {
	blobs := map[string][]byte{}
	dirs := map[string]map[string][]byte{}
	for p, data := range files {
		if err := validatePath(p); err != nil {
			return plumbing.ZeroHash, err
		}
		name, rest, nested := strings.Cut(p, "/")
		if !nested {
			blobs[name] = data
			continue
		}
		if dirs[name] == nil {
			dirs[name] = map[string][]byte{}
		}
		dirs[name][rest] = data
	}

	entries := make([]object.TreeEntry, 0, len(blobs)+len(dirs))
	for name, data := range blobs {
		if _, clash := dirs[name]; clash {
			return plumbing.ZeroHash, fmt.Errorf("path %q is both a file and a directory", name)
		}
		hash, err := writeBlob(repo, data)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		entries = append(entries, object.TreeEntry{Name: name, Mode: filemode.Regular, Hash: hash})
	}
	for name, sub := range dirs {
		hash, err := writeTree(repo, sub)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		entries = append(entries, object.TreeEntry{Name: name, Mode: filemode.Dir, Hash: hash})
	}
	return encodeTree(repo, entries)
}

func encodeTree(repo *git.Repository, entries []object.TreeEntry) (plumbing.Hash, error) {
	sort.Sort(object.TreeEntrySorter(entries))
	obj := repo.Storer.NewEncodedObject()
	if err := (&object.Tree{Entries: entries}).Encode(obj); err != nil {
		return plumbing.ZeroHash, err
	}
	return repo.Storer.SetEncodedObject(obj)
}

func writeBlob(repo *git.Repository, data []byte) (plumbing.Hash, error) {
	obj := repo.Storer.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)
	w, err := obj.Writer()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	if _, err := w.Write(data); err != nil {
		_ = w.Close()
		return plumbing.ZeroHash, err
	}
	if err := w.Close(); err != nil {
		return plumbing.ZeroHash, err
	}
	return repo.Storer.SetEncodedObject(obj)
}

// validatePath accepts only clean, relative, slash-separated paths.
func validatePath(p string) error {
	if p == "" || path.IsAbs(p) || path.Clean(p) != p || p == "." || p == ".." ||
		strings.HasPrefix(p, "../") || strings.ContainsAny(p, "\\\x00") {
		return fmt.Errorf("invalid file path %q", p)
	}
	return nil
}
