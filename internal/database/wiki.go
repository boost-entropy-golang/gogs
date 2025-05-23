// Copyright 2015 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package database

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/unknwon/com"

	"github.com/gogs/git-module"

	"gogs.io/gogs/internal/conf"
	"gogs.io/gogs/internal/repoutil"
	"gogs.io/gogs/internal/sync"
)

var wikiWorkingPool = sync.NewExclusivePool()

// ToWikiPageURL formats a string to corresponding wiki URL name.
func ToWikiPageURL(name string) string {
	return url.QueryEscape(name)
}

// ToWikiPageName formats a URL back to corresponding wiki page name,
// and removes leading characters './' to prevent changing files
// that are not belong to wiki repository.
func ToWikiPageName(urlString string) string {
	name, _ := url.QueryUnescape(urlString)
	return strings.ReplaceAll(strings.TrimLeft(path.Clean("/"+name), "/"), "/", " ")
}

// WikiCloneLink returns clone URLs of repository wiki.
//
// Deprecated: Use repoutil.NewCloneLink instead.
func (r *Repository) WikiCloneLink() (cl *repoutil.CloneLink) {
	return r.cloneLink(true)
}

// WikiPath returns wiki data path by given user and repository name.
func WikiPath(userName, repoName string) string {
	return filepath.Join(repoutil.UserPath(userName), strings.ToLower(repoName)+".wiki.git")
}

func (r *Repository) WikiPath() string {
	return WikiPath(r.MustOwner().Name, r.Name)
}

// HasWiki returns true if repository has wiki.
func (r *Repository) HasWiki() bool {
	return com.IsDir(r.WikiPath())
}

// InitWiki initializes a wiki for repository,
// it does nothing when repository already has wiki.
func (r *Repository) InitWiki() error {
	if r.HasWiki() {
		return nil
	}

	if err := git.Init(r.WikiPath(), git.InitOptions{Bare: true}); err != nil {
		return fmt.Errorf("init repository: %v", err)
	} else if err = createDelegateHooks(r.WikiPath()); err != nil {
		return fmt.Errorf("createDelegateHooks: %v", err)
	}
	return nil
}

func (r *Repository) LocalWikiPath() string {
	return filepath.Join(conf.Server.AppDataPath, "tmp", "local-wiki", com.ToStr(r.ID))
}

// UpdateLocalWiki makes sure the local copy of repository wiki is up-to-date.
func (r *Repository) UpdateLocalWiki() error {
	return UpdateLocalCopyBranch(r.WikiPath(), r.LocalWikiPath(), "master", true)
}

func discardLocalWikiChanges(localPath string) error {
	return discardLocalRepoBranchChanges(localPath, "master")
}

// updateWikiPage adds new page to repository wiki.
func (r *Repository) updateWikiPage(doer *User, oldTitle, title, content, message string, isNew bool) (err error) {
	wikiWorkingPool.CheckIn(com.ToStr(r.ID))
	defer wikiWorkingPool.CheckOut(com.ToStr(r.ID))

	if err = r.InitWiki(); err != nil {
		return fmt.Errorf("InitWiki: %v", err)
	}

	localPath := r.LocalWikiPath()
	if err = discardLocalWikiChanges(localPath); err != nil {
		return fmt.Errorf("discardLocalWikiChanges: %v", err)
	} else if err = r.UpdateLocalWiki(); err != nil {
		return fmt.Errorf("UpdateLocalWiki: %v", err)
	}

	title = ToWikiPageName(title)
	filename := path.Join(localPath, title+".md")

	// If not a new file, show perform update not create.
	if isNew {
		if com.IsExist(filename) {
			return ErrWikiAlreadyExist{filename}
		}
	} else {
		os.Remove(path.Join(localPath, oldTitle+".md"))
	}

	// SECURITY: if new file is a symlink to non-exist critical file,
	// attack content can be written to the target file (e.g. authorized_keys2)
	// as a new page operation.
	// So we want to make sure the symlink is removed before write anything.
	// The new file we created will be in normal text format.
	os.Remove(filename)

	if err = os.WriteFile(filename, []byte(content), 0666); err != nil {
		return fmt.Errorf("WriteFile: %v", err)
	}

	if message == "" {
		message = "Update page '" + title + "'"
	}
	if err = git.Add(localPath, git.AddOptions{All: true}); err != nil {
		return fmt.Errorf("add all changes: %v", err)
	}

	err = git.CreateCommit(
		localPath,
		&git.Signature{
			Name:  doer.DisplayName(),
			Email: doer.Email,
			When:  time.Now(),
		},
		message,
	)
	if err != nil {
		return fmt.Errorf("commit changes: %v", err)
	} else if err = git.Push(localPath, "origin", "master"); err != nil {
		return fmt.Errorf("push: %v", err)
	}

	return nil
}

func (r *Repository) AddWikiPage(doer *User, title, content, message string) error {
	return r.updateWikiPage(doer, "", title, content, message, true)
}

func (r *Repository) EditWikiPage(doer *User, oldTitle, title, content, message string) error {
	return r.updateWikiPage(doer, oldTitle, title, content, message, false)
}

func (r *Repository) DeleteWikiPage(doer *User, title string) (err error) {
	wikiWorkingPool.CheckIn(com.ToStr(r.ID))
	defer wikiWorkingPool.CheckOut(com.ToStr(r.ID))

	localPath := r.LocalWikiPath()
	if err = discardLocalWikiChanges(localPath); err != nil {
		return fmt.Errorf("discardLocalWikiChanges: %v", err)
	} else if err = r.UpdateLocalWiki(); err != nil {
		return fmt.Errorf("UpdateLocalWiki: %v", err)
	}

	title = ToWikiPageName(title)
	filename := path.Join(localPath, title+".md")
	os.Remove(filename)

	message := "Delete page '" + title + "'"

	if err = git.Add(localPath, git.AddOptions{All: true}); err != nil {
		return fmt.Errorf("add all changes: %v", err)
	}

	err = git.CreateCommit(
		localPath,
		&git.Signature{
			Name:  doer.DisplayName(),
			Email: doer.Email,
			When:  time.Now(),
		},
		message,
	)
	if err != nil {
		return fmt.Errorf("commit changes: %v", err)
	} else if err = git.Push(localPath, "origin", "master"); err != nil {
		return fmt.Errorf("push: %v", err)
	}

	return nil
}
