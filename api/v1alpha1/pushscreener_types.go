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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// PushScreenerSpec defines the desired state of PushScreener
type PushScreenerSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Repo specifies which GitHub repository needs to be screened for push events
	Repo `json:"repo"`
}

// PushScreenerStatus defines the observed state of PushScreener
type PushScreenerStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	ETag `json:"etag,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// PushScreener is the Schema for the pushscreeners API
type PushScreener struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PushScreenerSpec   `json:"spec,omitempty"`
	Status PushScreenerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PushScreenerList contains a list of PushScreener
type PushScreenerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PushScreener `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PushScreener{}, &PushScreenerList{})
}
