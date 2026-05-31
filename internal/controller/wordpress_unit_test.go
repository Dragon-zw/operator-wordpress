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
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	appsv1alpha1 "github.com/georgezhong/wordpress-operator/api/v1alpha1"
)

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := scheme.Scheme
	if err := appsv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	return s
}

func newReconciler(t *testing.T, objs ...client.Object) (*WordPressReconciler, client.Client) {
	t.Helper()
	s := newTestScheme(t)
	cl := fakeclient.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&appsv1alpha1.WordPress{}).
		Build()
	return &WordPressReconciler{
		Client:   cl,
		Scheme:   s,
		Recorder: record.NewFakeRecorder(32),
	}, cl
}

// reconcileUntilStable calls Reconcile until the finalizer is registered and
// the reconcile loop reaches the resource-creation phase. In unit tests the
// first call only adds the finalizer and returns; the second call does the work.
func reconcileUntilStable(t *testing.T, r *WordPressReconciler, req ctrl.Request) (ctrl.Result, error) {
	t.Helper()
	ctx := context.Background()
	// First call: adds finalizer.
	if _, err := r.Reconcile(ctx, req); err != nil {
		return ctrl.Result{}, err
	}
	// Second call: real reconciliation.
	return r.Reconcile(ctx, req)
}

func TestApplyDefaults(t *testing.T) {
	wp := &appsv1alpha1.WordPress{}
	applyDefaults(wp)

	if wp.Spec.Image != "wordpress:6.5-apache" {
		t.Errorf("default image not set, got %q", wp.Spec.Image)
	}
	if wp.Spec.Replicas == nil || *wp.Spec.Replicas != 1 {
		t.Errorf("default replicas not 1: %+v", wp.Spec.Replicas)
	}
	if wp.Spec.Service.Type != corev1.ServiceTypeClusterIP {
		t.Errorf("default service type wrong: %v", wp.Spec.Service.Type)
	}
	if wp.Spec.Database.Mode != appsv1alpha1.DatabaseModeInternal {
		t.Errorf("default database mode wrong: %v", wp.Spec.Database.Mode)
	}
	if wp.Spec.Database.Internal == nil ||
		wp.Spec.Database.Internal.Image != "mysql:8.0" {
		t.Errorf("default internal image not set: %+v", wp.Spec.Database.Internal)
	}
}

