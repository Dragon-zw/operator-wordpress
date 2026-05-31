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

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	appsv1alpha1 "github.com/georgezhong/wordpress-operator/api/v1alpha1"
)

// envFromDBSecret returns env vars for DB credentials sourced from the
// WordPress operator-managed secret.
func envFromInternalDBSecret(secretName string) []corev1.EnvVar {
	return []corev1.EnvVar{
		{
			Name: "WORDPRESS_DB_PASSWORD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
					Key:                  "wordpress-password",
				},
			},
		},
	}
}

// envFromExternalDBSecret references the user-supplied secret.
func envFromExternalDBSecret(ref appsv1alpha1.SecretKeyRef) []corev1.EnvVar {
	return []corev1.EnvVar{
		{
			Name: "WORDPRESS_DB_PASSWORD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: ref.Name},
					Key:                  ref.Key,
				},
			},
		},
	}
}

// buildWordPressDeployment renders the WordPress Deployment.
func buildWordPressDeployment(
	wp *appsv1alpha1.WordPress,
	dbHost string,
	dbPort int32,
	dbEnv []corev1.EnvVar,
) *appsv1.Deployment {
	labels := commonLabels(wp, ComponentWordPress)
	selector := selectorLabels(wp, ComponentWordPress)
	podLabels := mergeStringMaps(labels, wp.Spec.PodLabels)

	envs := append([]corev1.EnvVar{
		{Name: "WORDPRESS_DB_HOST", Value: fmt.Sprintf("%s:%d", dbHost, dbPort)},
		{Name: "WORDPRESS_DB_NAME", Value: wp.Spec.Database.Name},
		{Name: "WORDPRESS_DB_USER", Value: wp.Spec.Database.User},
		{Name: "WORDPRESS_TABLE_PREFIX", Value: wp.Spec.Database.TablePrefix},
	}, dbEnv...)

	if wp.Spec.SiteURL != "" {
		envs = append(envs,
			corev1.EnvVar{Name: "WORDPRESS_HOME", Value: wp.Spec.SiteURL},
			corev1.EnvVar{Name: "WORDPRESS_SITEURL", Value: wp.Spec.SiteURL},
		)
	}
	envs = append(envs, wp.Spec.Env...)

	volumes := []corev1.Volume{}
	volumeMounts := []corev1.VolumeMount{}

	if wp.Spec.Persistence.Enabled {
		volumes = append(volumes, corev1.Volume{
			Name: "wp-content",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: wpPVCName(wp),
				},
			},
		})
	} else {
		volumes = append(volumes, corev1.Volume{
			Name: "wp-content",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		})
	}
	volumeMounts = append(volumeMounts, corev1.VolumeMount{
		Name:      "wp-content",
		MountPath: "/var/www/html",
	})

	strategy := appsv1.DeploymentStrategy{
		Type: appsv1.RollingUpdateDeploymentStrategyType,
		RollingUpdate: &appsv1.RollingUpdateDeployment{
			MaxUnavailable: wp.Spec.MaxUnavailable,
			MaxSurge:       wp.Spec.MaxSurge,
		},
	}

	// init container waits for MySQL to accept connections before WordPress starts,
	// avoiding crash-loops when MySQL takes longer than expected to become ready.
	var initContainers []corev1.Container
	if wp.Spec.Database.Mode == appsv1alpha1.DatabaseModeInternal {
		dbHost := fmt.Sprintf("%s-mysql.%s.svc", wp.Name, wp.Namespace)
		initContainers = []corev1.Container{
			{
				Name:            "wait-for-db",
				Image:           "busybox:1.36",
				ImagePullPolicy: corev1.PullIfNotPresent,
				Command: []string{
					"sh", "-c",
					fmt.Sprintf("until nc -z -w2 %s 3306; do echo 'waiting for mysql'; sleep 3; done", dbHost),
				},
			},
		}
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      wpName(wp),
			Namespace: wp.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: wp.Spec.Replicas,
			Selector: &metav1.LabelSelector{MatchLabels: selector},
			Strategy: strategy,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      podLabels,
					Annotations: wp.Spec.PodAnnotations,
				},
				Spec: corev1.PodSpec{
					InitContainers:  initContainers,
					ImagePullSecrets: wp.Spec.ImagePullSecrets,
					NodeSelector:    wp.Spec.NodeSelector,
					Tolerations:     wp.Spec.Tolerations,
					Affinity:        wp.Spec.Affinity,
					Containers: []corev1.Container{
						{
							Name:            "wordpress",
							Image:           wp.Spec.Image,
							ImagePullPolicy: wp.Spec.ImagePullPolicy,
							Ports: []corev1.ContainerPort{
								{Name: "http", ContainerPort: 80, Protocol: corev1.ProtocolTCP},
							},
							Env:          envs,
							Resources:    wp.Spec.Resources,
							VolumeMounts: volumeMounts,
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path:   "/wp-login.php",
										Port:   intstr.FromInt(80),
										Scheme: corev1.URISchemeHTTP,
									},
								},
								InitialDelaySeconds: 20,
								PeriodSeconds:       10,
								TimeoutSeconds:      5,
								FailureThreshold:    6,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt(80)},
								},
								InitialDelaySeconds: 60,
								PeriodSeconds:       20,
								TimeoutSeconds:      5,
								FailureThreshold:    6,
							},
						},
					},
					Volumes: volumes,
				},
			},
		},
	}

	if wp.Spec.Autoscaling.Enabled {
		// HPA owns replicas; do not set spec.replicas to avoid fighting.
		dep.Spec.Replicas = nil
	}
	return dep
}

