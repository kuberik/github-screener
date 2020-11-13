package controllers

import (
	"context"
	"fmt"
	"os"

	"github.com/google/go-github/v32/github"
	"github.com/gregjones/httpcache"
	"golang.org/x/oauth2"

	"strconv"
	"time"

	githubscreenersv1alpha1 "github.com/kuberik/github-screener/api/v1alpha1"
)

var (
	cache httpcache.Cache
)

func init() {
	cache = httpcache.NewMemoryCache()
}

// NewClient returns a new cacheable GitHub client
func NewClient() *github.Client {
	httpCacheClient := httpcache.NewTransport(cache).Client()

	ctx := context.WithValue(context.TODO(), oauth2.HTTPClient, httpCacheClient)
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: os.Getenv("GITHUB_TOKEN")},
	)
	tc := oauth2.NewClient(ctx, ts)

	return github.NewClient(tc)
}

type EventPoller struct {
	*github.Client
}

func NewEventPoller() EventPoller {
	return EventPoller{
		Client: NewClient(),
	}
}

func (p *EventPoller) PollOnce(repo githubscreenersv1alpha1.Repo) []github.Event {
	events, response, _ := p.Client.Activity.ListRepositoryEvents(context.TODO(), repo.Owner, repo.Name, &github.ListOptions{})
	for _, e := range events {
		fmt.Println(*e.Type)
	}
	pollInterval, err := strconv.Atoi(response.Header.Get("X-Poll-Interval"))
	if err != nil {
		pollInterval = 60
	}
	fmt.Println(response.Header)
	time.Sleep(time.Duration(pollInterval) * time.Second)
	return nil
}
