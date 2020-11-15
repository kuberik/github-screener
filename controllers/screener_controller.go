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
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1alpha1 "github.com/kuberik/github-screener/api/v1alpha1"
	"github.com/kuberik/github-screener/controllers/reconciler"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ScreenerReconciler reconciles a Screener object
type ScreenerReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme

	screenerShutdown map[types.NamespacedName]chan bool
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

	r.StartScreener(screener)

	return ctrl.Result{}, nil
}

func (r *ScreenerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1alpha1.Screener{}).
		Complete(r)
}

func (r *ScreenerReconciler) StartScreener(screener corev1alpha1.Screener) error {
	reqLogger := r.Log.WithValues("start", screener)
	reqLogger.Info("Starting screener")

	if r.screenerShutdown == nil {
		r.screenerShutdown = make(map[types.NamespacedName]chan bool)
	}

	nn := NamespacedName(&screener)
	if _, ok := r.screenerShutdown[nn]; ok {
		return nil
	}

	config := &PushScreenerConfig{}
	ParseScreenerConfig(screener, config)

	shutdown := make(chan bool)
	r.screenerShutdown[nn] = shutdown
	go func() {
		poller := NewEventPoller()
		reqLogger.Info("Start polling")
		for {
			select {
			case _ = <-shutdown:
				break
			default:
				reqLogger.Info("Polling...")
				result := poller.PollOnce(corev1alpha1.Repo{
					Owner: config.Owner,
					Name:  config.Name,
				})
				r.processPollResult(screener, result)
				time.Sleep(time.Duration(result.PollInterval) * time.Second)
			}
		}
	}()
	return nil
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

		ke := corev1alpha1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					etagLabel: result.ETag,
				},
				GenerateName: fmt.Sprintf("%s-%s-", screener.Name, pushEventSuffix),
				Namespace:    screener.Namespace,
			},
			Spec: corev1alpha1.EventSpec{
				Movie: screener.Spec.Movie,
				Data: map[string]string{
					"GITHUB_REF": pushEvent.GetRef(),
				},
			},
		}
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
	Repo `json:"repo"`
}

// Repo defines an unique GitHub repo
type Repo struct {
	Name  string `json:"name"`
	Owner string `json:"owner"`
}

func ParseScreenerConfig(screener corev1alpha1.Screener, obj interface{}) error {
	return json.Unmarshal(screener.Spec.Config.Raw, obj)
}
