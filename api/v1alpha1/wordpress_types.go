/*
Copyright 2026 George Zhong.

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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// DatabaseMode defines how the WordPress database is provided.
// +kubebuilder:validation:Enum=Internal;External
type DatabaseMode string

const (
	// DatabaseModeInternal means the operator will provision a MySQL StatefulSet
	// for the WordPress instance.
	DatabaseModeInternal DatabaseMode = "Internal"
	// DatabaseModeExternal means the user provides connection info for an
	// already-existing MySQL/MariaDB compatible database.
	DatabaseModeExternal DatabaseMode = "External"
)

// SecretKeyRef points to a key inside a Secret in the same namespace.
type SecretKeyRef struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

// InternalDatabaseSpec configures an operator-managed MySQL StatefulSet.
type InternalDatabaseSpec struct {
	// Image for the MySQL/MariaDB container.
	// +kubebuilder:default="mysql:8.0"
	// +optional
	Image string `json:"image,omitempty"`

	// ImagePullPolicy of the database container.
	// +kubebuilder:default=IfNotPresent
	// +optional
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// StorageSize for the database PVC.
	// +kubebuilder:default="5Gi"
	// +optional
	StorageSize string `json:"storageSize,omitempty"`

	// StorageClassName for the database PVC; nil uses cluster default.
	// +optional
	StorageClassName *string `json:"storageClassName,omitempty"`

	// Resources requests/limits for the database container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// RootPasswordSecret optionally references an existing Secret to use as the
	// MySQL root password. If nil, the operator generates one and stores it.
	// +optional
	RootPasswordSecret *SecretKeyRef `json:"rootPasswordSecret,omitempty"`

	// NodeSelector placed on the database pods.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Tolerations placed on the database pods.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// Affinity placed on the database pods.
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`
}

// ExternalDatabaseSpec configures connection to a pre-existing database.
type ExternalDatabaseSpec struct {
	// Host of the external database.
	// +kubebuilder:validation:MinLength=1
	Host string `json:"host"`

	// Port of the external database.
	// +kubebuilder:default=3306
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	Port int32 `json:"port,omitempty"`

	// PasswordSecret references the Secret holding the database password.
	// +kubebuilder:validation:Required
	PasswordSecret SecretKeyRef `json:"passwordSecret"`
}

// DatabaseSpec selects how WordPress connects to its database.
// Common fields apply to both internal and external modes.
type DatabaseSpec struct {
	// Mode chooses Internal (operator-managed) or External (BYO).
	// +kubebuilder:default=Internal
	// +optional
	Mode DatabaseMode `json:"mode,omitempty"`

	// Name of the WordPress database.
	// +kubebuilder:default="wordpress"
	// +kubebuilder:validation:MinLength=1
	// +optional
	Name string `json:"name,omitempty"`

	// User used by WordPress to access the database.
	// +kubebuilder:default="wordpress"
	// +kubebuilder:validation:MinLength=1
	// +optional
	User string `json:"user,omitempty"`

	// TablePrefix for WordPress tables.
	// +kubebuilder:default="wp_"
	// +optional
	TablePrefix string `json:"tablePrefix,omitempty"`

	// Internal configures the operator-managed database. Used when
	// Mode is Internal. Ignored otherwise.
	// +optional
	Internal *InternalDatabaseSpec `json:"internal,omitempty"`

	// External configures the BYO database. Required when Mode is External.
	// +optional
	External *ExternalDatabaseSpec `json:"external,omitempty"`
}

// IngressSpec configures an optional Ingress object exposing WordPress.
type IngressSpec struct {
	// Enabled toggles the creation of an Ingress.
	// +kubebuilder:default=false
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// IngressClassName selects the IngressClass.
	// +optional
	IngressClassName *string `json:"ingressClassName,omitempty"`

	// Host name for the WordPress site.
	// +optional
	Host string `json:"host,omitempty"`

	// TLSSecretName references an existing Secret of type kubernetes.io/tls.
	// +optional
	TLSSecretName string `json:"tlsSecretName,omitempty"`

	// Annotations to add to the Ingress.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// ServiceSpec configures the WordPress Service.
type ServiceSpec struct {
	// Type of the WordPress service.
	// +kubebuilder:default=ClusterIP
	// +kubebuilder:validation:Enum=ClusterIP;NodePort;LoadBalancer
	// +optional
	Type corev1.ServiceType `json:"type,omitempty"`

	// Port exposed by the service.
	// +kubebuilder:default=80
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	Port int32 `json:"port,omitempty"`

	// NodePort, used when Type is NodePort.
	// +optional
	NodePort int32 `json:"nodePort,omitempty"`

	// Annotations applied to the service.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// PersistenceSpec configures persistent storage for the wp-content directory.
type PersistenceSpec struct {
	// Enabled toggles PVC-backed storage for /var/www/html.
	// When disabled, an emptyDir is used (data is lost on pod restart).
	// +kubebuilder:default=true
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// Size of the WordPress PVC.
	// +kubebuilder:default="5Gi"
	// +optional
	Size string `json:"size,omitempty"`

	// StorageClassName for the PVC; nil uses the cluster default.
	// +optional
	StorageClassName *string `json:"storageClassName,omitempty"`

	// AccessModes for the PVC.
	// +optional
	AccessModes []corev1.PersistentVolumeAccessMode `json:"accessModes,omitempty"`
}

// AutoscalingSpec configures an HPA for the WordPress Deployment.
type AutoscalingSpec struct {
	// Enabled toggles HPA creation.
	// +kubebuilder:default=false
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// MinReplicas lower bound for autoscaling.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +optional
	MinReplicas int32 `json:"minReplicas,omitempty"`

	// MaxReplicas upper bound for autoscaling.
	// +kubebuilder:default=5
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxReplicas int32 `json:"maxReplicas,omitempty"`

	// TargetCPUUtilizationPercentage for the HPA.
	// +kubebuilder:default=80
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +optional
	TargetCPUUtilizationPercentage int32 `json:"targetCPUUtilizationPercentage,omitempty"`
}

// WordPressSpec defines the desired state of WordPress.
type WordPressSpec struct {
	// Image for the WordPress container.
	// +kubebuilder:default="wordpress:6.5-apache"
	// +optional
	Image string `json:"image,omitempty"`

	// ImagePullPolicy of the WordPress container.
	// +kubebuilder:default=IfNotPresent
	// +optional
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// ImagePullSecrets used to pull the WordPress image.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// Replicas for the WordPress Deployment. Ignored if autoscaling is enabled.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// Resources requests/limits for the WordPress container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// SiteURL is exposed to WordPress via WORDPRESS_HOME / WORDPRESS_SITEURL.
	// +optional
	SiteURL string `json:"siteURL,omitempty"`

	// Env additional environment variables passed to the WordPress container.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// Database configures the database backend.
	// +optional
	Database DatabaseSpec `json:"database,omitempty"`

	// Service configures the Service that fronts WordPress.
	// +optional
	Service ServiceSpec `json:"service,omitempty"`

	// Ingress configures an optional Ingress.
	// +optional
	Ingress IngressSpec `json:"ingress,omitempty"`

	// Persistence configures wp-content storage.
	// +optional
	Persistence PersistenceSpec `json:"persistence,omitempty"`

	// Autoscaling configures the HPA.
	// +optional
	Autoscaling AutoscalingSpec `json:"autoscaling,omitempty"`

	// PodAnnotations applied to the WordPress pods.
	// +optional
	PodAnnotations map[string]string `json:"podAnnotations,omitempty"`

	// PodLabels applied to the WordPress pods.
	// +optional
	PodLabels map[string]string `json:"podLabels,omitempty"`

	// NodeSelector placed on the WordPress pods.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Tolerations placed on the WordPress pods.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// Affinity placed on the WordPress pods.
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// MaxUnavailable for the rolling update strategy.
	// +optional
	MaxUnavailable *intstr.IntOrString `json:"maxUnavailable,omitempty"`

	// MaxSurge for the rolling update strategy.
	// +optional
	MaxSurge *intstr.IntOrString `json:"maxSurge,omitempty"`
}

// WordPressPhase summarizes the lifecycle state.
// +kubebuilder:validation:Enum=Pending;Provisioning;Ready;Failed
type WordPressPhase string

const (
	PhasePending      WordPressPhase = "Pending"
	PhaseProvisioning WordPressPhase = "Provisioning"
	PhaseReady        WordPressPhase = "Ready"
	PhaseFailed       WordPressPhase = "Failed"
)

// Standard condition types used by this operator.
const (
	ConditionTypeReady             = "Ready"
	ConditionTypeDatabaseReady     = "DatabaseReady"
	ConditionTypeDeploymentReady   = "DeploymentReady"
	ConditionTypeIngressConfigured = "IngressConfigured"
)

// WordPressStatus defines the observed state of WordPress.
type WordPressStatus struct {
	// ObservedGeneration is the generation last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Phase is a high-level summary of the WordPress lifecycle.
	// +optional
	Phase WordPressPhase `json:"phase,omitempty"`

	// URL is the (best-effort) externally accessible URL for the site.
	// +optional
	URL string `json:"url,omitempty"`

	// Replicas observed for the WordPress Deployment.
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// ReadyReplicas observed for the WordPress Deployment.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// DatabaseHost contains the resolved database host the operator wrote
	// into the WordPress configuration.
	// +optional
	DatabaseHost string `json:"databaseHost,omitempty"`

	// Conditions represent the current state of the WordPress resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:subresource:scale:specpath=.spec.replicas,statuspath=.status.replicas
// +kubebuilder:resource:shortName=wp;wps,categories=apps
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Ready",type="integer",JSONPath=".status.readyReplicas"
// +kubebuilder:printcolumn:name="Replicas",type="integer",JSONPath=".status.replicas"
// +kubebuilder:printcolumn:name="URL",type="string",JSONPath=".status.url"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// WordPress is the Schema for the wordpresses API.
type WordPress struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of WordPress
	// +required
	Spec WordPressSpec `json:"spec"`

	// status defines the observed state of WordPress
	// +optional
	Status WordPressStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// WordPressList contains a list of WordPress.
type WordPressList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []WordPress `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WordPress{}, &WordPressList{})
}