func TestValidateSpec(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(wp *appsv1alpha1.WordPress)
		wantErr bool
	}{
		{
			name:    "internal default ok",
			mutate:  func(wp *appsv1alpha1.WordPress) {},
			wantErr: false,
		},
		{
			name: "external missing host",
			mutate: func(wp *appsv1alpha1.WordPress) {
				wp.Spec.Database.Mode = appsv1alpha1.DatabaseModeExternal
				wp.Spec.Database.External = &appsv1alpha1.ExternalDatabaseSpec{
					PasswordSecret: appsv1alpha1.SecretKeyRef{Name: "s", Key: "k"},
				}
			},
			wantErr: true,
		},
		{
			name: "external complete",
			mutate: func(wp *appsv1alpha1.WordPress) {
				wp.Spec.Database.Mode = appsv1alpha1.DatabaseModeExternal
				wp.Spec.Database.External = &appsv1alpha1.ExternalDatabaseSpec{
					Host:           "db.example.com",
					Port:           3306,
					PasswordSecret: appsv1alpha1.SecretKeyRef{Name: "s", Key: "k"},
				}
			},
			wantErr: false,
		},
		{
			name: "ingress without host",
			mutate: func(wp *appsv1alpha1.WordPress) {
				wp.Spec.Ingress.Enabled = true
			},
			wantErr: true,
		},
		{
			name: "autoscaling max < min",
			mutate: func(wp *appsv1alpha1.WordPress) {
				wp.Spec.Autoscaling.Enabled = true
				wp.Spec.Autoscaling.MinReplicas = 5
				wp.Spec.Autoscaling.MaxReplicas = 2
			},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wp := &appsv1alpha1.WordPress{
				ObjectMeta: metav1.ObjectMeta{Name: "wp", Namespace: "ns"},
			}
			tc.mutate(wp)
			applyDefaults(wp)
			err := validateSpec(wp)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestReconcileInternalCreatesAllResources(t *testing.T) {
	wp := &appsv1alpha1.WordPress{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "blog",
			Namespace:  "demo",
			Generation: 1,
		},
		Spec: appsv1alpha1.WordPressSpec{
			Persistence: appsv1alpha1.PersistenceSpec{Enabled: true},
		},
	}
	r, cl := newReconciler(t, wp)

	ctx := context.Background()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "blog", Namespace: "demo"}}
	res, err := reconcileUntilStable(t, r, req)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.RequeueAfter == 0 && !res.Requeue {
		t.Logf("no requeue requested, res=%+v", res)
	}

	// Secret created
	sec := &corev1.Secret{}
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "demo", Name: "blog-mysql-credentials"}, sec); err != nil {
		t.Fatalf("expected mysql secret, got: %v", err)
	}
	if _, ok := sec.Data[SecretKeyRoot]; !ok {
		// Secret created via StringData. Fake client typically materialises Data. Check StringData fallback.
		if _, ok2 := sec.StringData[SecretKeyRoot]; !ok2 {
			t.Fatalf("missing root password key, got data=%v stringData=%v", sec.Data, sec.StringData)
		}
	}

	// Headless service
	svc := &corev1.Service{}
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "demo", Name: "blog-mysql"}, svc); err != nil {
		t.Fatalf("expected mysql svc: %v", err)
	}
	if svc.Spec.ClusterIP != corev1.ClusterIPNone {
		t.Errorf("mysql service should be headless, got %q", svc.Spec.ClusterIP)
	}

	// StatefulSet
	ss := &appsv1.StatefulSet{}
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "demo", Name: "blog-mysql"}, ss); err != nil {
		t.Fatalf("expected mysql sts: %v", err)
	}
	if got := ss.Spec.Template.Spec.Containers[0].Image; got != "mysql:8.0" {
		t.Errorf("mysql image = %q", got)
	}

	// PVC
	pvc := &corev1.PersistentVolumeClaim{}
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "demo", Name: "blog-content"}, pvc); err != nil {
		t.Fatalf("expected wp pvc: %v", err)
	}

	// Deployment
	dep := &appsv1.Deployment{}
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "demo", Name: "blog"}, dep); err != nil {
		t.Fatalf("expected wp deployment: %v", err)
	}
	containers := dep.Spec.Template.Spec.Containers
	if len(containers) != 1 || containers[0].Image == "" {
		t.Fatalf("unexpected deployment containers: %+v", containers)
	}
	envMap := map[string]corev1.EnvVar{}
	for _, e := range containers[0].Env {
		envMap[e.Name] = e
	}
	if envMap["WORDPRESS_DB_HOST"].Value != "blog-mysql.demo.svc:3306" {
		t.Errorf("unexpected DB host env: %+v", envMap["WORDPRESS_DB_HOST"])
	}
	if envMap["WORDPRESS_DB_PASSWORD"].ValueFrom == nil {
		t.Errorf("DB password env should reference secret")
	}

	// WordPress service
	wpSvc := &corev1.Service{}
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "demo", Name: "blog"}, wpSvc); err != nil {
		t.Fatalf("expected wp svc: %v", err)
	}

	// No HPA / Ingress when disabled
	hpa := &autoscalingv2.HorizontalPodAutoscaler{}
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "demo", Name: "blog-hpa"}, hpa); err == nil {
		t.Errorf("HPA should not exist when autoscaling disabled")
	}
	ing := &networkingv1.Ingress{}
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "demo", Name: "blog"}, ing); err == nil {
		t.Errorf("Ingress should not exist when disabled")
	}

	// Status updated to Provisioning since deployment has no ready replicas yet.
	got := &appsv1alpha1.WordPress{}
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "demo", Name: "blog"}, got); err != nil {
		t.Fatalf("get wp: %v", err)
	}
	if got.Status.Phase != appsv1alpha1.PhaseProvisioning {
		t.Errorf("expected phase Provisioning, got %q", got.Status.Phase)
	}
}