// buildWordPressService renders the WordPress Service.
func buildWordPressService(wp *appsv1alpha1.WordPress) *corev1.Service {
	labels := commonLabels(wp, ComponentWordPress)
	selector := selectorLabels(wp, ComponentWordPress)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        wpName(wp),
			Namespace:   wp.Namespace,
			Labels:      labels,
			Annotations: wp.Spec.Service.Annotations,
		},
		Spec: corev1.ServiceSpec{
			Type:     wp.Spec.Service.Type,
			Selector: selector,
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       wp.Spec.Service.Port,
					TargetPort: intstr.FromInt(80),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
	if wp.Spec.Service.Type == corev1.ServiceTypeNodePort && wp.Spec.Service.NodePort > 0 {
		svc.Spec.Ports[0].NodePort = wp.Spec.Service.NodePort
	}
	return svc
}

// buildWordPressPVC renders the WordPress PVC.
func buildWordPressPVC(wp *appsv1alpha1.WordPress) (*corev1.PersistentVolumeClaim, error) {
	q, err := resource.ParseQuantity(wp.Spec.Persistence.Size)
	if err != nil {
		return nil, fmt.Errorf("invalid persistence.size %q: %w", wp.Spec.Persistence.Size, err)
	}
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      wpPVCName(wp),
			Namespace: wp.Namespace,
			Labels:    commonLabels(wp, ComponentWordPress),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      wp.Spec.Persistence.AccessModes,
			StorageClassName: wp.Spec.Persistence.StorageClassName,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: q,
				},
			},
		},
	}, nil
}

// buildWordPressIngress renders the WordPress Ingress.
func buildWordPressIngress(wp *appsv1alpha1.WordPress) *networkingv1.Ingress {
	pathType := networkingv1.PathTypePrefix
	rules := []networkingv1.IngressRule{
		{
			Host: wp.Spec.Ingress.Host,
			IngressRuleValue: networkingv1.IngressRuleValue{
				HTTP: &networkingv1.HTTPIngressRuleValue{
					Paths: []networkingv1.HTTPIngressPath{
						{
							Path:     "/",
							PathType: &pathType,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: wpName(wp),
									Port: networkingv1.ServiceBackendPort{
										Number: wp.Spec.Service.Port,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        wpName(wp),
			Namespace:   wp.Namespace,
			Labels:      commonLabels(wp, ComponentWordPress),
			Annotations: wp.Spec.Ingress.Annotations,
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: wp.Spec.Ingress.IngressClassName,
			Rules:            rules,
		},
	}

	if wp.Spec.Ingress.TLSSecretName != "" && wp.Spec.Ingress.Host != "" {
		ing.Spec.TLS = []networkingv1.IngressTLS{
			{
				Hosts:      []string{wp.Spec.Ingress.Host},
				SecretName: wp.Spec.Ingress.TLSSecretName,
			},
		}
	}
	return ing
}

// buildWordPressHPA renders the HPA when autoscaling is enabled.
func buildWordPressHPA(wp *appsv1alpha1.WordPress) *autoscalingv2.HorizontalPodAutoscaler {
	target := wp.Spec.Autoscaling.TargetCPUUtilizationPercentage
	min := wp.Spec.Autoscaling.MinReplicas
	return &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hpaName(wp),
			Namespace: wp.Namespace,
			Labels:    commonLabels(wp, ComponentWordPress),
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       wpName(wp),
			},
			MinReplicas: &min,
			MaxReplicas: wp.Spec.Autoscaling.MaxReplicas,
			Metrics: []autoscalingv2.MetricSpec{
				{
					Type: autoscalingv2.ResourceMetricSourceType,
					Resource: &autoscalingv2.ResourceMetricSource{
						Name: corev1.ResourceCPU,
						Target: autoscalingv2.MetricTarget{
							Type:               autoscalingv2.UtilizationMetricType,
							AverageUtilization: &target,
						},
					},
				},
			},
		},
	}
}
