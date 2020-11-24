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
	"strconv"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1alpha1 "github.com/kuberik/github-screener/api/v1alpha1"
)

type ScreenerOperator interface {
	Update(corev1alpha1.Screener) error
	Screen(chan corev1alpha1.Event, chan bool) error
	Recover(corev1alpha1.Event) error
}

type ScreenerOperatorProducer struct {
	Type string
	New  func(client.Client, logr.Logger) ScreenerOperator
}

// ScreenerReconciler reconciles a Screener object
type ScreenerReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme

	ScreenerOperatorProducers []ScreenerOperatorProducer

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

	if fr, err := r.FinalizerResult(screener); fr != nil {
		return *fr, err
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
		for _, p := range r.ScreenerOperatorProducers {
			if p.Type == screener.Spec.Type {
				sc := p.New(r.Client, reqLogger)
				go r.StartScreener(sc, screener)
				return ctrl.Result{}, nil
			}
		}
		// TODO update status of screener and log error
	}

	return ctrl.Result{}, nil
}

func (r *ScreenerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1alpha1.Screener{}).
		Complete(r)
}

const screenerFinalizer = "finalizer.screener.kuberik.io"

func (r *ScreenerReconciler) addFinalizer(screener controllerutil.Object) error {
	// TODO
	// reqLogger := s.WithValues("screenerfinalizeadd")
	// TODOs
	// reqLogger.Info("Adding Finalizer for the Screener")
	controllerutil.AddFinalizer(screener, screenerFinalizer)

	// Update CR
	err := r.Update(context.TODO(), screener)
	if err != nil {
		// TODO
		// reqLogger.Error(err, "Failed to update screener with finalizer")
		return err
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func (r *ScreenerReconciler) FinalizerResult(screener corev1alpha1.Screener) (*ctrl.Result, error) {
	// Check if the Screener instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isScreenerMarkedToBeDeleted := screener.GetDeletionTimestamp() != nil
	if isScreenerMarkedToBeDeleted {
		if contains(screener.GetFinalizers(), screenerFinalizer) {
			// Run finalization logic for screenerFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			if err := r.ShutdownScreener(screener); err != nil {
				return &ctrl.Result{}, err
			}

			// Remove screenerFinalizer. Once all finalizers have been
			// removed, the object will be deleted.
			controllerutil.RemoveFinalizer(&screener, screenerFinalizer)
			err := r.Update(context.TODO(), &screener)
			if err != nil {
				return &ctrl.Result{}, err
			}
		}
		return &ctrl.Result{}, nil
	}

	// Add finalizer for this CR
	if !contains(screener.GetFinalizers(), screenerFinalizer) {
		if err := r.addFinalizer(&screener); err != nil {
			return &ctrl.Result{}, err
		}
	}
	return nil, nil
}

func screenerKey(screener corev1alpha1.Screener, key string) string {
	return fmt.Sprintf("%s/%s", screener.Spec.Class, key)
}

func eventLabels(screener corev1alpha1.Screener) labels.Set {
	return labels.Set{
		screenerKey(screener, "screener"): screener.Name,
	}
}

func setEventDefaults(screener corev1alpha1.Screener, event *corev1alpha1.Event) {
	event.Labels = labels.Merge(event.Labels, eventLabels(screener))
	if event.Annotations == nil {
		event.Annotations = map[string]string{}
	}
	event.Namespace = screener.Namespace
	event.GenerateName = fmt.Sprintf("%s-", screener.Name)
	event.Spec.Movie = screener.Spec.Movie
}

func (r *ScreenerReconciler) rebootScreener(
	sc ScreenerOperator,
	screener corev1alpha1.Screener,
	eventCreate chan corev1alpha1.Event,
	reloadScreener chan bool,
	stopScreener *chan bool,
) {
	reqLogger := r.Log.WithValues("screener", NamespacedName(&screener))

	if *stopScreener != nil {
		reqLogger.Info("sending stop signal to old screener process")
		*stopScreener <- true
		close(*stopScreener)
	}
	*stopScreener = make(chan bool, 1)

	if err := sc.Update(screener); err != nil {
		// TODO do exponexntial back-off
		time.Sleep(5 * time.Second)
		reloadScreener <- true
		return
	}

	reqLogger.Info("restarting")
	go func() {
		stop := *stopScreener
		if err := sc.Screen(eventCreate, stop); err != nil {
			// TODO do exponential back-off
			time.Sleep(5 * time.Second)
			select {
			case _ = <-stop:
				close(eventCreate)
			default:
				reloadScreener <- true
			}
		}
	}()
}

func (r *ScreenerReconciler) lastEvent(screener corev1alpha1.Screener) (last *corev1alpha1.Event) {
	var eventList corev1alpha1.EventList
	r.List(context.TODO(), &eventList, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{}),
	})
	for i, e := range eventList.Items {
		if last == nil {
			last = &eventList.Items[i]
			continue
		}
		rvc, _ := strconv.Atoi(e.ResourceVersion)
		rvl, _ := strconv.Atoi(last.ResourceVersion)
		if rvc > rvl {
			last = &eventList.Items[i]
		}
	}
	return
}

func (r *ScreenerReconciler) StartScreener(sc ScreenerOperator, screener corev1alpha1.Screener) {
	nn := NamespacedName(&screener)
	var eventCreate chan corev1alpha1.Event
	reloadScreener := make(chan bool, 1)
	var stopScreener chan bool
	lastEvent := r.lastEvent(screener)

	defer close(reloadScreener)

	reqLogger := r.Log.WithValues("screener", nn)
	for {
		select {
		case _ = <-reloadScreener:
			reqLogger.Info("reloading")
			r.rebootScreener(sc, screener, eventCreate, reloadScreener, &stopScreener)
		case screener = <-r.screenerUpdate[nn]:
			reqLogger.Info("updating")
			eventCreate = make(chan corev1alpha1.Event, 1)
			if lastEvent != nil {
				reqLogger.Info("recovering")
				sc.Recover(*lastEvent)
			}
			r.rebootScreener(sc, screener, eventCreate, reloadScreener, &stopScreener)
		case _ = <-r.screenerShutdown[nn]:
			stopScreener <- true
			close(stopScreener)
			reqLogger.Info("shutting down")
			return
		// TODO think if we need to check if channel is open or closed
		case event := <-eventCreate:
			lastEvent = event.DeepCopy()
			reqLogger.Info("creating events")
			setEventDefaults(screener, &event)
			err := r.Create(context.TODO(), &event)
			if err != nil {
				// TODO requeue somehow
				reqLogger.Error(err, "Failed to create event")
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

func NamespacedName(object controllerutil.Object) types.NamespacedName {
	return types.NamespacedName{Namespace: object.GetNamespace(), Name: object.GetName()}
}

func ParseScreenerConfig(screener corev1alpha1.Screener, obj interface{}) error {
	return json.Unmarshal(screener.Spec.Config.Raw, obj)
}
