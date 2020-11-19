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
		sc := NewPushEventScreenerController(r.Client)
		go r.StartScreener(&sc, nn)
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

func (r *ScreenerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1alpha1.Screener{}).
		Complete(r)
}

func populateEvent(screener corev1alpha1.Screener, event *corev1alpha1.Event) {
	if event.Labels == nil {
		event.Labels = map[string]string{}
	}
	if event.Annotations == nil {
		event.Annotations = map[string]string{}
	}
	event.Namespace = screener.Namespace
	event.GenerateName = fmt.Sprintf("%s-", screener.Name)
	event.Spec.Movie = screener.Spec.Movie
}

func (r *ScreenerReconciler) reloadScreener(
	sc ScreenerController,
	screener corev1alpha1.Screener,
	eventCreate chan corev1alpha1.Event,
	reloadScreener chan bool,
) {
	reqLogger := r.Log.WithValues("screener", NamespacedName(&screener))
	reqLogger.Info("updating")
	if eventCreate != nil {
		close(eventCreate)
	}
	// TODO reduce to less than 100
	eventCreate = make(chan corev1alpha1.Event, 100)
	if err := sc.Update(screener); err != nil {
		// TODO do exponexntial back-off
		time.Sleep(5 * time.Second)
		reloadScreener <- true
		return
	}
	reqLogger.Info("restarting")
	go func() {
		defer func() { recover(); reqLogger.Info("stopped old version") }()
		if err := sc.Screen(eventCreate); err != nil {
			// TODO do exponential back-off
			time.Sleep(5 * time.Second)
			reloadScreener <- true
		}
	}()
}

func (r *ScreenerReconciler) StartScreener(sc ScreenerController, nn types.NamespacedName) {
	var screener corev1alpha1.Screener
	var eventCreate chan corev1alpha1.Event
	reloadScreener := make(chan bool)
	defer close(reloadScreener)

	reqLogger := r.Log.WithValues("screener", nn)
	for {
		select {
		case _ = <-reloadScreener:
			r.reloadScreener(sc, screener, eventCreate, reloadScreener)
		case screener = <-r.screenerUpdate[nn]:
			r.reloadScreener(sc, screener, eventCreate, reloadScreener)
		case _ = <-r.screenerShutdown[nn]:
			close(eventCreate)
			reqLogger.Info("shutting down")
			return
		// TODO think if we need to check if channel is open or closed
		case event := <-eventCreate:
			reqLogger.Info("creating events")
			populateEvent(screener, &event)
			err := r.Create(context.TODO(), &event)
			if err != nil {
				// TODO requeue somehow
				reqLogger.Info("Failed to create event")
			}
		}
	}
}

func (r *ScreenerReconciler) UpdateScreener(screener corev1alpha1.Screener) {
	r.screenerUpdate[NamespacedName(&screener)] <- screener
}

func (r *ScreenerReconciler) ShutdownScreener(screener corev1alpha1.Screener) error {
	nn := NamespacedName(&screener)
	r.screenerShutdown[nn] <- true
	close(r.screenerShutdown[nn])
	close(r.screenerUpdate[nn])
	delete(r.screenerShutdown, nn)
	delete(r.screenerUpdate, nn)
	return nil
}

const (
	etagLabel = "github.screeners.kuberik.io/etag"
)

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

type ScreenerController interface {
	Update(corev1alpha1.Screener) error
	Screen(chan corev1alpha1.Event) error
}

type PushEventScreenerController struct {
	poller EventPoller
	client.Client
}

func NewPushEventScreenerController(client client.Client) PushEventScreenerController {
	return PushEventScreenerController{
		poller: NewEventPoller(),
		Client: client,
	}
}

func (sc *PushEventScreenerController) Update(screener corev1alpha1.Screener) error {
	config := &PushScreenerConfig{}
	ParseScreenerConfig(screener, config)
	sc.poller.Repo = config.Repo
	if config.TokenSecret != "" {
		secret := &v1.Secret{}
		err := sc.Get(context.TODO(), types.NamespacedName{Name: config.TokenSecret, Namespace: screener.Namespace}, secret)

		if err != nil {
			// Skip setting up authentication for now. The function will be called again on 403
			// reqLogger.Error(err, "failed to fetch token secret")
			return err
		}

		tokenKey := "token"
		if _, ok := secret.Data[tokenKey]; ok {
			sc.poller.Token.AccessToken = string(secret.Data[tokenKey])
		}
	}
	return nil
}

func (sc *PushEventScreenerController) Screen(eventCreate chan corev1alpha1.Event) error {
	for {
		ctrl.Log.Info("polling")
		// reqLogger.Info("Poll")
		result, err := sc.poller.PollOnce()
		// TODO what to do on error?
		if err != nil {
			ctrl.Log.Error(err, "poll error")
			return err
		}
		for _, e := range sc.processPollResult(*result) {
			eventCreate <- e
		}
		time.Sleep(time.Duration(result.PollInterval) * time.Second)
	}
}

func (sc *PushEventScreenerController) processPollResult(result EventPollResult) (createEvents []corev1alpha1.Event) {
	for _, e := range result.Events {
		payload, err := e.ParsePayload()
		if err != nil {
			ctrl.Log.Error(err, fmt.Sprintf("Failed to parse an event %s/%s#%s", e.GetRepo().GetOwner(), e.GetRepo().GetName(), e.GetID()))
			continue
		}

		pushEvent, ok := payload.(*github.PushEvent)
		if !ok {
			ctrl.Log.Info("Skipping event", "type", e.GetType())
			continue
		}

		ke := corev1alpha1.Event{}
		ke.Spec.Data = map[string]string{
			"GITHUB_REF": pushEvent.GetRef(),
		}
		// TODO remove if not needed
		ke.Labels = map[string]string{
			etagLabel: result.ETag.String(),
		}
		createEvents = append(createEvents, ke)
	}
	return
}
