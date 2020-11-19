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

	f, err := reconciler.FinalizerResult(r, screener)
	if f != nil {
		return *f, err
	}

	if r.screenerShutdown == nil {
		r.screenerShutdown = make(map[types.NamespacedName]chan bool)
	}
	if r.screenerUpdate == nil {
		r.screenerUpdate = make(map[types.NamespacedName]chan corev1alpha1.Screener)
	}

	nn := NamespacedName(&screener)
	_, screenerStarted := r.screenerShutdown[nn]
	if !screenerStarted {
		r.screenerShutdown[nn] = make(chan bool, 1)
		r.screenerUpdate[nn] = make(chan corev1alpha1.Screener, 1)
	}
	r.UpdateScreener(screener)

	if !screenerStarted {
		reqLogger.Info("Starting screener")
		go r.RunScreener(nn)
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

func (r *ScreenerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1alpha1.Screener{}).
		Complete(r)
}

func (r *ScreenerReconciler) RunScreener(nn types.NamespacedName) {
	poller := NewEventPoller()
	var screener corev1alpha1.Screener
	for {
		reqLogger := r.Log.WithValues("running", &screener)
		select {
		case screener = <-r.screenerUpdate[nn]:
			reqLogger.Info("Update screener")
			config := &PushScreenerConfig{}
			ParseScreenerConfig(screener, config)
			poller.Repo = config.Repo
			if config.TokenSecret != "" {
				secret := &v1.Secret{}
				err := r.Get(context.TODO(), types.NamespacedName{Name: config.TokenSecret, Namespace: screener.Namespace}, secret)

				if err != nil {
					// Skip setting up authentication for now. The section will be called again on 403
					reqLogger.Error(err, "failed to fetch token secret")
					continue
				}

				tokenKey := "token"
				if _, ok := secret.Data[tokenKey]; ok {
					poller.Token.AccessToken = string(secret.Data[tokenKey])
				}
			}
		case _ = <-r.screenerShutdown[nn]:
			reqLogger.Info("Shutdown screener")
			return
		default:
			reqLogger.Info("Poll")
			result, err := poller.PollOnce()
			// TODO what to do on error?
			if errors.IsUnauthorized(err) {
				r.screenerUpdate[nn] <- screener
			} else if err != nil {
				time.Sleep(time.Second)
				reqLogger.Error(err, "failed poll")
				continue
			}
			r.processPollResult(screener, *result)
			time.Sleep(time.Duration(result.PollInterval) * time.Second)
		}
	}
}

func (r *ScreenerReconciler) UpdateScreener(screener corev1alpha1.Screener) {
	r.screenerUpdate[NamespacedName(&screener)] <- screener
}

func (r *ScreenerReconciler) ShutdownScreener(screener corev1alpha1.Screener) error {
	nn := NamespacedName(&screener)
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
		// TODO remove if not needed
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
