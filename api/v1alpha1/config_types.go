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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ConfigSpec defines the desired state of Config
type ConfigSpec struct {
	Scope             string            `json:"scope" validate:"required,oneof=cluster namespace"`          // Scope of the config
	Namespace         string            `json:"namespace,omitempty" validate:"required_if=Scope namespace"` // Namespace to watch if scope is namespace
	IncludeResource   []*ResourceFilter `json:"includeResource,omitempty" validate:"dive"`                  // Resources to include
	Labels            map[string]string `json:"labels,omitempty"`                                           // Label filters
	Annotations       map[string]string `json:"annotations,omitempty"`                                      // Annotation filters
	GitRef            string            `json:"gitRef" validate:"required"`                                 // Reference to GitConfig
	FolderStructure   string            `json:"folderStructure" validate:"required"`                        // Folder structure
	ExcludeFieldPaths []string          `json:"excludeFieldPaths,omitempty"`                                // Paths to exclude
}

type ResourceFilter struct {
	Kind    string `json:"kind" validate:"required"` // Name of the resource
	Group   string `json:"group,omitempty"`          // Group of the resource
	Version string `json:"version,omitempty"`        // API version of the resource
}

// ConfigStatus defines the observed state of Config
type ConfigStatus struct {
	Error string `json:"error,omitempty"` // Error message
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=configs

// Config is the Schema for the configs API
type Config struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ConfigSpec   `json:"spec,omitempty"`
	Status ConfigStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ConfigList contains a list of Config
type ConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Config `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Config{}, &ConfigList{})
}
