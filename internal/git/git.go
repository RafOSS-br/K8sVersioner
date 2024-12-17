package git

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/RafOSS-br/K8sVersioner/api/v1alpha1"
	"github.com/RafOSS-br/K8sVersioner/internal/store"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// GitClient represents a Git client with repository information and internal state
type GitClient struct {
	repo     *git.Repository
	worktree *git.Worktree
	auth     transport.AuthMethod
	branch   string
	dir      string
	push     bool
	mu       sync.Mutex
	sig      v1alpha1.Signature
}

// newHTTPAuth creates an HTTP authentication method
func newHTTPAuth(cfg *store.Bundle) transport.AuthMethod {
	return &http.BasicAuth{
		Username: cfg.Git.GitConfig.Spec.Username,
		Password: cfg.Git.GitConfig.Spec.Password,
	}
}

// NewGitClient creates a new Git client based on the provided configuration
func NewGitClient(ctx context.Context, cfg *store.Bundle) (*GitClient, error) {
	var (
		auth transport.AuthMethod
		url  string
		err  error
	)

	logger := log.FromContext(ctx)
	repositoryURL := cfg.Git.GitConfig.Spec.RepositoryURL

	// Configure authentication and URL based on the protocol
	switch cfg.Git.GitConfig.Spec.Protocol {
	case "https":
		auth = newHTTPAuth(cfg)
		repositoryURL = strings.TrimPrefix(repositoryURL, "https://")
		url = "https://" + repositoryURL
	case "http":
		auth = newHTTPAuth(cfg)
		repositoryURL = strings.TrimPrefix(repositoryURL, "http://")
		url = "http://" + repositoryURL
	case "ssh":
		repositoryURL = strings.TrimPrefix(repositoryURL, "ssh://")
		pKey, err := ssh.NewPublicKeysFromFile("git", cfg.Git.GitConfig.Spec.SSHPrivateKeyPath, cfg.Git.GitConfig.Spec.Password)
		if err != nil {
			return nil, err
		}
		auth = pKey
		url = repositoryURL
	default:
		return nil, errors.New("unsupported protocol")
	}

	// Create a hash for the local directory
	h := md5.New()
	h.Write([]byte(cfg.Git.GitConfig.Spec.RepositoryURL + cfg.Git.GitConfig.Spec.Branch))
	dirHash := hex.EncodeToString(h.Sum(nil))
	dir := filepath.Join(cfg.Git.GitConfig.Spec.RepositoryBasePath, dirHash)

	var repo *git.Repository
	push := !cfg.Git.GitConfig.Spec.DryRun

	// Check if the base path exists
	err = os.MkdirAll(dir, os.ModePerm)
	if err != nil {
		logger.Error(err, "Error creating base path", "path", cfg.Git.GitConfig.Spec.RepositoryBasePath)
		return nil, err
	}

	// Try to open the local repository; if it fails, try to clone it
	repo, err = git.PlainOpen(dir)
	if err != nil {
		logger.Info("Repository not found locally, attempting to clone", "url", url, "branch", cfg.Git.GitConfig.Spec.Branch)
		repo, err = git.PlainCloneContext(ctx, dir, false, &git.CloneOptions{
			URL:           url,
			ReferenceName: plumbing.ReferenceName("refs/heads/" + cfg.Git.GitConfig.Spec.Branch),
			Auth:          auth,
			SingleBranch:  true,
			Depth:         1,
		})
		if err != nil {
			// Check if the error is due to a non-existent branch or uninitialized repository
			logger.Info("Repository not found, initializing new local repository", "url", url, "branch", cfg.Git.GitConfig.Spec.Branch, "error", err)

			// Initialize a new local repository
			repo, err = git.PlainInit(dir, false)
			if err != nil {
				logger.Error(err, "Error initializing new local repository")
				return nil, err
			}

			worktree, err := repo.Worktree()
			if err != nil {
				logger.Error(err, "Error getting worktree after initializing repository")
				return nil, err
			}

			// Create a README file for the initial commit
			readmePath := filepath.Join(dir, "README.md")
			err = os.WriteFile(readmePath, []byte("# Repository Initialization"), 0644)
			if err != nil {
				logger.Error(err, "Error creating README.md for initial commit")
				return nil, err
			}

			// Add the file to the index
			_, err = worktree.Add("README.md")
			if err != nil {
				logger.Error(err, "Error adding README.md to worktree")
				return nil, err
			}

			// Perform the initial commit
			commitMsg := "Initial commit"
			_, err = worktree.Commit(commitMsg, &git.CommitOptions{
				Author: &object.Signature{
					Name:  "Auto Commit K8sVersioner",
					Email: "auto@k8sversioner.app",
					When:  time.Now(),
				},
			})
			if err != nil {
				logger.Error(err, "Error performing initial commit")
				return nil, err
			}

			// Set the remote origin
			_, err = repo.CreateRemote(&config.RemoteConfig{
				Name: "origin",
				URLs: []string{url},
			})
			if err != nil {
				logger.Error(err, "Error setting remote origin")
				return nil, err
			}

			// Create and checkout the desired branch
			branchRef := plumbing.ReferenceName("refs/heads/" + cfg.Git.GitConfig.Spec.Branch)
			err = worktree.Checkout(&git.CheckoutOptions{
				Branch: branchRef,
				Create: true,
				Hash:   plumbing.ZeroHash,
			})
			if err != nil {
				logger.Error(err, "Error creating and checking out new branch", "branch", cfg.Git.GitConfig.Spec.Branch)
				return nil, err
			}

			logger.Info("Initialized new local repository and created branch", "branch", cfg.Git.GitConfig.Spec.Branch)
		}
	}

	// Get the worktree
	worktree, err := repo.Worktree()
	if err != nil {
		logger.Error(err, "Error getting worktree")
		return nil, err
	}

	return &GitClient{
		repo:     repo,
		worktree: worktree,
		auth:     auth,
		branch:   cfg.Git.GitConfig.Spec.Branch,
		dir:      dir,
		push:     push,
		sig:      cfg.Git.GitConfig.Spec.Signature,
	}, nil
}

