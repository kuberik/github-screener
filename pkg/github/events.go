package main

import (
	"context"
	"fmt"
	"os"

	"github.com/google/go-github/v32/github"
	"github.com/gregjones/httpcache"
	"golang.org/x/oauth2"

	"strconv"
	"time"
)

// NewClient returns a new cacheable GitHub client
func NewClient() *github.Client {
	httpCacheClient := httpcache.NewMemoryCacheTransport().Client()

	ctx := context.WithValue(context.TODO(), oauth2.HTTPClient, httpCacheClient)
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: os.Getenv("GITHUB_TOKEN")},
	)
	tc := oauth2.NewClient(ctx, ts)

	return github.NewClient(tc)
}

func main() {
	client := NewClient()
	for {
		events, response, _ := client.Activity.ListRepositoryEvents(context.TODO(), "kuberik", "kuberik", &github.ListOptions{})
		for _, e := range events {
			fmt.Println(*e.Type)
		}
		pollInterval, err := strconv.Atoi(response.Header.Get("X-Poll-Interval"))
		if err != nil {
			pollInterval = 60
		}
		fmt.Println(response.Header)
		time.Sleep(time.Duration(pollInterval) * time.Second)
	}
}
