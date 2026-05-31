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
	"crypto/rand"
	"encoding/base64"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	appsv1alpha1 "github.com/georgezhong/wordpress-operator/api/v1alpha1"
)

const (
	// SecretKeyRoot holds the MySQL root password.
	SecretKeyRoot = "mysql-root-password"
	// SecretKeyWordPressPassword holds the WordPress user password.
	SecretKeyWordPressPassword = "wordpress-password"
	// SecretKeyWordPressUser stores the WordPress user name (cosmetic; for parity).
	SecretKeyWordPressUser = "wordpress-user"
	// SecretKeyWordPressDB stores the WordPress database name (cosmetic; for parity).
	SecretKeyWordPressDB = "wordpress-database"
)

// generatePassword returns a 24-byte random password URL-safe base64 encoded.
func generatePassword() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// buildMySQLSecret generates a fresh credentials Secret. Used only on first
// creation; subsequent reconciles must NOT overwrite it (or passwords would
// rotate randomly).
func buildMySQLSecret(wp *appsv1alpha1.WordPress) (*corev1.Secret, error) {
	rootPwd, err := generatePassword()
	if err != nil {
		return nil, fmt.Errorf("generate root password: %w", err)
	}
	wpPwd, err := generatePassword()
	if err != nil {
		return nil, fmt.Errorf("generate wordpress password: %w", err)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dbSecretName(wp),
			Namespace: wp.Namespace,
			Labels:    commonLabels(wp, ComponentMySQL),
		},
		StringData: map[string]string{
			SecretKeyRoot:              rootPwd,
			SecretKeyWordPressPassword: wpPwd,
			SecretKeyWordPressUser:     wp.Spec.Database.User,
			SecretKeyWordPressDB:       wp.Spec.Database.Name,
		},
		Type: corev1.SecretTypeOpaque,
	}, nil
}

// buildMySQLHeadlessService returns the headless Service used by the MySQL StatefulSet.
func buildMySQLHeadlessService(wp *appsv1alpha1.WordPress) *corev1.Service {
	labels := commonLabels(wp, ComponentMySQL)
	selector := selectorLabels(wp, ComponentMySQL)
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dbServiceName(wp),
			Namespace: wp.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Type:            corev1.ServiceTypeClusterIP,
			ClusterIP:       corev1.ClusterIPNone,
			SessionAffinity: corev1.ServiceAffinityNone,
			Selector:        selector,
			Ports: []corev1.ServicePort{
				{
					Name:       "mysql",
					Port:       3306,
					TargetPort: intstr.FromInt(3306),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

// buildMySQLStatefulSet renders the MySQL StatefulSet.
func buildMySQLStatefulSet(wp *appsv1alpha1.WordPress) (*appsv1.StatefulSet, error) {
	internal := wp.Spec.Database.Internal
	q, err := resource.ParseQuantity(internal.StorageSize)
	if err != nil {
		return nil, fmt.Errorf("invalid database.internal.storageSize %q: %w", internal.StorageSize, err)
	}

	labels := commonLabels(wp, ComponentMySQL)
	selector := selectorLabels(wp, ComponentMySQL)

	rootPwdEnv := corev1.EnvVar{
		Name: "MYSQL_ROOT_PASSWORD",
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: dbSecretName(wp)},
				Key:                  SecretKeyRoot,
			},
		},
	}
	if internal.RootPasswordSecret != nil {
		rootPwdEnv.ValueFrom = &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: internal.RootPasswordSecret.Name},
				Key:                  internal.RootPasswordSecret.Key,
			},
		}
	}

	envs := []corev1.EnvVar{
		rootPwdEnv,
		{Name: "MYSQL_DATABASE", Value: wp.Spec.Database.Name},
		{Name: "MYSQL_USER", Value: wp.Spec.Database.User},
		{
			Name: "MYSQL_PASSWORD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: dbSecretName(wp)},
					Key:                  SecretKeyWordPressPassword,
				},
			},
		},
	}

	pingCmd := []string{"sh", "-c", `mysqladmin ping -h 127.0.0.1 -uroot -p"$MYSQL_ROOT_PASSWORD"`}
	readinessProbe := &corev1.Probe{
		ProbeHandler:        corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: pingCmd}},
		InitialDelaySeconds: 30,
		PeriodSeconds:       10,
		TimeoutSeconds:      5,
		FailureThreshold:    3,
	}
	// Liveness is more lenient: give MySQL more time to recover before killing it.
	livenessProbe := &corev1.Probe{
		ProbeHandler:        corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: pingCmd}},
		InitialDelaySeconds: 60,
		PeriodSeconds:       20,
		TimeoutSeconds:      10,
		FailureThreshold:    6,
	}

	one := int32(1)
	ss := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dbStatefulSetName(wp),
			Namespace: wp.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: dbServiceName(wp),
			Replicas:    &one,
			Selector:    &metav1.LabelSelector{MatchLabels: selector},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					NodeSelector: internal.NodeSelector,
					Tolerations:  internal.Tolerations,
					Affinity:     internal.Affinity,
					Containers: []corev1.Container{
						{
							Name:            "mysql",
							Image:           internal.Image,
							ImagePullPolicy: internal.ImagePullPolicy,
							Ports: []corev1.ContainerPort{
								{Name: "mysql", ContainerPort: 3306, Protocol: corev1.ProtocolTCP},
							},
							Env:       envs,
							Resources: internal.Resources,
							VolumeMounts: []corev1.VolumeMount{
								{Name: "data", MountPath: "/var/lib/mysql"},
							},
							ReadinessProbe: readinessProbe,
							LivenessProbe:  livenessProbe,
						},
					},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "data"},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
						StorageClassName: internal.StorageClassName,
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: q,
							},
						},
					},
				},
			},
		},
	}
	return ss, nil
}

// resolvedDBHost returns the host the WordPress container should use.
func resolvedDBHost(wp *appsv1alpha1.WordPress) (host string, port int32) {
	if wp.Spec.Database.Mode == appsv1alpha1.DatabaseModeExternal {
		return wp.Spec.Database.External.Host, wp.Spec.Database.External.Port
	}
	return fmt.Sprintf("%s.%s.svc", dbServiceName(wp), wp.Namespace), 3306
}
