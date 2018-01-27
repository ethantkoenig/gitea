// Copyright 2017 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package integrations

import (
	"context"
	"crypto/rand"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"code.gitea.io/git"
	"code.gitea.io/gitea/modules/setting"
	api "code.gitea.io/sdk/gitea"

	"github.com/Unknwon/com"
	"github.com/stretchr/testify/assert"
)

const (
	littleSize = 1024              //1ko
	bigSize    = 128 * 1024 * 1024 //128Mo
)

func onGiteaRun(t *testing.T, callback func(*testing.T, *url.URL)) {
	prepareTestEnv(t)
	s := http.Server{
		Handler: mac,
	}

	u, err := url.Parse(setting.AppURL)
	assert.NoError(t, err)
	listener, err := net.Listen("tcp", u.Host)
	assert.NoError(t, err)

	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		s.Shutdown(ctx)
		cancel()
	}()

	go s.Serve(listener)
	//Started by config go ssh.Listen(setting.SSH.ListenHost, setting.SSH.ListenPort, setting.SSH.ServerCiphers, setting.SSH.ServerKeyExchanges, setting.SSH.ServerMACs)

	callback(t, u)
}

func TestGit(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, u *url.URL) {
		u.Path = "user2/repo1.git"

		t.Run("HTTP", func(t *testing.T) {
			fmt.Printf("Test: HTTP\n")
			dstPath, err := ioutil.TempDir("", "repo-tmp-17")
			assert.NoError(t, err)
			defer os.RemoveAll(dstPath)
			t.Run("Standard", func(t *testing.T) {
				fmt.Printf("Test: HTTP.Standard\n")
				t.Run("CloneNoLogin", func(t *testing.T) {
					dstLocalPath, err := ioutil.TempDir("", "repo1")
					assert.NoError(t, err)
					defer os.RemoveAll(dstLocalPath)
					err = git.Clone(u.String(), dstLocalPath, git.CloneRepoOptions{})
					assert.NoError(t, err)
					assert.True(t, com.IsExist(filepath.Join(dstLocalPath, "README.md")))
				})

				t.Run("CreateRepo", func(t *testing.T) {
					session := loginUser(t, "user2")
					req := NewRequestWithJSON(t, "POST", "/api/v1/user/repos", &api.CreateRepoOption{
						AutoInit:    true,
						Description: "Temporary repo",
						Name:        "repo-tmp-17",
						Private:     false,
						Gitignores:  "",
						License:     "WTFPL",
						Readme:      "Default",
					})
					session.MakeRequest(t, req, http.StatusCreated)
				})

				u.Path = "user2/repo-tmp-17.git"
				u.User = url.UserPassword("user2", userPassword)
				t.Run("Clone", func(t *testing.T) {
					err = git.Clone(u.String(), dstPath, git.CloneRepoOptions{})
					assert.NoError(t, err)
					assert.True(t, com.IsExist(filepath.Join(dstPath, "README.md")))
				})

				t.Run("PushCommit", func(t *testing.T) {
					t.Run("Little", func(t *testing.T) {
						commitAndPush(t, littleSize, dstPath)
					})
					t.Run("Big", func(t *testing.T) {
						commitAndPush(t, bigSize, dstPath)
					})
				})
			})
			t.Run("LFS", func(t *testing.T) {
				fmt.Printf("Test: HTTP.LFS\n")
				t.Run("PushCommit", func(t *testing.T) {
					//Setup git LFS
					_, err = git.NewCommand("lfs").AddArguments("install").RunInDir(dstPath)
					assert.NoError(t, err)
					_, err = git.NewCommand("lfs").AddArguments("track", "data-file-*").RunInDir(dstPath)
					assert.NoError(t, err)
					err = git.AddChanges(dstPath, false, ".gitattributes")
					assert.NoError(t, err)

					t.Run("Little", func(t *testing.T) {
						commitAndPush(t, littleSize, dstPath)
					})
					t.Run("Big", func(t *testing.T) {
						commitAndPush(t, bigSize, dstPath)
					})
				})
				t.Run("Locks", func(t *testing.T) {
					lockTest(t, u.String(), dstPath)
				})
			})
		})
	})
}

func lockTest(t *testing.T, remote, repoPath string) {
	_, err := git.NewCommand("remote").AddArguments("set-url", "origin", remote).RunInDir(repoPath) //TODO add test ssh git-lfs-creds
	assert.NoError(t, err)
	_, err = git.NewCommand("lfs").AddArguments("locks").RunInDir(repoPath)
	assert.NoError(t, err)
	_, err = git.NewCommand("lfs").AddArguments("lock", "README.md").RunInDir(repoPath)
	assert.NoError(t, err)
	_, err = git.NewCommand("lfs").AddArguments("locks").RunInDir(repoPath)
	assert.NoError(t, err)
	_, err = git.NewCommand("lfs").AddArguments("unlock", "README.md").RunInDir(repoPath)
	assert.NoError(t, err)
}

func commitAndPush(t *testing.T, size int, repoPath string) {
	fmt.Printf("commitAndPush(%s): starting\n", repoPath)
	err := generateCommitWithNewData(size, repoPath, "user2@example.com", "User Two")
	assert.NoError(t, err)
	fmt.Printf("commitAndPush(%s): about to push\n", repoPath)
	_, err = git.NewCommand("push").RunInDir(repoPath) //Push
	fmt.Printf("commitAndPush(%s): done pushing\n", repoPath)
	assert.NoError(t, err)
}

func generateCommitWithNewData(size int, repoPath, email, fullName string) error {
	//Generate random file
	data := make([]byte, size)
	_, err := rand.Read(data)
	if err != nil {
		return err
	}
	tmpFile, err := ioutil.TempFile(repoPath, "data-file-")
	if err != nil {
		return err
	}
	defer tmpFile.Close()
	_, err = tmpFile.Write(data)
	if err != nil {
		return err
	}

	//Commit
	err = git.AddChanges(repoPath, false, filepath.Base(tmpFile.Name()))
	if err != nil {
		return err
	}
	err = git.CommitChanges(repoPath, git.CommitChangesOptions{
		Committer: &git.Signature{
			Email: email,
			Name:  fullName,
			When:  time.Now(),
		},
		Author: &git.Signature{
			Email: email,
			Name:  fullName,
			When:  time.Now(),
		},
		Message: fmt.Sprintf("Testing commit @ %v", time.Now()),
	})
	return err
}
