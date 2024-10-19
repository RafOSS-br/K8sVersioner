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

// GitConfigSpec defines the desired state of GitConfig
type GitConfigSpec struct {
	Protocol           string    `json:"protocol" validate:"required,oneof=http https ssh"`               // Protocol
	RepositoryURL      string    `json:"repositoryUrl" validate:"required,url"`                           // Repository URL
	Branch             string    `json:"branch" validate:"required"`                                      // Branch
	Username           string    `json:"username,omitempty" validate:"required"`                          // Username (optional)
	Password           string    `json:"password,omitempty" validate:"required"`                          // Password (optional) // TODO: read from secret
	SSHPrivateKeyPath  string    `json:"sshPrivateKeyPath,omitempty" validate:"required_if=Protocol ssh"` // SSH Private Key Path
	RepositoryBasePath string    `json:"repositoryBasePath" validate:"required"`                          // Repository Path
	DryRun             bool      `json:"dryRun,omitempty"`                                                // Dry Run
	Signature          Signature `json:"signature,omitempty" validate:"required"`                         // Signature
}

type Signature struct {
	Name  string `json:"name" validate:"required"`
	Email string `json:"email" validate:"required,email"`
}

// GitConfigStatus defines the observed state of GitConfig
type GitConfigStatus struct {
	Error string `json:"error,omitempty"` // Error message
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Cluster,shortName=gitconfigs

// GitConfig is the Schema for the gitconfigs API
type GitConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GitConfigSpec   `json:"spec,omitempty"`
	Status GitConfigStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// GitConfigList contains a list of GitConfig
type GitConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GitConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GitConfig{}, &GitConfigList{})
}
