package operators

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/google/go-github/v32/github"
	"github.com/jarcoal/httpmock"
	corev1alpha1 "github.com/kuberik/github-screener/api/v1alpha1"
)

var (
	pushPollEventCollectorEventTemplate = struct {
		id        string
		ref       string
		repoName  string
		repoOwner string
		hash      string
	}{
		id:        "a",
		ref:       "foo",
		repoName:  "foo-repo",
		repoOwner: "foo-owner",
		hash:      "ab2dc7fcb96e5298446d02dbd22a09bb64af3218",
	}
	pushPollEventCollectorEventWant = corev1alpha1.Event{Spec: corev1alpha1.EventSpec{Data: map[string]string{
		eventIDKey:         pushPollEventCollectorEventTemplate.id,
		eventRefKey:        pushPollEventCollectorEventTemplate.ref,
		eventCommitHashKey: pushPollEventCollectorEventTemplate.hash,
	}}}
)

var pushPollEventCollectorTests = []struct {
	payload interface{}
	mocks   []struct {
		method    string
		url       string
		responder httpmock.Responder
	}
}{
	{
		payload: &github.PushEvent{
			Ref:  &pushPollEventCollectorEventTemplate.ref,
			Head: &pushPollEventCollectorEventTemplate.hash,
		},
	},
	{
		payload: &github.CreateEvent{
			Ref: &pushPollEventCollectorEventTemplate.ref,
			Repo: &github.Repository{
				Name: &pushPollEventCollectorEventTemplate.repoName,
				Owner: &github.User{
					Name: &pushPollEventCollectorEventTemplate.repoOwner,
				},
			},
		},
		mocks: []struct {
			method    string
			url       string
			responder httpmock.Responder
		}{{
			method: "GET",
			url: fmt.Sprintf(
				"https://api.github.com/repos/%s/%s/git/ref/%s",
				pushPollEventCollectorEventTemplate.repoOwner,
				pushPollEventCollectorEventTemplate.repoName,
				pushPollEventCollectorEventTemplate.ref,
			),
			responder: func(req *http.Request) (*http.Response, error) {
				body := github.Reference{Object: &github.GitObject{SHA: &pushPollEventCollectorEventTemplate.hash}}
				resp, _ := httpmock.NewJsonResponse(200, body)
				return resp, nil
			},
		}},
	},
}

func TestPushPollEventCollectorCollect(t *testing.T) {
	mockEvent := mockGithubEvent(pushPollEventCollectorEventTemplate.id)
	for i, testCase := range pushPollEventCollectorTests {
		httpmock.Activate()

		for _, m := range testCase.mocks {
			httpmock.RegisterResponder(m.method, m.url, m.responder)
		}

		client, _ := NewGithubClient()
		collector := pushPollEventCollector{client: client}
		e, err := collector.Collect(&mockEvent, testCase.payload)
		if err != nil {
			t.Errorf("testcase %d: failed to collect event: %s", i, err)
		} else if !reflect.DeepEqual(*e, pushPollEventCollectorEventWant) {
			t.Errorf("testcase %d: want %v Event after collect, but got %v", i, pushPollEventCollectorEventWant, e)
		}

		httpmock.DeactivateAndReset()
	}
}