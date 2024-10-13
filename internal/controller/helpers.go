package controller

import (
	"context"

	k8sversionerv1alpha1 "github.com/RafOSS-br/K8sVersioner/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
)

// StateUpdate updates the status of the resource
func (r *ConfigReconciler) StateUpdate(ctx context.Context, req ctrl.Request, cfg *k8sversionerv1alpha1.Config, err ...error) error {
	if len(err) > 0 {
		cfg.Status.Error = err[0].Error()
	}

	if err := r.Status().Update(ctx, cfg); err != nil {
		return err
	}

	return nil
}

// StateUpdate updates the status of the resource
func (r *GitConfigReconciler) StateUpdate(ctx context.Context, req ctrl.Request, cfg *k8sversionerv1alpha1.GitConfig, err ...error) error {
	if len(err) > 0 {
		cfg.Status.Error = err[0].Error()
	}

	if err := r.Status().Update(ctx, cfg); err != nil {
		return err
	}

	return nil
}
