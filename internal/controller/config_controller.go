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
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"

	k8sversionerv1alpha1 "github.com/RafOSS-br/K8sVersioner/api/v1alpha1"
	"github.com/RafOSS-br/K8sVersioner/internal/store"
	"github.com/go-playground/validator/v10"
)

// ConfigReconciler reconciles a Config object
type ConfigReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=versioning.k8sversioner.app,resources=configs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=versioning.k8sversioner.app,resources=configs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=versioning.k8sversioner.app,resources=configs/finalizers,verbs=update
func (r *ConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling Config")
	config := &k8sversionerv1alpha1.Config{}

	if err := r.Get(ctx, req.NamespacedName, config); err != nil {

		if errors.IsNotFound(err) {
			if err := store.StoreSingleton.DeleteConfig(ctx, req.Name); err != nil {
				logger.Error(err, "unable to delete Config")
				return ctrl.Result{}, err
			}
			logger.Info("Deleted Config resource")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to fetch Config")
		return ctrl.Result{}, err
	}

	v := validator.New()
	if err := v.Struct(config.Spec); err != nil {
		if config.Status.Error == err.Error() {
			return ctrl.Result{}, nil
		}
		logger.Info("Validation failed", "error", err)
		if err := r.StateUpdate(ctx, req, config, err); err != nil {
			logger.Error(err, "unable to update Config state")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	err := store.StoreSingleton.CreateOrUpdateConfig(ctx, config)
	if err != nil {
		if err == store.ErrGitConfigNotFound {
			logger.Info(err.Error())
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to create or update Config")
		return ctrl.Result{}, err
	}

	logger.Info("Loaded Config", "name", config.Name)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&k8sversionerv1alpha1.Config{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		Complete(r)
}
