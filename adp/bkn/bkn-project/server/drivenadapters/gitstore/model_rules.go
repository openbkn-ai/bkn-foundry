// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package gitstore

import (
	"context"
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const modelRulesFile = "knowledge-base.json"
const projectDir = "project"

func projectFiles(repo *git.Repository, root *object.Tree) (map[string][]byte, error) {
	files := map[string][]byte{}
	for _, entry := range root.Entries {
		if entry.Name != projectDir {
			continue
		}
		if entry.Mode != filemode.Dir {
			return nil, fmt.Errorf("project metadata is not a directory")
		}
		subtree, err := repo.TreeObject(entry.Hash)
		if err != nil {
			return nil, err
		}
		if err := readFiles(subtree, "", files); err != nil {
			return nil, err
		}
	}
	return files, nil
}
func (gs *gitStore) ReadModelRules(ctx context.Context, lineageID, ref string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	repo, err := gs.open(lineageID)
	if err != nil {
		return nil, err
	}
	lock := gs.lock(lineageID)
	lock.RLock()
	defer lock.RUnlock()
	commit, err := commitAt(repo, ref)
	if err != nil {
		return nil, err
	}
	root, err := commit.Tree()
	if err != nil {
		return nil, err
	}
	files, err := projectFiles(repo, root)
	if err != nil {
		return nil, err
	}
	return files[modelRulesFile], nil
}
func (gs *gitStore) CommitModel(ctx context.Context, lineageID, branch string, definitions map[string][]byte, rules []byte, message string, author interfaces.GitAuthor) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	repo, err := gs.open(lineageID)
	if err != nil {
		return "", err
	}
	lock := gs.lock(lineageID)
	lock.Lock()
	defer lock.Unlock()
	ref := plumbing.NewBranchReferenceName(branch)
	parent, err := branchHead(repo, ref)
	if err != nil {
		return "", err
	}
	previous, err := parent.Tree()
	if err != nil {
		return "", err
	}
	files, err := projectFiles(repo, previous)
	if err != nil {
		return "", err
	}
	if len(rules) == 0 {
		delete(files, modelRulesFile)
	} else {
		files[modelRulesFile] = rules
	}
	// Both directories are written before moving the branch once. A failed
	// write leaves the original head intact; unrelated project files survive.
	bknRoot, err := rootTreeWithBkn(repo, parent, definitions)
	if err != nil {
		return "", err
	}
	root, err := repo.TreeObject(bknRoot)
	if err != nil {
		return "", err
	}
	entries := []object.TreeEntry{}
	for _, entry := range root.Entries {
		if entry.Name != projectDir {
			entries = append(entries, entry)
		}
	}
	if len(files) > 0 {
		subtree, err := writeTree(repo, files)
		if err != nil {
			return "", err
		}
		entries = append(entries, object.TreeEntry{Name: projectDir, Mode: filemode.Dir, Hash: subtree})
	}
	treeHash, err := encodeTree(repo, entries)
	if err != nil {
		return "", err
	}
	if treeHash == parent.TreeHash {
		return parent.Hash.String(), nil
	}
	hash, err := writeCommit(repo, treeHash, []plumbing.Hash{parent.Hash}, message, author)
	if err != nil {
		return "", err
	}
	if err := repo.Storer.SetReference(plumbing.NewHashReference(ref, hash)); err != nil {
		return "", err
	}
	return hash.String(), nil
}

var _ interfaces.ModelGitStore = (*gitStore)(nil)
