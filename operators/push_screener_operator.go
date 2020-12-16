package operators

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-github/v32/github"
	corev1alpha1 "github.com/kuberik/github-screener/api/v1alpha1"
	"github.com/kuberik/github-screener/screener/controllers"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	eventIDKey         = "GITHUB_EVENT_ID"
	eventRefKey        = "GITHUB_REF"
	eventCommitHashKey = "GITHUB_COMMIT_HASH"

	etagLabel = "github.screeners.kuberik.io/etag"
)

type pushPollEventCollector struct {
	client *github.Client
}

func (c pushPollEventCollector) Collect(e *github.Event, payload interface{}) (*corev1alpha1.Event, error) {
	ke, _ := DefaultPollEventCollector.Collect(e, payload)

	switch p := payload.(type) {
	case *github.PushEvent:
		ke.Spec.Data[eventRefKey] = p.GetRef()
		ke.Spec.Data[eventCommitHashKey] = *p.Head
	case *github.CreateEvent:
		fullRepoNameSplit := strings.Split(*e.Repo.Name, "/")
		repo := Repo{Owner: fullRepoNameSplit[0], Name: fullRepoNameSplit[1]}
		branch, _, err := c.client.Repositories.GetBranch(context.TODO(), repo.Owner, repo.Name, p.GetRef())
		if err != nil {
			return nil, err
		}
		ke.Spec.Data[eventRefKey] = fmt.Sprintf("refs/head/%s", p.GetRef())
		ke.Spec.Data[eventCommitHashKey] = *branch.Commit.SHA
	default:
		return nil, nil
	}

	return ke, nil
}

type PushEventScreenerOperator struct {
	EventPoller
	client.Client
	Log logr.Logger
}

func NewPushEventScreenerOperator(client client.Client, log logr.Logger) controllers.ScreenerOperator {
	poller := NewEventPoller()
	poller.log = log
	poller.PollEventCollector = pushPollEventCollector{
		client: poller.Client,
	}
	return &PushEventScreenerOperator{
		EventPoller: poller,
		Client:      client,
		Log:         log,
	}
}

type PushScreenerConfig struct {
	Repo        `json:"repo"`
	TokenSecret string `json:"tokenSecret"`
}

func (sc *PushEventScreenerOperator) Update(screener corev1alpha1.Screener) error {
	config := &PushScreenerConfig{}
	controllers.ParseScreenerConfig(screener, config)
	sc.Repo = config.Repo
	sc.Start = screener.CreationTimestamp.Time
	if config.TokenSecret != "" {
		secret := &v1.Secret{}
		err := sc.Get(context.TODO(), types.NamespacedName{Name: config.TokenSecret, Namespace: screener.Namespace}, secret)

		if err != nil {
			// Skip setting up authentication for now. The function will be called again on 403
			// reqLogger.Error(err, "failed to fetch token secret")
			return err
		}

		tokenKey := "token"
		if _, ok := secret.Data[tokenKey]; ok {
			sc.Token.AccessToken = string(secret.Data[tokenKey])
		}
	}
	return nil
}

func (sc *PushEventScreenerOperator) Screen(eventCreate chan corev1alpha1.Event, stop chan bool) error {
	for {
		select {
		case _ = <-stop:
			close(eventCreate)
			sc.Log.Info("stopping")
			return nil
		default:
			sc.Log.Info("polling")
			result, err := sc.PollOnce()
			// TODO: what to do on error?
			if err != nil {
				sc.Log.Error(err, "poll error")
				continue
			}
			for _, e := range result.Events {
				eventCreate <- e
			}
			time.Sleep(time.Duration(result.PollInterval) * time.Second)
		}
	}
}

func (sc *PushEventScreenerOperator) Recover(event corev1alpha1.Event) error {
	sc.checkpoint = event.Spec.Data[eventIDKey]
	return nil
}
