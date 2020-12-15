package operators

import (
	"context"
	"regexp"

	"github.com/google/go-github/v32/github"
	corev1alpha1 "github.com/kuberik/github-screener/api/v1alpha1"
	"github.com/m4ns0ur/httpcache"
	"golang.org/x/oauth2"

	"strconv"
)

var (
	cache httpcache.Cache
)

func init() {
	cache = httpcache.NewMemoryCache()
}

// NewGithubClient returns a new cacheable GitHub client
func NewGithubClient() (*github.Client, *oauth2.Token) {
	httpCacheClient := httpcache.NewTransport(cache).Client()

	ctx := context.WithValue(context.TODO(), oauth2.HTTPClient, httpCacheClient)
	oauthToken := &oauth2.Token{AccessToken: ""}
	ts := oauth2.StaticTokenSource(oauthToken)
	tc := oauth2.NewClient(ctx, ts)

	return github.NewClient(tc), oauthToken
}

type PollEventCollector interface {
	Collect(*github.Event, interface{}) (*corev1alpha1.Event, error)
}

type defaultPollEventCollector struct{}

func (_ defaultPollEventCollector) Collect(e *github.Event, payload interface{}) (*corev1alpha1.Event, error) {
	ke := &corev1alpha1.Event{}
	ke.Spec.Data = map[string]string{
		eventIDKey: *e.ID,
	}
	return ke, nil
}

var DefaultPollEventCollector PollEventCollector = defaultPollEventCollector{}

type EventPoller struct {
	*github.Client
	Repo       Repo
	Token      *oauth2.Token
	checkpoint string
	PollEventCollector
}

func NewEventPoller() EventPoller {
	client, oauthToken := NewGithubClient()
	return EventPoller{
		PollEventCollector: DefaultPollEventCollector,
		Client:             client,
		Token:              oauthToken,
	}
}

type EventPollResult struct {
	Events       []corev1alpha1.Event
	ETag         ETag
	PollInterval int
}

func (p *EventPoller) PollOnce() (*EventPollResult, error) {
	events, response, err := p.Client.Activity.ListRepositoryEvents(context.TODO(), p.Repo.Owner, p.Repo.Name, &github.ListOptions{})
	if err != nil {
		return nil, err
	}

	checkpointIndex := len(events)
	for i, e := range events {
		if *e.ID == p.checkpoint {
			checkpointIndex = i
			break
		}
	}

	var collectedEvents []corev1alpha1.Event
	for i := checkpointIndex - 1; i >= 0; i-- {
		payload, err := events[i].ParsePayload()
		if err != nil {
			return nil, err
		}
		e, err := p.Collect(events[i], payload)
		if err != nil {
			break
		} else if e != nil {
			collectedEvents = append(collectedEvents, *e)
			p.checkpoint = *events[i].ID
		}
	}

	pollInterval, err := strconv.Atoi(response.Header.Get("X-Poll-Interval"))
	if err != nil {
		pollInterval = 60
	}

	return &EventPollResult{
		Events: collectedEvents,
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
