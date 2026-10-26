// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package gitstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

var author = interfaces.GitAuthor{Name: "u1", Email: "u1@bkn-project"}

func Test_GitStore(t *testing.T) {
	Convey("Test gitStore\n", t, func() {
		gs := newGitStore(t.TempDir())
		ctx := context.Background()
		base := map[string][]byte{
			"network.bkn":              []byte("network v1"),
			"object_types/order.bkn":   []byte("order v1"),
			"object_types/product.bkn": []byte("product v1"),
		}

		first, err := gs.InitLineage(ctx, "orders", base, "init", author)
		So(err, ShouldBeNil)

		Convey("InitLineage writes a bare repository with main at the first commit\n", func() {
			repo, err := git.PlainOpen(gs.rootDir + "/orders.git")
			So(err, ShouldBeNil)
			_, err = repo.Worktree()
			So(err, ShouldEqual, git.ErrIsBareRepository)
			head, err := repo.Head()
			So(err, ShouldBeNil)
			So(head.Name(), ShouldEqual, plumbing.NewBranchReferenceName("main"))
			So(head.Hash().String(), ShouldEqual, first)

			files, err := gs.ReadTree(ctx, "orders", "main")
			So(err, ShouldBeNil)
			So(files, ShouldResemble, base)
		})

		Convey("InitLineage refuses an existing lineage and an invalid id\n", func() {
			_, err := gs.InitLineage(ctx, "orders", base, "again", author)
			So(err, ShouldNotBeNil)
			_, err = gs.InitLineage(ctx, "../escape", base, "init", author)
			So(err, ShouldNotBeNil)
		})

		Convey("A failed InitLineage leaves nothing behind and can be retried\n", func() {
			_, err := gs.InitLineage(ctx, "retry", map[string][]byte{"../escape.bkn": []byte("x")}, "init", author)
			So(err, ShouldNotBeNil)
			_, statErr := os.Stat(gs.rootDir + "/retry.git")
			So(os.IsNotExist(statErr), ShouldBeTrue)

			// A temporary directory left by a killed run is cleaned up on retry.
			So(os.MkdirAll(gs.rootDir+"/.tmp-retry.stale.git/objects", 0o755), ShouldBeNil)
			// The temporary of lineage "retry-x", which may be mid-init under its own lock, is not ours.
			So(os.MkdirAll(gs.rootDir+"/.tmp-retry-x.1.git/objects", 0o755), ShouldBeNil)
			_, err = gs.InitLineage(ctx, "retry", base, "init", author)
			So(err, ShouldBeNil)
			leftovers, _ := filepath.Glob(gs.rootDir + "/.tmp-retry.*")
			So(leftovers, ShouldBeEmpty)
			_, err = os.Stat(gs.rootDir + "/.tmp-retry-x.1.git")
			So(err, ShouldBeNil)
			files, err := gs.ReadTree(ctx, "retry", "main")
			So(err, ShouldBeNil)
			So(files, ShouldResemble, base)
		})

		Convey("CommitFiles replaces bkn/ and keeps the other top-level entries\n", func() {
			// Plant release.yaml beside bkn/ the way a later publish would.
			repo, _ := git.PlainOpen(gs.rootDir + "/orders.git")
			parent, _ := repo.CommitObject(plumbing.NewHash(first))
			root, _ := parent.Tree()
			manifest, err := writeBlob(repo, []byte("version: 1.0.0"))
			So(err, ShouldBeNil)
			entries := append([]object.TreeEntry{}, root.Entries...)
			entries = append(entries, object.TreeEntry{Name: "release.yaml", Mode: filemode.Regular, Hash: manifest})
			rootHash, err := encodeTree(repo, entries)
			So(err, ShouldBeNil)
			sig := object.Signature{Name: "u1", When: time.Now()}
			obj := repo.Storer.NewEncodedObject()
			So((&object.Commit{Author: sig, Committer: sig, Message: "manifest", TreeHash: rootHash,
				ParentHashes: []plumbing.Hash{parent.Hash}}).Encode(obj), ShouldBeNil)
			planted, err := repo.Storer.SetEncodedObject(obj)
			So(err, ShouldBeNil)
			So(repo.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName("main"), planted)), ShouldBeNil)

			next := map[string][]byte{
				"network.bkn":            []byte("network v1"),
				"object_types/order.bkn": []byte("order v2"),
				"action_types/ship.bkn":  []byte("ship v1"),
			}
			second, err := gs.CommitFiles(ctx, "orders", "main", next, "edit", author)
			So(err, ShouldBeNil)

			files, err := gs.ReadTree(ctx, "orders", "main")
			So(err, ShouldBeNil)
			So(files, ShouldResemble, next)

			commit, _ := repo.CommitObject(plumbing.NewHash(second))
			So(commit.ParentHashes, ShouldResemble, []plumbing.Hash{planted})
			So(commit.Message, ShouldEqual, "edit")
			So(commit.Author.Name, ShouldEqual, "u1")
			tree, _ := commit.Tree()
			entry, err := tree.FindEntry("release.yaml")
			So(err, ShouldBeNil)
			So(entry.Hash, ShouldEqual, manifest)

			Convey("and an unchanged set makes no commit\n", func() {
				again, err := gs.CommitFiles(ctx, "orders", "main", next, "noop", author)
				So(err, ShouldBeNil)
				So(again, ShouldEqual, second)
			})
		})

		Convey("CommitFiles rejects unsafe paths and an unknown branch\n", func() {
			for _, p := range []string{"/abs.bkn", "../up.bkn", "a//b.bkn", "./a.bkn", ""} {
				_, err := gs.CommitFiles(ctx, "orders", "main", map[string][]byte{p: []byte("x")}, "bad", author)
				So(err, ShouldNotBeNil)
			}
			_, err := gs.CommitFiles(ctx, "orders", "nope", base, "x", author)
			So(err, ShouldNotBeNil)
			_, err = gs.CommitFiles(ctx, "missing", "main", base, "x", author)
			So(err, ShouldNotBeNil)
		})

		Convey("CreateBranch, Tag, Diff and MergeBase follow the history\n", func() {
			So(gs.Tag(ctx, "orders", "v1.0.0", "main"), ShouldBeNil)
			So(gs.Tag(ctx, "orders", "v1.0.0", "main"), ShouldNotBeNil)
			So(gs.CreateBranch(ctx, "orders", "project-a", "v1.0.0"), ShouldBeNil)
			So(gs.CreateBranch(ctx, "orders", "project-a", "main"), ShouldNotBeNil)
			So(gs.CreateBranch(ctx, "orders", "project-b", "no-such-ref"), ShouldNotBeNil)

			onBranch := map[string][]byte{
				"network.bkn":              []byte("network v1"),
				"object_types/order.bkn":   []byte("order on branch"),
				"object_types/product.bkn": []byte("product v1"),
				"relation_types/buys.bkn":  []byte("buys v1"),
			}
			branchHead, err := gs.CommitFiles(ctx, "orders", "project-a", onBranch, "branch edit", author)
			So(err, ShouldBeNil)
			onMain := map[string][]byte{
				"network.bkn":            []byte("network v2"),
				"object_types/order.bkn": []byte("order v1"),
			}
			_, err = gs.CommitFiles(ctx, "orders", "main", onMain, "main edit", author)
			So(err, ShouldBeNil)

			base, err := gs.MergeBase(ctx, "orders", "main", "project-a")
			So(err, ShouldBeNil)
			So(base, ShouldEqual, first)

			diff, err := gs.Diff(ctx, "orders", "v1.0.0", "project-a")
			So(err, ShouldBeNil)
			So(diff, ShouldResemble, &interfaces.PathDiff{
				Added:   []string{"relation_types/buys.bkn"},
				Changed: []string{"object_types/order.bkn"},
				Removed: []string{},
			})
			diff, err = gs.Diff(ctx, "orders", "v1.0.0", "main")
			So(err, ShouldBeNil)
			So(diff, ShouldResemble, &interfaces.PathDiff{
				Added:   []string{},
				Changed: []string{"network.bkn"},
				Removed: []string{"object_types/product.bkn"},
			})
			diff, err = gs.Diff(ctx, "orders", branchHead, branchHead)
			So(err, ShouldBeNil)
			So(diff.Empty(), ShouldBeTrue)

			files, err := gs.ReadTree(ctx, "orders", "v1.0.0")
			So(err, ShouldBeNil)
			So(files, ShouldResemble, map[string][]byte{
				"network.bkn":              []byte("network v1"),
				"object_types/order.bkn":   []byte("order v1"),
				"object_types/product.bkn": []byte("product v1"),
			})
		})

		Convey("ResolveRef resolves branches and reports a missing ref or lineage\n", func() {
			head, err := gs.ResolveRef(ctx, "orders", "main")
			So(err, ShouldBeNil)
			So(head, ShouldEqual, first)
			_, err = gs.ResolveRef(ctx, "orders", "refs/heads/nope")
			So(errors.Is(err, plumbing.ErrReferenceNotFound), ShouldBeTrue)
			_, err = gs.ResolveRef(ctx, "missing", "main")
			So(errors.Is(err, git.ErrRepositoryNotExists), ShouldBeTrue)
			_, err = gs.ResolveRef(ctx, "../escape", "main")
			So(errors.Is(err, git.ErrRepositoryNotExists), ShouldBeTrue)
		})

		Convey("FastForward moves a branch only forward\n", func() {
			So(gs.CreateBranch(ctx, "orders", "project/p1", "main"), ShouldBeNil)
			next, err := gs.CommitFiles(ctx, "orders", "main", map[string][]byte{"network.bkn": []byte("network v2")}, "main edit", author)
			So(err, ShouldBeNil)

			head, err := gs.FastForward(ctx, "orders", "project/p1", "main")
			So(err, ShouldBeNil)
			So(head, ShouldEqual, next)
			got, _ := gs.ResolveRef(ctx, "orders", "project/p1")
			So(got, ShouldEqual, next)

			// The branch moves ahead; going back to its own ancestor is refused.
			_, err = gs.CommitFiles(ctx, "orders", "project/p1", map[string][]byte{"network.bkn": []byte("network v3")}, "branch edit", author)
			So(err, ShouldBeNil)
			_, err = gs.FastForward(ctx, "orders", "project/p1", first)
			So(err, ShouldNotBeNil)
			_, err = gs.FastForward(ctx, "orders", "project/none", "main")
			So(errors.Is(err, plumbing.ErrReferenceNotFound), ShouldBeTrue)
		})

		Convey("CommitMerge writes a two-parent commit and keeps the other top-level entries\n", func() {
			So(gs.CreateBranch(ctx, "orders", "project/p1", "main"), ShouldBeNil)
			mainHead, err := gs.CommitFiles(ctx, "orders", "main", map[string][]byte{"network.bkn": []byte("network v2")}, "main edit", author)
			So(err, ShouldBeNil)
			branchFiles := map[string][]byte{"network.bkn": []byte("network v1"), "object_types/order.bkn": []byte("order v2")}
			branchHead, err := gs.CommitFiles(ctx, "orders", "project/p1", branchFiles, "branch edit", author)
			So(err, ShouldBeNil)

			merged := map[string][]byte{"network.bkn": []byte("network v2"), "object_types/order.bkn": []byte("order v2")}
			hash, err := gs.CommitMerge(ctx, "orders", "project/p1", "refs/heads/main", merged, "merge main", author)
			So(err, ShouldBeNil)

			repo, _ := git.PlainOpen(gs.rootDir + "/orders.git")
			commit, _ := repo.CommitObject(plumbing.NewHash(hash))
			So(commit.ParentHashes, ShouldResemble, []plumbing.Hash{plumbing.NewHash(branchHead), plumbing.NewHash(mainHead)})
			So(commit.Message, ShouldEqual, "merge main")
			files, err := gs.ReadTree(ctx, "orders", "project/p1")
			So(err, ShouldBeNil)
			So(files, ShouldResemble, merged)
			base, err := gs.MergeBase(ctx, "orders", "main", "project/p1")
			So(err, ShouldBeNil)
			So(base, ShouldEqual, mainHead)

			Convey("even when the tree does not change\n", func() {
				again, err := gs.CommitMerge(ctx, "orders", "project/p1", "main", merged, "merge again", author)
				So(err, ShouldBeNil)
				So(again, ShouldNotEqual, hash)
				commit, _ := repo.CommitObject(plumbing.NewHash(again))
				So(commit.NumParents(), ShouldEqual, 2)
			})
		})

		Convey("WithLineageLock runs fn under the lineage lock with a store that does not relock\n", func() {
			var head string
			err := gs.WithLineageLock(ctx, "orders", func(store interfaces.GitStore) error {
				// Reads and writes through the given store do not deadlock.
				files, err := store.ReadTree(ctx, "orders", "main")
				if err != nil {
					return err
				}
				files["object_types/order.bkn"] = []byte("order v2")
				head, err = store.CommitFiles(ctx, "orders", "main", files, "locked edit", author)
				return err
			})
			So(err, ShouldBeNil)
			got, _ := gs.ResolveRef(ctx, "orders", "main")
			So(got, ShouldEqual, head)

			// A writer on the outer store waits until fn returns.
			release := make(chan struct{})
			entered := make(chan struct{})
			done := make(chan struct{})
			go func() {
				_ = gs.WithLineageLock(ctx, "orders", func(interfaces.GitStore) error {
					close(entered)
					<-release
					return nil
				})
			}()
			<-entered
			go func() {
				_, _ = gs.CommitFiles(ctx, "orders", "main", base, "outer", author)
				close(done)
			}()
			select {
			case <-done:
				t.Fatal("CommitFiles ran while the lineage lock was held")
			case <-time.After(50 * time.Millisecond):
			}
			close(release)
			<-done

			// The given store serves its own lineage only.
			_, err = gs.InitLineage(ctx, "other", base, "init", author)
			So(err, ShouldBeNil)
			err = gs.WithLineageLock(ctx, "orders", func(store interfaces.GitStore) error {
				_, err := store.ReadTree(ctx, "other", "main")
				return err
			})
			So(err, ShouldNotBeNil)
			So(gs.WithLineageLock(ctx, "../escape", func(interfaces.GitStore) error { return nil }), ShouldNotBeNil)
		})

		Convey("An empty file set leaves no bkn/ directory and reads back empty\n", func() {
			_, err := gs.CommitFiles(ctx, "orders", "main", map[string][]byte{}, "clear", author)
			So(err, ShouldBeNil)
			files, err := gs.ReadTree(ctx, "orders", "main")
			So(err, ShouldBeNil)
			So(files, ShouldBeEmpty)
		})

		Convey("Concurrent commits to one lineage are serialised\n", func() {
			const n = 8
			var wg sync.WaitGroup
			errs := make([]error, n)
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					files := map[string][]byte{"network.bkn": []byte(fmt.Sprintf("network %d", i))}
					_, errs[i] = gs.CommitFiles(ctx, "orders", "main", files, fmt.Sprintf("c%d", i), author)
				}(i)
			}
			wg.Wait()
			for _, err := range errs {
				So(err, ShouldBeNil)
			}

			// Every commit landed on main: none was lost to a concurrent ref update.
			repo, _ := git.PlainOpen(gs.rootDir + "/orders.git")
			head, _ := repo.Reference(plumbing.NewBranchReferenceName("main"), true)
			count := 0
			commit, err := repo.CommitObject(head.Hash())
			So(err, ShouldBeNil)
			for {
				count++
				if commit.NumParents() == 0 {
					break
				}
				commit, err = commit.Parent(0)
				So(err, ShouldBeNil)
			}
			So(count, ShouldEqual, n+1)
		})
	})
}
