//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type lumiviaReleaseClient struct {
	updateServiceGitHubClientStub
	repositories []string
}

func (c *lumiviaReleaseClient) FetchLatestRelease(ctx context.Context, repo string) (*GitHubRelease, error) {
	c.repositories = append(c.repositories, repo)
	return c.updateServiceGitHubClientStub.FetchLatestRelease(ctx, repo)
}

func (c *lumiviaReleaseClient) FetchRecentReleases(ctx context.Context, repo string, limit int) ([]*GitHubRelease, error) {
	c.repositories = append(c.repositories, repo)
	return c.updateServiceGitHubClientStub.FetchRecentReleases(ctx, repo, limit)
}

func TestLumiviaUpdatesUseFork(t *testing.T) {
	client := &lumiviaReleaseClient{updateServiceGitHubClientStub: updateServiceGitHubClientStub{
		release: &GitHubRelease{TagName: "v0.2.1", Name: "v0.2.1"},
	}}
	svc := NewUpdateService(&updateServiceCacheStub{}, client, "0.2.1", "release")
	_, err := svc.CheckUpdate(context.Background(), true)
	require.NoError(t, err)
	_, err = svc.ListRollbackVersions(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"goldenfishs/sub2api", "goldenfishs/sub2api"}, client.repositories)
}