func TestReconcileExternalDatabaseRequiresSecret(t *testing.T) {
	wp := &appsv1alpha1.WordPress{
		ObjectMeta: metav1.ObjectMeta{Name: "ext", Namespace: "demo", Generation: 1},
		Spec: appsv1alpha1.WordPressSpec{
			Database: appsv1alpha1.DatabaseSpec{
				Mode: appsv1alpha1.DatabaseModeExternal,
				External: &appsv1alpha1.ExternalDatabaseSpec{
					Host: "db.svc",
					Port: 3306,
					PasswordSecret: appsv1alpha1.SecretKeyRef{
						Name: "missing-secret", Key: "password",
					},
				},
			},
		},
	}
	r, cl := newReconciler(t, wp)

	_, err := reconcileUntilStable(t, r, ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "ext", Namespace: "demo"},
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// Should not create deployment until secret available; reconcile returns
	// quickly with a condition set.
	dep := &appsv1.Deployment{}
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "demo", Name: "ext"}, dep); err == nil {
		t.Fatalf("deployment should not exist when external secret missing")
	}

	got := &appsv1alpha1.WordPress{}
	_ = cl.Get(context.Background(), types.NamespacedName{Namespace: "demo", Name: "ext"}, got)
	found := false
	for _, c := range got.Status.Conditions {
		if c.Type == appsv1alpha1.ConditionTypeDatabaseReady &&
			c.Reason == "ExternalSecretMissing" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ExternalSecretMissing condition; got %+v", got.Status.Conditions)
	}
}

func TestReconcileExternalDatabaseHappyPath(t *testing.T) {
	wp := &appsv1alpha1.WordPress{
		ObjectMeta: metav1.ObjectMeta{Name: "ext", Namespace: "demo", Generation: 1},
		Spec: appsv1alpha1.WordPressSpec{
			Database: appsv1alpha1.DatabaseSpec{
				Mode: appsv1alpha1.DatabaseModeExternal,
				External: &appsv1alpha1.ExternalDatabaseSpec{
					Host: "db.svc",
					Port: 3306,
					PasswordSecret: appsv1alpha1.SecretKeyRef{
						Name: "ext-pwd", Key: "password",
					},
				},
			},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "ext-pwd", Namespace: "demo"},
		StringData: map[string]string{"password": "p"},
	}
	r, cl := newReconciler(t, wp, secret)

	_, err := reconcileUntilStable(t, r, ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "ext", Namespace: "demo"},
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// No internal MySQL StatefulSet should have been created.
	ss := &appsv1.StatefulSet{}
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "demo", Name: "ext-mysql"}, ss); err == nil {
		t.Errorf("external mode should not create internal StatefulSet")
	}

	dep := &appsv1.Deployment{}
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "demo", Name: "ext"}, dep); err != nil {
		t.Fatalf("expected wp deployment: %v", err)
	}
	envMap := map[string]corev1.EnvVar{}
	for _, e := range dep.Spec.Template.Spec.Containers[0].Env {
		envMap[e.Name] = e
	}
	if got := envMap["WORDPRESS_DB_HOST"].Value; got != "db.svc:3306" {
		t.Errorf("expected WORDPRESS_DB_HOST=db.svc:3306, got %q", got)
	}
	if envMap["WORDPRESS_DB_PASSWORD"].ValueFrom == nil ||
		envMap["WORDPRESS_DB_PASSWORD"].ValueFrom.SecretKeyRef.Name != "ext-pwd" {
		t.Errorf("password env should reference user-provided secret")
	}
}

