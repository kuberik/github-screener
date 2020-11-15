package controllers

import (
	"context"
	"os"
	"regexp"

	"github.com/google/go-github/v32/github"
	"github.com/gregjones/httpcache"
	"golang.org/x/oauth2"

	"strconv"
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
	Repo Repo
}

func NewEventPoller(repo Repo) EventPoller {
	return EventPoller{
		Client: NewClient(),
		Repo:   repo,
	}
}

type EventPollResult struct {
	Events       []*github.Event
	ETag         ETag
	PollInterval int
}

func (p *EventPoller) PollOnce() (*EventPollResult, error) {
	events, response, err := p.Client.Activity.ListRepositoryEvents(context.TODO(), p.Repo.Owner, p.Repo.Name, &github.ListOptions{})
	if err != nil {
		return nil, err
	}
	pollInterval, err := strconv.Atoi(response.Header.Get("X-Poll-Interval"))
	if err != nil {
		pollInterval = 60
	}
	return &EventPollResult{
		Events: events,
		// returns only first 63 characters of etag, since no more can fit in a label
		ETag:         ETag(regexp.MustCompile("\\w{2,}").FindString(response.Header.Get("ETag"))),
		PollInterval: pollInterval,
	}, nil
}

type ETag string

func (e ETag) String() string {
	kubernetesMaxLabelLen := 63
	if len(e) > kubernetesMaxLabelLen {
		return string(e[0 : kubernetesMaxLabelLen-1])
	}
	return string(e)
}

// Repo defines an unique GitHub repo
type Repo struct {
	Name  string `json:"name"`
	Owner string `json:"owner"`
}
