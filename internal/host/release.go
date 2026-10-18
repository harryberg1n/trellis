package host

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// downloadTimeout bounds `gh release download`, which moves a whole binary.
const downloadTimeout = 5 * time.Minute

// LatestRelease is the tag of the newest published release of repo.
func (g GH) LatestRelease(repo string) (string, error) {
	out, err := g.gh(nil, "api", "repos/"+repo+"/releases/latest")
	if err != nil {
		return "", err
	}
	var rel struct {
		Tag string `json:"tag_name"`
	}
	if err := json.Unmarshal(out, &rel); err != nil {
		return "", fmt.Errorf("host: reading gh api releases/latest: %w", err)
	}
	if rel.Tag == "" {
		return "", errors.New("host: the latest release of " + repo + " names no tag")
	}
	return rel.Tag, nil
}

// DownloadRelease downloads the assets of release tag whose names are patterns
// into dir, replacing a file already there.
func (g GH) DownloadRelease(repo, tag, dir string, patterns ...string) error {
	args := append(make([]string, 0, 8+2*len(patterns)), "release", "download", tag, "--repo", repo, "--dir", dir, "--clobber")
	for _, p := range patterns {
		args = append(args, "--pattern", p)
	}
	_, err := g.ghWithin(downloadTimeout, nil, args...)
	return err
}
