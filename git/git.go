package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"k8s-git-operator/config"

	"github.com/go-git/go-git/v5"
	gitConfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

type GitClient struct {
	repo   *git.Repository
	auth   transport.AuthMethod
	branch string
	dir    string
	push   bool
}

func newHttpAuth(cfg *config.GitConfig) transport.AuthMethod {
	return &http.BasicAuth{
		Username: cfg.Username,
		Password: cfg.Password,
	}
}

func NewGitClient(ctx context.Context, cfg *config.GitConfig) (*GitClient, error) {
	var auth transport.AuthMethod
	var url string
	switch cfg.Protocol {
	case "https":
		auth = newHttpAuth(cfg)
		url = "https://" + cfg.RepositoryURL
	case "http":
		auth = newHttpAuth(cfg)
		url = "http://" + cfg.RepositoryURL
	case "ssh":
		pKey, err := ssh.NewPublicKeysFromFile("git", cfg.SSHPrivateKeyPath, cfg.Password)
		if err != nil {
			return nil, err
		}
		auth = pKey
		if strings.Contains(cfg.RepositoryURL, "@") {
			if user := strings.Split(cfg.RepositoryURL, "@")[0]; user != "" {
				cfg.Username = user
			}
		}
		url = cfg.RepositoryURL
	default:
		return nil, errors.New("unsupported protocol")
	}
	dir := cfg.RepositoryPath
	if strings.HasSuffix(dir, "/") {
		dir = dir + cfg.RepositoryFolder
	} else {
		dir = dir + "/" + cfg.RepositoryFolder
	}

	var repo *git.Repository

	push := true
	if cfg.DryRun {
		push = false
	}

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		repo, err = git.PlainCloneContext(ctx, dir, false, &git.CloneOptions{
			URL:           url,
			ReferenceName: plumbing.ReferenceName("refs/heads/" + cfg.Branch),
			Auth:          auth,
		})
		if err != nil {
			return nil, err
		}
	} else {
		repo, err = git.PlainOpen(dir)
		if err != nil {
			return nil, err
		}
	}

	return &GitClient{
		repo:   repo,
		auth:   auth,
		branch: cfg.Branch,
		dir:    dir,
		push:   push,
	}, nil
}

func (g *GitClient) CommitAndPush(ctx context.Context, message string) error {
	w, err := g.repo.Worktree()
	if err != nil {
		return err
	}

	// Adding all changes
	if _, err := w.Add("."); err != nil {
		return err
	}

	// Committing the changes
	if _, err := w.Commit(message, &git.CommitOptions{}); err != nil {
		return err
	}

	if !g.push {
		return nil
	}

	// Pushing the changes
	if err := g.repo.PushContext(ctx, &git.PushOptions{
		RemoteName: "origin",
		Auth:       g.auth,
		RefSpecs: []gitConfig.RefSpec{
			gitConfig.RefSpec("refs/heads/" + g.branch + ":refs/heads/" + g.branch),
		},
	}); err != nil && err != git.NoErrAlreadyUpToDate {
		return err
	}

	return nil
}

func (g *GitClient) SaveResource(ctx context.Context, path string, data []byte) error {
	fullPath := filepath.Join(g.dir, path)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		return err
	}
	return nil
}
