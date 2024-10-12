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

// GitConfigSpec defines the desired state of GitConfig
type GitConfigSpec struct {
	Protocol          string `json:"protocol" validate:"required,oneof=http https ssh"`               // Protocol
	RepositoryURL     string `json:"repositoryUrl" validate:"required,url"`                           // Repository URL
	Branch            string `json:"branch" validate:"required"`                                      // Branch
	Username          string `json:"username,omitempty" validate:"required"`                          // Username (optional)
	Password          string `json:"password,omitempty" validate:"required"`                          // Password (optional)
	SSHPrivateKeyPath string `json:"sshPrivateKeyPath,omitempty" validate:"required_if=Protocol ssh"` // SSH Private Key Path
	RepositoryPath    string `json:"repositoryPath" validate:"required"`                              // Repository Path
	RepositoryFolder  string `json:"repositoryFolder" validate:"required"`                            // Repository Folder
	DryRun            bool   `json:"dryRun,omitempty"`
}

// GitConfigStatus defines the observed state of GitConfig
type GitConfigStatus struct {
	LastRun string `json:"lastRun,omitempty"` // Last run timestamp
	Error   string `json:"error,omitempty"`   // Error message
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

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
