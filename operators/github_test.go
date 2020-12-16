package operators

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/go-github/v32/github"
	"github.com/jarcoal/httpmock"
)

var (
	mockEventType                      = "PushEvent"
	mockEventPayload   json.RawMessage = []byte("{}")
	mockEventCreatedAt                 = time.Now()
)

func mockGithubEvent(id string) github.Event {
	return github.Event{
		ID:         &id,
		Type:       &mockEventType,
		RawPayload: &mockEventPayload,
		CreatedAt:  &mockEventCreatedAt,
	}
}

func TestEventPollerPollOnce(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	repo := Repo{
		Owner: "kuberik",
		Name:  "foo",
	}

	mockEvents := []github.Event{}
	etag := "5f36d139db088e04db015fd8232e28da5679ff4a03add6d5be8532ccbe1db928"

	httpmock.RegisterResponder(
		"GET",
		fmt.Sprintf("https://api.github.com/repos/%s/%s/events", repo.Owner, repo.Name),
		func(req *http.Request) (*http.Response, error) {
			resp, _ := httpmock.NewJsonResponse(200, mockEvents)
			resp.Header.Set("etag", fmt.Sprintf(`W/"%s"`, etag))
			return resp, nil
		},
	)

	eventPoller := NewEventPoller()
	eventPoller.Repo = repo
	pollResult, err := eventPoller.PollOnce()
	if err != nil {
		t.Fatalf("Poll resulted in an error: %s", err)
	}
	if len(pollResult.Events) != len(mockEvents) {
		t.Errorf("Want %d events on poll, but got %d", len(mockEvents), len(pollResult.Events))
	}
	if string(pollResult.ETag) != etag {
		t.Errorf("Want etag %s, got %s", etag, pollResult.ETag)
	}

	// get count info
	wantTotalCount := 1
	if gotTotalCount := httpmock.GetTotalCallCount(); wantTotalCount != gotTotalCount {
		t.Errorf("Want total number of API calls to be %d, got %d", wantTotalCount, gotTotalCount)
	}
}

func TestEventPollerPollCache(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	repo := Repo{
		Owner: "kuberik",
		Name:  "foo",
	}

	mockEventID := "123456789"
	mockEvents := []github.Event{mockGithubEvent(mockEventID)}
	etag := "5f36d139db088e04db015fd8232e28da5679ff4a03add6d5be8532ccbe1db928"

	httpmock.RegisterResponder(
		"GET",
		fmt.Sprintf("https://api.github.com/repos/%s/%s/events", repo.Owner, repo.Name),
		func(req *http.Request) (resp *http.Response, err error) {
			if req.Header.Get("if-none-match") == etag {
				resp = httpmock.NewStringResponse(http.StatusNotModified, "")
			} else {
				resp, err = httpmock.NewJsonResponse(http.StatusOK, mockEvents)
				if err != nil {
					t.Fatalf("Failed to setup JSON response: %v", err)
				}
			}
			resp.Header.Set("ETag", fmt.Sprintf(`\W"%s"`, etag))
			return resp, nil
		},
	)

	eventPoller := NewEventPoller()
	eventPoller.Repo = repo
	pollResult, err := eventPoller.PollOnce()
	if err != nil {
		t.Fatalf("Poll resulted in an error: %s", err)
	}
	if len(pollResult.Events) != len(mockEvents) {
		t.Errorf("Want %d events on poll, but got %d", len(mockEvents), len(pollResult.Events))
	}
	if string(pollResult.ETag) != etag {
		t.Errorf("Want etag %s, got %s", etag, pollResult.ETag)
	}

	pollResult, err = eventPoller.PollOnce()
	if err != nil {
		t.Fatalf("Poll resulted in an error: %s", err)
	}
	if len(pollResult.Events) != 0 {
		t.Errorf("Want %d events on poll, but got %d", 0, len(pollResult.Events))
	}
	if string(pollResult.ETag) != etag {
		t.Errorf("Want etag %s, got %s", etag, pollResult.ETag)
	}

	// get count info
	wantTotalCount := 2
	if gotTotalCount := httpmock.GetTotalCallCount(); wantTotalCount != gotTotalCount {
		t.Errorf("Want total number of API calls to be %d, got %d", wantTotalCount, gotTotalCount)
	}
}

func TestEventPollerPollTwice(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	repo := Repo{
		Owner: "kuberik",
		Name:  "foo",
	}

	mockEventID := "123456789"
	mockEvents := []github.Event{mockGithubEvent(mockEventID)}
	etag := "5f36d139db088e04db015fd8232e28da5679ff4a03add6d5be8532ccbe1db928"

	httpmock.RegisterResponder(
		"GET",
		fmt.Sprintf("https://api.github.com/repos/%s/%s/events", repo.Owner, repo.Name),
		func(req *http.Request) (resp *http.Response, err error) {
			resp, err = httpmock.NewJsonResponse(http.StatusOK, mockEvents)
			if err != nil {
				t.Fatalf("Failed to setup JSON response: %v", err)
			}
			resp.Header.Set("ETag", fmt.Sprintf(`\W"%s"`, etag))
			return resp, nil
		},
	)

	eventPoller := NewEventPoller()
	eventPoller.Repo = repo
	pollResult, err := eventPoller.PollOnce()
	if err != nil {
		t.Fatalf("Poll resulted in an error: %s", err)
	}
	if len(pollResult.Events) != len(mockEvents) {
		t.Errorf("Want %d events on poll, but got %d", len(mockEvents), len(pollResult.Events))
	}
	if string(pollResult.ETag) != etag {
		t.Errorf("Want etag %s, got %s", etag, pollResult.ETag)
	}

	mockEventNewID := "987656432"
	etag = "22289f995675ee81e38e8579a704733701c42f7ee36197d08ba2d9d29c65424f"
	mockEvents = []github.Event{mockGithubEvent(mockEventNewID), mockEvents[0]}

	pollResult, err = eventPoller.PollOnce()
	if err != nil {
		t.Fatalf("Poll resulted in an error: %s", err)
	}
	if len(pollResult.Events) != 1 {
		t.Errorf("Want %d events on poll, but got %d", 1, len(pollResult.Events))
	}
	if pollResult.Events[0].Spec.Data[eventIDKey] != mockEventNewID {
		t.Errorf("Want event with ID %v, but got event with ID %v", mockEventNewID, pollResult.Events[0].Spec.Data[eventIDKey])
	}
	if string(pollResult.ETag) != etag {
		t.Errorf("Want etag %s, got %s", etag, pollResult.ETag)
	}

	// get count info
	wantTotalCount := 2
	if gotTotalCount := httpmock.GetTotalCallCount(); wantTotalCount != gotTotalCount {
		t.Errorf("Want total number of API calls to be %d, got %d", wantTotalCount, gotTotalCount)
	}
}
