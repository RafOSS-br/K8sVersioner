package git

import (
	"context"
	"os"
	"path/filepath"

	"k8s-git-operator/config"

	"github.com/go-git/go-git/v5"
	gitConfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

type GitClient struct {
	repo   *git.Repository
	auth   *http.BasicAuth
	branch string
	dir    string
}

func NewGitClient(ctx context.Context, cfg *config.GitConfig) (*GitClient, error) {
	auth := &http.BasicAuth{
		Username: cfg.Username,
		Password: cfg.Password,
	}

	dir := "./repo" // Local directory to clone the repository
	var repo *git.Repository

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		repo, err = git.PlainCloneContext(ctx, dir, false, &git.CloneOptions{
			URL:           cfg.RepositoryURL,
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
