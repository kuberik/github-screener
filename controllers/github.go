package controllers

import (
	"context"
	"os"
	"regexp"

	"github.com/google/go-github/v32/github"
	"github.com/gregjones/httpcache"
	"golang.org/x/oauth2"

	"strconv"

	corev1alpha1 "github.com/kuberik/github-screener/api/v1alpha1"
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

type EventPollResult struct {
	Events       []*github.Event
	ETag         string
	PollInterval int
}

func (p *EventPoller) PollOnce(repo corev1alpha1.Repo) EventPollResult {
	events, response, _ := p.Client.Activity.ListRepositoryEvents(context.TODO(), repo.Owner, repo.Name, &github.ListOptions{})
	pollInterval, err := strconv.Atoi(response.Header.Get("X-Poll-Interval"))
	if err != nil {
		pollInterval = 60
	}
	return EventPollResult{
		Events: events,
		// returns only first 63 characters of etag, since no more can fit in a label
		ETag:         regexp.MustCompile("\\w{63}").FindString(response.Header.Get("ETag")),
		PollInterval: pollInterval,
	}
}

// Repo defines an unique GitHub repo
type Repo struct {
	Name  string `json:"name"`
	Owner string `json:"owner"`
}