func (g *GitClient) Reclone(ctx context.Context) error {
	remotes, err := g.repo.Remotes()
	if err != nil || len(remotes) == 0 {
		fmt.Println(g.repo.Remotes())
		return errors.New("no remotes found")
	}
	_ = os.RemoveAll(g.dir)
	repo, err := git.PlainCloneContext(ctx, g.dir, false, &git.CloneOptions{
		URL:           remotes[0].Config().URLs[0],
		ReferenceName: plumbing.ReferenceName("refs/heads/" + g.branch),
		Auth:          g.auth,
		SingleBranch:  true,
		Depth:         1,
	})
	if err != nil {
		return err
	}
	worktree, err := repo.Worktree()
	if err != nil {
		return err
	}
	g.repo = repo
	g.worktree = worktree
	return nil
}

// ErrAlreadyUpToDate is returned when there are no changes to commit
var ErrAlreadyUpToDate = errors.New("already up to date")

// ErrObjectNotFound is returned when the object is not found in the repository
var ErrObjectNotFound = errors.New("object not found")

// CommitAndPush creates a commit and pushes changes to the remote repository
func (g *GitClient) CommitAndPush(ctx context.Context, message string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	status, err := g.worktree.Status()
	if err != nil {
		return err
	}

	if status.IsClean() {
		return ErrAlreadyUpToDate
	}

	_, err = g.worktree.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  g.sig.Name,
			Email: g.sig.Email,
			When:  time.Now(),
		},
	})
	if err != nil {
		return err
	}

	if g.push {
		err = g.repo.PushContext(ctx, &git.PushOptions{
			RefSpecs: []config.RefSpec{
				config.RefSpec("refs/heads/" + g.branch + ":refs/heads/" + g.branch),
			},
			RemoteName: "origin",
			Auth:       g.auth,
		})
		if err != nil && err != git.NoErrAlreadyUpToDate {
			if err == plumbing.ErrObjectNotFound {
				return ErrObjectNotFound
			}
			return err
		}
	}

	return nil
}

// SaveResource saves a resource to the repository
func (g *GitClient) SaveResource(ctx context.Context, path string, data []byte) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	fullPath := filepath.Join(g.dir, path)
	err := os.MkdirAll(filepath.Dir(fullPath), os.ModePerm)
	if err != nil {
		return err
	}

	err = os.WriteFile(fullPath, data, 0644)
	if err != nil {
		return err
	}

	_, err = g.worktree.Add(path)
	if err != nil {
		return err
	}

	return nil
}

// RemoveResource removes a resource from the repository
func (g *GitClient) RemoveResource(ctx context.Context, path string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	fullPath := filepath.Join(g.dir, path)
	err := os.Remove(fullPath)
	if err != nil {
		return err
	}

	_, err = g.worktree.Remove(path)
	if err != nil {
		return err
	}

	return nil
}
