package reconciler

// TODO move to github.com/kuberik/kuberik

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type ScreenerReconciler interface {
	reconcile.Reconciler
	client.Client
	ShutdownScreener(controllerutil.Object) error
}

const screenerFinalizer = "finalizer.screener.kuberik.io"

func addFinalizer(s ScreenerReconciler, screener controllerutil.Object) error {
	// TODO
	// reqLogger := s.WithValues("screenerfinalizeadd")
	// TODOs
	// reqLogger.Info("Adding Finalizer for the Memcached")
	controllerutil.AddFinalizer(screener, screenerFinalizer)

	// Update CR
	err := s.Update(context.TODO(), screener)
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

func FinalizerResult(sr ScreenerReconciler, screener controllerutil.Object) (*ctrl.Result, error) {
	// Check if the Screener instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isMemcachedMarkedToBeDeleted := screener.GetDeletionTimestamp() != nil
	if isMemcachedMarkedToBeDeleted {
		if contains(screener.GetFinalizers(), screenerFinalizer) {
			// Run finalization logic for screenerFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			if err := sr.ShutdownScreener(screener); err != nil {
				return &ctrl.Result{}, err
			}

			// Remove screenerFinalizer. Once all finalizers have been
			// removed, the object will be deleted.
			controllerutil.RemoveFinalizer(screener, screenerFinalizer)
			err := sr.Update(context.TODO(), screener)
			if err != nil {
				return &ctrl.Result{}, err
			}
		}
		return &ctrl.Result{}, nil
	}

	// Add finalizer for this CR
	if !contains(screener.GetFinalizers(), screenerFinalizer) {
		if err := addFinalizer(sr, screener); err != nil {
			return &ctrl.Result{}, err
		}
	}
	return nil, nil
}
