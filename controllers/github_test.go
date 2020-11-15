package controllers

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/go-github/v32/github"
	"github.com/jarcoal/httpmock"
)

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

	eventPoller := NewEventPoller(repo)
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
