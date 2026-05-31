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

package controller

import (
	"fmt"

	appsv1alpha1 "github.com/georgezhong/wordpress-operator/api/v1alpha1"
)

// Names of subresources owned by a WordPress object. Centralised so that
// references stay in sync between builders and the reconciler.

const (
	// LabelManagedBy identifies resources managed by this operator.
	LabelManagedBy = "app.kubernetes.io/managed-by"
	// LabelInstance identifies the WordPress CR instance.
	LabelInstance = "app.kubernetes.io/instance"
	// LabelName identifies the application name.
	LabelName = "app.kubernetes.io/name"
	// LabelComponent identifies the component (wordpress / mysql).
	LabelComponent = "app.kubernetes.io/component"
	// LabelPartOf identifies the larger system this object is part of.
	LabelPartOf = "app.kubernetes.io/part-of"

	ManagerName = "wordpress-operator"

	ComponentWordPress = "wordpress"
	ComponentMySQL     = "mysql"

	// Finalizer for WordPress CRs.
	WordPressFinalizer = "wordpresses.apps.kubesphere.ai/finalizer"

	// RetainDataAnnotation prevents the operator from deleting MySQL VolumeClaimTemplates
	// PVCs when the WordPress CR is deleted. Set to "true" to retain the database volume.
	RetainDataAnnotation = "wordpresses.apps.kubesphere.ai/retain-data"
)

// wpName returns the WordPress Deployment / Service base name.
func wpName(wp *appsv1alpha1.WordPress) string {
	return wp.Name
}

// dbServiceName returns the headless Service name for the internal MySQL.
func dbServiceName(wp *appsv1alpha1.WordPress) string {
	return fmt.Sprintf("%s-mysql", wp.Name)
}

// dbStatefulSetName returns the StatefulSet name for the internal MySQL.
func dbStatefulSetName(wp *appsv1alpha1.WordPress) string {
	return fmt.Sprintf("%s-mysql", wp.Name)
}

// dbSecretName returns the Secret name holding internal-DB credentials.
func dbSecretName(wp *appsv1alpha1.WordPress) string {
	return fmt.Sprintf("%s-mysql-credentials", wp.Name)
}

// wpPVCName returns the PVC name backing /var/www/html.
func wpPVCName(wp *appsv1alpha1.WordPress) string {
	return fmt.Sprintf("%s-content", wp.Name)
}

// hpaName returns the HPA name.
func hpaName(wp *appsv1alpha1.WordPress) string {
	return fmt.Sprintf("%s-hpa", wp.Name)
}

// commonLabels for resources owned by a given WordPress.
func commonLabels(wp *appsv1alpha1.WordPress, component string) map[string]string {
	return map[string]string{
		LabelName:      "wordpress",
		LabelInstance:  wp.Name,
		LabelComponent: component,
		LabelManagedBy: ManagerName,
		LabelPartOf:    "wordpress-operator",
	}
}

// selectorLabels are the subset of labels used in label selectors.
// They MUST be stable across versions, so keep this list minimal.
func selectorLabels(wp *appsv1alpha1.WordPress, component string) map[string]string {
	return map[string]string{
		LabelName:      "wordpress",
		LabelInstance:  wp.Name,
		LabelComponent: component,
	}
}

// mergeStringMaps returns a new map with all keys from src and overrides.
// overrides takes precedence.
func mergeStringMaps(src map[string]string, overrides ...map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range src {
		out[k] = v
	}
	for _, o := range overrides {
		for k, v := range o {
			out[k] = v
		}
	}
	return out
}
