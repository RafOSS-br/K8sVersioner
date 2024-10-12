/*
Copyright 2024.

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

package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	k8sversionerv1alpha1 "github.com/RafOSS-br/K8sVersionerls/api/v1alpha1"
	"github.com/RafOSS-br/K8sVersionerls/internal/store"
	"github.com/go-playground/validator/v10"
)

// GitConfigReconciler reconciles a GitConfig object
type GitConfigReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=versioning.k8sversioner.app,resources=gitconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=versioning.k8sversioner.app,resources=gitconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=versioning.k8sversioner.app,resources=gitconfigs/finalizers,verbs=update
func (r *GitConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling GitConfig")
	gitConfig := &k8sversionerv1alpha1.GitConfig{}

	if err := r.Get(ctx, req.NamespacedName, gitConfig); err != nil {
		if errors.IsNotFound(err) {
			if err := store.StoreSingleton.DeleteGitConfig(req.Name); err != nil {
				logger.Error(err, "unable to delete GitConfig")
				return ctrl.Result{}, err
			}
			logger.Info("Deleted GitConfig resource")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to fetch GitConfig")
		return ctrl.Result{}, err
	}

	v := validator.New()

	if err := v.Struct(gitConfig.Spec); err != nil {
		logger.Info("GitConfig validation failed", "error", err)
		if err := r.StateUpdate(ctx, req, gitConfig, err); err != nil {
			logger.Error(err, "unable to update GitConfig state")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	err := store.StoreSingleton.CreateOrUpdateGitConfig(gitConfig)
	if err != nil {
		logger.Error(err, "unable to create or update GitConfig")
		return ctrl.Result{}, err
	}

	logger.Info("Loaded GitConfig", "name", gitConfig.Name)

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GitConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&k8sversionerv1alpha1.GitConfig{}).
		Complete(r)
}