func TestReconcileIngressAndHPALifecycle(t *testing.T) {
	host := "blog.example.com"
	wp := &appsv1alpha1.WordPress{
		ObjectMeta: metav1.ObjectMeta{Name: "blog", Namespace: "demo", Generation: 1},
		Spec: appsv1alpha1.WordPressSpec{
			Ingress: appsv1alpha1.IngressSpec{
				Enabled: true,
				Host:    host,
			},
			Autoscaling: appsv1alpha1.AutoscalingSpec{
				Enabled:                        true,
				MinReplicas:                    2,
				MaxReplicas:                    4,
				TargetCPUUtilizationPercentage: 60,
			},
		},
	}
	r, cl := newReconciler(t, wp)
	ctx := context.Background()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "blog", Namespace: "demo"}}
	if _, err := reconcileUntilStable(t, r, req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	ing := &networkingv1.Ingress{}
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "demo", Name: "blog"}, ing); err != nil {
		t.Fatalf("expected ingress: %v", err)
	}
	if ing.Spec.Rules[0].Host != host {
		t.Errorf("unexpected host: %q", ing.Spec.Rules[0].Host)
	}

	hpa := &autoscalingv2.HorizontalPodAutoscaler{}
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "demo", Name: "blog-hpa"}, hpa); err != nil {
		t.Fatalf("expected hpa: %v", err)
	}
	if hpa.Spec.MaxReplicas != 4 {
		t.Errorf("unexpected hpa max: %d", hpa.Spec.MaxReplicas)
	}

	// Toggle off; reconcile should remove them.
	current := &appsv1alpha1.WordPress{}
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "demo", Name: "blog"}, current); err != nil {
		t.Fatalf("get: %v", err)
	}
	current.Spec.Ingress.Enabled = false
	current.Spec.Autoscaling.Enabled = false
	if err := cl.Update(ctx, current); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := r.Reconcile(ctx, ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "blog", Namespace: "demo"},
	}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "demo", Name: "blog"}, &networkingv1.Ingress{}); err == nil {
		t.Errorf("ingress should be deleted")
	}
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "demo", Name: "blog-hpa"}, &autoscalingv2.HorizontalPodAutoscaler{}); err == nil {
		t.Errorf("hpa should be deleted")
	}
}

func TestFinalizerAddedOnCreate(t *testing.T) {
	wp := &appsv1alpha1.WordPress{
		ObjectMeta: metav1.ObjectMeta{Name: "blog", Namespace: "demo", Generation: 1},
	}
	r, cl := newReconciler(t, wp)
	ctx := context.Background()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "blog", Namespace: "demo"}}

	// First reconcile should add the finalizer.
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := &appsv1alpha1.WordPress{}
	if err := cl.Get(ctx, req.NamespacedName, got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if !contains(got.Finalizers, WordPressFinalizer) {
		t.Errorf("finalizer not set; finalizers=%v", got.Finalizers)
	}
}

func TestFinalizerDeletesMySQLPVC(t *testing.T) {
	now := metav1.Now()
	wp := &appsv1alpha1.WordPress{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "blog",
			Namespace:         "demo",
			DeletionTimestamp: &now,
			Finalizers:        []string{WordPressFinalizer},
		},
	}
	applyDefaults(wp)
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "data-blog-mysql-0", Namespace: "demo"},
	}
	r, cl := newReconciler(t, wp, pvc)
	ctx := context.Background()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "blog", Namespace: "demo"}}

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// MySQL PVC should be deleted.
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "demo", Name: "data-blog-mysql-0"}, &corev1.PersistentVolumeClaim{}); err == nil {
		t.Errorf("MySQL PVC should have been deleted")
	}
	// WordPress CR is gone (finalizer removed → object GC'd by fake client).
	got := &appsv1alpha1.WordPress{}
	if err := cl.Get(ctx, req.NamespacedName, got); err == nil {
		// If it still exists, the finalizer must have been removed.
		if contains(got.Finalizers, WordPressFinalizer) {
			t.Errorf("finalizer should be removed after deletion")
		}
	}
}

func TestFinalizerRetainsDataWhenAnnotated(t *testing.T) {
	now := metav1.Now()
	wp := &appsv1alpha1.WordPress{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "blog",
			Namespace:         "demo",
			DeletionTimestamp: &now,
			Finalizers:        []string{WordPressFinalizer},
			Annotations:       map[string]string{RetainDataAnnotation: "true"},
		},
	}
	applyDefaults(wp)
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "data-blog-mysql-0", Namespace: "demo"},
	}
	r, cl := newReconciler(t, wp, pvc)
	ctx := context.Background()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "blog", Namespace: "demo"}}

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// MySQL PVC should NOT be deleted when retain-data annotation is set.
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "demo", Name: "data-blog-mysql-0"}, &corev1.PersistentVolumeClaim{}); err != nil {
		t.Errorf("MySQL PVC should be retained when annotation set: %v", err)
	}
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
