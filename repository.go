// repository.go
package git_backup

import (
	"errors"
	"log"
	"net/url"
	"os"
	"runtime"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

type RepositorySource interface {
	GetName() string
	Test() error
	ListRepositories() ([]*Repository, error)
}

type Repository struct {
	GitURL   url.URL
	FullName string
}

func isBare(repo *git.Repository) (bool, error) {
	config, err := repo.Config()
	if err != nil {
		return false, err
	}

	return config.Core.IsBare, nil
}

// logMemoryUsage logs current memory usage
func logMemoryUsage(operation string) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	log.Printf("%s - Memory: Alloc=%d KB, TotalAlloc=%d KB, Sys=%d KB, NumGC=%d",
		operation,
		bToKb(m.Alloc),
		bToKb(m.TotalAlloc),
		bToKb(m.Sys),
		m.NumGC)
}

func bToKb(b uint64) uint64 {
	return b / 1024
}

// forceGC forces garbage collection and logs memory usage
func forceGC(operation string) {
	runtime.GC()
	logMemoryUsage(operation)
}

// fetchAllRefs fetches all branches and tags from the remote repository
func (r *Repository) fetchAllRefs(gitRepo *git.Repository, auth http.AuthMethod) error {
	log.Printf("Fetching all branches and tags for %s", r.FullName)
	logMemoryUsage("Before fetch for " + r.FullName)

	// For large repositories, fetch branches and tags separately to avoid memory issues
	// First, fetch all branches
	err := gitRepo.Fetch(&git.FetchOptions{
		Auth:     auth,
		Progress: os.Stdout,
		RefSpecs: []config.RefSpec{
			"+refs/heads/*:refs/heads/*", // All branches
		},
		Force: true,
	})

	logMemoryUsage("After branch fetch for " + r.FullName)

	if err != nil && err != git.NoErrAlreadyUpToDate {
		log.Printf("Branch fetch failed for %s: %v", r.FullName, err)
	}

	// Force garbage collection between operations
	runtime.GC()

	// Then fetch all tags separately
	err = gitRepo.Fetch(&git.FetchOptions{
		Auth:     auth,
		Progress: os.Stdout,
		Tags:     git.AllTags,
		Force:    true,
	})

	logMemoryUsage("After tag fetch for " + r.FullName)

	if err != nil && err != git.NoErrAlreadyUpToDate {
		log.Printf("Tag fetch failed for %s: %v", r.FullName, err)
		// Try with RefSpec as fallback
		err = gitRepo.Fetch(&git.FetchOptions{
			Auth:     auth,
			Progress: os.Stdout,
			RefSpecs: []config.RefSpec{
				"+refs/tags/*:refs/tags/*",
			},
			Force: true,
		})
	}

	return err
}

func (r *Repository) CloneInto(path string, bare bool) error {
	logMemoryUsage("Starting clone for " + r.FullName)

	var auth http.AuthMethod
	if r.GitURL.User != nil {
		password, _ := r.GitURL.User.Password()
		auth = &http.BasicAuth{
			Username: r.GitURL.User.Username(),
			Password: password,
		}
	}

	// Modified: Use Mirror instead of just bare to get all branches and tags
	log.Printf("Starting mirror clone for %s", r.FullName)
	gitRepo, err := git.PlainClone(path, bare, &git.CloneOptions{
		URL:      r.GitURL.String(),
		Auth:     auth,
		Progress: os.Stdout,
		Mirror:   true,  // Add this line to clone as mirror
	})

	logMemoryUsage("After clone attempt for " + r.FullName)

	if errors.Is(err, git.ErrRepositoryAlreadyExists) {
		// Pull instead of clone
		if gitRepo, err = git.PlainOpen(path); err == nil {
			// we need to check whether it's a bare repo or not.
			// if not we should pull, if it is then pull won't work
			if isBare, bErr := isBare(gitRepo); bErr == nil && !isBare {
				if w, wErr := gitRepo.Worktree(); wErr != nil {
					err = wErr
				} else {
					err = w.Pull(&git.PullOptions{
						Auth:     auth,
						Progress: os.Stdout,
					})
				}
			} else {
				// For mirror/bare repositories, we need to fetch all refs
				err = r.fetchAllRefs(gitRepo, auth)
			}
		}
	}

	switch {
	case errors.Is(err, transport.ErrEmptyRemoteRepository):
		log.Printf("%s is an empty repository", r.FullName)
		//  Empty repo does not need backup
		return nil
	default:
		return err
	case errors.Is(err, git.NoErrAlreadyUpToDate):
		log.Printf("No need to pull, %s is already up-to-date", r.FullName)
		// Already up to date on current branch, still need to refresh other branches
		fallthrough
	case err == nil:
		// No errors, continue - fetch all branches and tags
		log.Printf("Fetching all branches and tags for %s", r.FullName)
		err = r.fetchAllRefs(gitRepo, auth)
		forceGC("After fetch for " + r.FullName)
	}

	switch err {
	case git.NoErrAlreadyUpToDate:
		log.Printf("All refs up-to-date for %s", r.FullName)
		return nil
	default:
		return err
	}
}