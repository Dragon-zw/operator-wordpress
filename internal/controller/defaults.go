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
	corev1 "k8s.io/api/core/v1"

	appsv1alpha1 "github.com/georgezhong/wordpress-operator/api/v1alpha1"
)

// applyDefaults fills implicit defaults that are easier to handle in code than
// via CRD defaults (e.g. defaults that depend on other field values).
func applyDefaults(wp *appsv1alpha1.WordPress) {
	if wp.Spec.Image == "" {
		wp.Spec.Image = "wordpress:6.5-apache"
	}
	if wp.Spec.ImagePullPolicy == "" {
		wp.Spec.ImagePullPolicy = corev1.PullIfNotPresent
	}
	if wp.Spec.Replicas == nil {
		one := int32(1)
		wp.Spec.Replicas = &one
	}

	if wp.Spec.Service.Type == "" {
		wp.Spec.Service.Type = corev1.ServiceTypeClusterIP
	}
	if wp.Spec.Service.Port == 0 {
		wp.Spec.Service.Port = 80
	}

	if wp.Spec.Persistence.Size == "" {
		wp.Spec.Persistence.Size = "5Gi"
	}
	if len(wp.Spec.Persistence.AccessModes) == 0 {
		wp.Spec.Persistence.AccessModes = []corev1.PersistentVolumeAccessMode{
			corev1.ReadWriteOnce,
		}
	}

	if wp.Spec.Database.Mode == "" {
		wp.Spec.Database.Mode = appsv1alpha1.DatabaseModeInternal
	}
	if wp.Spec.Database.Name == "" {
		wp.Spec.Database.Name = "wordpress"
	}
	if wp.Spec.Database.User == "" {
		wp.Spec.Database.User = "wordpress"
	}
	if wp.Spec.Database.TablePrefix == "" {
		wp.Spec.Database.TablePrefix = "wp_"
	}

	if wp.Spec.Database.Mode == appsv1alpha1.DatabaseModeInternal {
		if wp.Spec.Database.Internal == nil {
			wp.Spec.Database.Internal = &appsv1alpha1.InternalDatabaseSpec{}
		}
		if wp.Spec.Database.Internal.Image == "" {
			wp.Spec.Database.Internal.Image = "mysql:8.0"
		}
		if wp.Spec.Database.Internal.ImagePullPolicy == "" {
			wp.Spec.Database.Internal.ImagePullPolicy = corev1.PullIfNotPresent
		}
		if wp.Spec.Database.Internal.StorageSize == "" {
			wp.Spec.Database.Internal.StorageSize = "5Gi"
		}
	}

	if wp.Spec.Database.Mode == appsv1alpha1.DatabaseModeExternal && wp.Spec.Database.External != nil {
		if wp.Spec.Database.External.Port == 0 {
			wp.Spec.Database.External.Port = 3306
		}
	}

	if wp.Spec.Autoscaling.Enabled {
		if wp.Spec.Autoscaling.MinReplicas == 0 {
			wp.Spec.Autoscaling.MinReplicas = 1
		}
		if wp.Spec.Autoscaling.MaxReplicas == 0 {
			wp.Spec.Autoscaling.MaxReplicas = 5
		}
		if wp.Spec.Autoscaling.TargetCPUUtilizationPercentage == 0 {
			wp.Spec.Autoscaling.TargetCPUUtilizationPercentage = 80
		}
	}
}
