package main

import (
	"github.com/kuberik/github-screener/operators"
	"github.com/kuberik/github-screener/screener"
	"github.com/kuberik/github-screener/screener/controllers"
)

const (
	screenerClass = "github.screeners.kuberik.io"
)

func main() {
	screener.Start(screenerClass, controllers.ScreenerOperatorProducer{
		Type: "Push",
		New:  operators.NewPushEventScreenerOperator,
	})
}
