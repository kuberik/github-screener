/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-github/v32/github"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1alpha1 "github.com/kuberik/github-screener/api/v1alpha1"
	"github.com/kuberik/github-screener/controllers/reconciler"
)

// ScreenerReconciler reconciles a Screener object
type ScreenerReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme

	screenerShutdown map[types.NamespacedName]chan bool
	screenerUpdate   map[types.NamespacedName]chan corev1alpha1.Screener
}

// +kubebuilder:rbac:groups=core.kuberik.io,resources=screeners,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.kuberik.io,resources=screeners/status,verbs=get;update;patch

func (r *ScreenerReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	reqLogger := r.Log.WithValues("screener", req.NamespacedName)

	screener := corev1alpha1.Screener{}
	err := r.Get(ctx, req.NamespacedName, &screener)
	if err != nil {
		if errors.IsNotFound(err) {
			reqLogger.Info("Screener resource not found. Ignoring since object must be deleted.")
			return ctrl.Result{}, nil
		}
		reqLogger.Error(err, "Failed to get Screener.")
		return ctrl.Result{}, err
	}

	f, err := reconciler.FinalizerResult(r, &screener)
	if f != nil {
		return *f, err
	}

	reqLogger.Info("Starting screener")
	if r.screenerShutdown == nil {
		r.screenerShutdown = make(map[types.NamespacedName]chan bool)
	}

	nn := NamespacedName(&screener)
	_, newScreener := r.screenerShutdown[nn]
	if newScreener {
		r.screenerShutdown[nn] = make(chan bool)
		r.screenerUpdate[nn] = make(chan corev1alpha1.Screener)
	}
	r.screenerUpdate[nn] <- screener

	if newScreener {
		go r.StartScreener(nn)
		return ctrl.Result{}, nil
	}
	// Update screener instead
	r.screenerUpdate[nn] <- screener

	return ctrl.Result{}, nil
}

func (r *ScreenerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1alpha1.Screener{}).
		Complete(r)
}

func (r *ScreenerReconciler) StartScreener(nn types.NamespacedName) {
	poller := NewEventPoller()
	var screener corev1alpha1.Screener
	for {
		select {
		case _ = <-r.screenerShutdown[nn]:
			return
		case screener = <-r.screenerUpdate[nn]:
			config := &PushScreenerConfig{}
			ParseScreenerConfig(screener, config)
			poller.Repo = config.Repo
			if config.TokenSecret != "" {
				secret := &v1.Secret{}
				// TODO retry on error
				_ = r.Get(context.TODO(), types.NamespacedName{Name: config.TokenSecret, Namespace: screener.Namespace}, secret)
				token, _ := base64.StdEncoding.DecodeString(string(secret.Data["token"]))
				poller.Token.AccessToken = string(token)
			}
		default:
			result, err := poller.PollOnce()
			// TODO what to do on error?
			if err == nil {
				r.processPollResult(screener, *result)
			}
			time.Sleep(time.Duration(result.PollInterval) * time.Second)
		}
	}
}

func (r *ScreenerReconciler) ShutdownScreener(screener controllerutil.Object) error {
	nn := NamespacedName(screener)
	r.screenerShutdown[nn] <- true
	delete(r.screenerShutdown, nn)
	return nil
}

const (
	etagLabel = "core.kuberik.io/etag"

	pushEventSuffix = "gh-pe"
)

func (r *ScreenerReconciler) processPollResult(screener corev1alpha1.Screener, result EventPollResult) {
	reqLogger := r.Log.WithValues("process-poll", screener)

	for _, e := range result.Events {
		payload, err := e.ParsePayload()
		if err != nil {
			reqLogger.Error(err, fmt.Sprintf("Failed to parse an event %s/%s#%s", e.GetRepo().GetOwner(), e.GetRepo().GetName(), e.GetID()))
			continue
		}

		pushEvent, ok := payload.(*github.PushEvent)
		if !ok {
			reqLogger.Info("Skipping event", "type", e.GetType())
			continue
		}

		ke := corev1alpha1.NewEvent(screener, map[string]string{
			"GITHUB_REF": pushEvent.GetRef(),
		})
		ke.Labels[etagLabel] = result.ETag.String()
		err = r.Create(context.TODO(), &ke)
		if err != nil {
			reqLogger.Error(err, "Unable to create Kuberik Event from Github Event")
		}
	}
}

func NamespacedName(object controllerutil.Object) types.NamespacedName {
	return types.NamespacedName{Namespace: object.GetNamespace(), Name: object.GetName()}
}

type PushScreenerConfig struct {
	Repo        `json:"repo"`
	TokenSecret string `json:"tokenSecret"`
}

func ParseScreenerConfig(screener corev1alpha1.Screener, obj interface{}) error {
	return json.Unmarshal(screener.Spec.Config.Raw, obj)
}
