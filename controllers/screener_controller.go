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
	if r.screenerShutdown == nil {
		r.screenerShutdown = make(map[types.NamespacedName]chan bool)
	}

	nn := NamespacedName(&screener)
	if _, ok := r.screenerShutdown[nn]; ok {
		return nil
	}

	shutdown := make(chan bool)
	r.screenerShutdown[nn] = shutdown
	go func() {
		poller := NewEventPoller()
		select {
		case _ = <-shutdown:
			break
		default:
			// TODO parse spec
			result := poller.PollOnce(corev1alpha1.Repo{
				Owner: "kuberik",
				Name:  "kuberik",
			})
			time.Sleep(time.Duration(result.PollInterval) * time.Second)
			r.processPollResult(screener, result)
		}
	}()
	return nil
}

func (r *ScreenerReconciler) ShutdownScreener(screener controllerutil.Object) error {
	nn := NamespacedName(screener)
	r.screenerShutdown[nn] <- true
	return nil
}

const (
	etagLabel = "core.kuberik.io/etag"

	pushEventSuffix = "gh-pe"
)

func (r *ScreenerReconciler) processPollResult(screener corev1alpha1.Screener, result EventPollResult) {
	reqLogger := r.Log.WithValues()

	for _, e := range result.Events {
		payload, err := e.ParsePayload()
		if err != nil {
			reqLogger.Error(err, fmt.Sprintf("Failed to parse an event %s/%s#%s", e.GetRepo().GetOwner(), e.GetRepo().GetName(), e.GetID()))
			continue
		}

		pushEvent, ok := payload.(*github.PushEvent)
		if !ok {
			continue
		}

		ke := corev1alpha1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					// TODO remove
					"core.kuberik.io/push-ref": pushEvent.GetRef(),
				},
				Labels: map[string]string{
					etagLabel: result.ETag,
				},
				GenerateName: fmt.Sprintf("%s-%s-", screener.Name, pushEventSuffix),
			},
			Spec: corev1alpha1.EventSpec{},
		}
		r.Create(context.TODO(), &ke)
	}
}

func NamespacedName(object controllerutil.Object) types.NamespacedName {
	return types.NamespacedName{Namespace: object.GetNamespace(), Name: object.GetName()}
}
