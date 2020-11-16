module github.com/kuberik/github-screener

go 1.13

require (
	github.com/go-logr/logr v0.1.0
	github.com/google/go-github/v32 v32.1.0
	github.com/jarcoal/httpmock v1.0.6
	// TODO replace with original repo when PR (https://github.com/gregjones/httpcache/pull/104) is merged
	github.com/m4ns0ur/httpcache v0.0.0-20200426190423-1040e2e8823f
	github.com/onsi/ginkgo v1.12.1
	github.com/onsi/gomega v1.10.1
	golang.org/x/oauth2 v0.0.0-20190604053449-0f29369cfe45
	k8s.io/api v0.18.6
	k8s.io/apimachinery v0.18.6
	k8s.io/client-go v0.18.6
	sigs.k8s.io/controller-runtime v0.6.3
)
