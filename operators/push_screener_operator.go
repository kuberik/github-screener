package operators

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-github/v32/github"
	corev1alpha1 "github.com/kuberik/github-screener/api/v1alpha1"
	"github.com/kuberik/github-screener/screener/controllers"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type PushEventScreenerOperator struct {
	poller EventPoller
	client.Client
	Log logr.Logger
}

func NewPushEventScreenerOperator(client client.Client, log logr.Logger) controllers.ScreenerOperator {
	return &PushEventScreenerOperator{
		poller: NewEventPoller(),
		Client: client,
		Log:    log,
	}
}

type PushScreenerConfig struct {
	Repo        `json:"repo"`
	TokenSecret string `json:"tokenSecret"`
}

func (sc *PushEventScreenerOperator) Update(screener corev1alpha1.Screener) error {
	config := &PushScreenerConfig{}
	controllers.ParseScreenerConfig(screener, config)
	sc.poller.Repo = config.Repo
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
			sc.poller.Token.AccessToken = string(secret.Data[tokenKey])
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
			result, err := sc.poller.PollOnce()
			// TODO what to do on error?
			if err != nil {
				sc.Log.Error(err, "poll error")
				return err
			}
			results := sc.processPollResult(*result)
			for i := range results {
				// Create oldest events first
				eventCreate <- results[len(results)-i-1]
			}
			time.Sleep(time.Duration(result.PollInterval) * time.Second)
		}
	}
}

const (
	eventIDKey         = "GITHUB_EVENT_ID"
	eventRefKey        = "GITHUB_REF"
	eventCommitHashKey = "GITHUB_COMMIT_HASH"

	etagLabel = "github.screeners.kuberik.io/etag"
)

func (sc *PushEventScreenerOperator) processPollResult(result EventPollResult) (createEvents []corev1alpha1.Event) {
	for _, e := range result.Events {
		payload, err := e.ParsePayload()
		if err != nil {
			sc.Log.Error(err, fmt.Sprintf("Failed to parse an event %s/%s#%s", e.GetRepo().GetOwner(), e.GetRepo().GetName(), e.GetID()))
			continue
		}

		pushEvent, ok := payload.(*github.PushEvent)
		if !ok {
			sc.Log.Info("Skipping event", "type", e.GetType())
			continue
		}

		ke := corev1alpha1.Event{}
		ke.Spec.Data = map[string]string{
			eventIDKey:         *e.ID,
			eventRefKey:        pushEvent.GetRef(),
			eventCommitHashKey: *pushEvent.Head,
		}
		// TODO remove if not needed
		ke.Labels = map[string]string{
			etagLabel: result.ETag.String(),
		}
		createEvents = append(createEvents, ke)
	}
	return
}

func (sc *PushEventScreenerOperator) Recover(event corev1alpha1.Event) error {
	sc.poller.checkpoint = event.Spec.Data[eventIDKey]
	return nil
}
