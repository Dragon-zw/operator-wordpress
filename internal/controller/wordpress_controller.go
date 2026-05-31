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
	"fmt"
	"reflect"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	appsv1alpha1 "github.com/georgezhong/wordpress-operator/api/v1alpha1"
)

// WordPressReconciler reconciles a WordPress object.
type WordPressReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=apps.kubesphere.ai,resources=wordpresses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps.kubesphere.ai,resources=wordpresses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps.kubesphere.ai,resources=wordpresses/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments;statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services;persistentvolumeclaims;secrets;configmaps;events,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete

// Reconcile drives the WordPress object toward its desired state.
func (r *WordPressReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("wordpress", req.NamespacedName)

	wp := &appsv1alpha1.WordPress{}
	if err := r.Get(ctx, req.NamespacedName, wp); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion before applying defaults or validating spec.
	if !wp.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, wp)
	}

	// Ensure the finalizer is registered so we can clean up MySQL PVCs on deletion.
	if !controllerutil.ContainsFinalizer(wp, WordPressFinalizer) {
		controllerutil.AddFinalizer(wp, WordPressFinalizer)
		if err := r.Update(ctx, wp); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Apply defaults that are easier in code than via the schema. This is
	// purely in-memory; we never write the defaulted spec back.
	applyDefaults(wp)

	// Fast-path validation.
	if err := validateSpec(wp); err != nil {
		log.Error(err, "invalid spec")
		r.event(wp, corev1.EventTypeWarning, "InvalidSpec", err.Error())
		_ = r.markFailed(ctx, wp, "InvalidSpec", err.Error())
		// Don't requeue on validation errors; user must fix the spec.
		return ctrl.Result{}, nil
	}

	// Reconcile owned resources.
	res, err := r.reconcileDatabase(ctx, wp)
	if err != nil {
		// Persist any condition we set before bailing.
		_ = r.persistStatus(ctx, wp)
		return res, ignoreConflict(err)
	}
	if !res.IsZero() {
		// Persist conditions even when we requeue without progressing.
		if statusErr := r.persistStatus(ctx, wp); statusErr != nil {
			return res, statusErr
		}
		return res, nil
	}

	if err := r.reconcilePVC(ctx, wp); err != nil {
		return ctrl.Result{}, ignoreConflict(err)
	}
	if err := r.reconcileWordPressDeployment(ctx, wp); err != nil {
		return ctrl.Result{}, ignoreConflict(err)
	}
	if err := r.reconcileWordPressService(ctx, wp); err != nil {
		return ctrl.Result{}, ignoreConflict(err)
	}
	if err := r.reconcileIngress(ctx, wp); err != nil {
		return ctrl.Result{}, ignoreConflict(err)
	}
	if err := r.reconcileHPA(ctx, wp); err != nil {
		return ctrl.Result{}, ignoreConflict(err)
	}

	// Recompute status.
	if err := r.updateStatus(ctx, wp); err != nil {
		return ctrl.Result{}, ignoreConflict(err)
	}

	// If we're not yet Ready, requeue periodically to react to pod readiness.
	if wp.Status.Phase != appsv1alpha1.PhaseReady {
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}
	return ctrl.Result{}, nil
}

// validateSpec rejects spec combinations that cannot be reconciled.
func validateSpec(wp *appsv1alpha1.WordPress) error {
	switch wp.Spec.Database.Mode {
	case appsv1alpha1.DatabaseModeExternal:
		if wp.Spec.Database.External == nil {
			return fmt.Errorf("database.mode=External requires database.external")
		}
		if wp.Spec.Database.External.Host == "" {
			return fmt.Errorf("database.external.host must be set")
		}
		if wp.Spec.Database.External.PasswordSecret.Name == "" ||
			wp.Spec.Database.External.PasswordSecret.Key == "" {
			return fmt.Errorf("database.external.passwordSecret must reference a Secret")
		}
	case appsv1alpha1.DatabaseModeInternal:
		// Internal is auto-defaulted; nothing to require.
	default:
		return fmt.Errorf("unsupported database.mode %q", wp.Spec.Database.Mode)
	}

	if wp.Spec.Autoscaling.Enabled {
		if wp.Spec.Autoscaling.MaxReplicas < wp.Spec.Autoscaling.MinReplicas {
			return fmt.Errorf("autoscaling.maxReplicas must be >= autoscaling.minReplicas")
		}
	}

	if wp.Spec.Ingress.Enabled && wp.Spec.Ingress.Host == "" {
		return fmt.Errorf("ingress.enabled=true requires ingress.host")
	}
	return nil
}

// reconcileDatabase creates/updates the operator-managed MySQL when in
// Internal mode. For External mode it only verifies the user secret exists.
func (r *WordPressReconciler) reconcileDatabase(
	ctx context.Context, wp *appsv1alpha1.WordPress,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if wp.Spec.Database.Mode == appsv1alpha1.DatabaseModeExternal {
		// Verify the user secret exists; surface the failure clearly otherwise.
		s := &corev1.Secret{}
		err := r.Get(ctx, types.NamespacedName{
			Namespace: wp.Namespace,
			Name:      wp.Spec.Database.External.PasswordSecret.Name,
		}, s)
		if err != nil {
			if apierrors.IsNotFound(err) {
				meta.SetStatusCondition(&wp.Status.Conditions, metav1.Condition{
					Type:    appsv1alpha1.ConditionTypeDatabaseReady,
					Status:  metav1.ConditionFalse,
					Reason:  "ExternalSecretMissing",
					Message: fmt.Sprintf("Secret %q not found", wp.Spec.Database.External.PasswordSecret.Name),
				})
				r.event(wp, corev1.EventTypeWarning, "ExternalSecretMissing",
					fmt.Sprintf("Secret %q not found", wp.Spec.Database.External.PasswordSecret.Name))
				return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
			}
			return ctrl.Result{}, err
		}
		meta.SetStatusCondition(&wp.Status.Conditions, metav1.Condition{
			Type:    appsv1alpha1.ConditionTypeDatabaseReady,
			Status:  metav1.ConditionTrue,
			Reason:  "ExternalDatabase",
			Message: "Using external database",
		})
		return ctrl.Result{}, nil
	}

	// Internal mode: ensure Secret exists (don't rotate).
	desiredSecret, err := buildMySQLSecret(wp)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := controllerutil.SetControllerReference(wp, desiredSecret, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}
	existingSecret := &corev1.Secret{}
	err = r.Get(ctx, client.ObjectKeyFromObject(desiredSecret), existingSecret)
	if apierrors.IsNotFound(err) {
		log.Info("creating MySQL credentials secret", "name", desiredSecret.Name)
		if err := r.Create(ctx, desiredSecret); err != nil {
			return ctrl.Result{}, err
		}
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// Headless service.
	desiredSvc := buildMySQLHeadlessService(wp)
	if err := controllerutil.SetControllerReference(wp, desiredSvc, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.applyService(ctx, desiredSvc); err != nil {
		return ctrl.Result{}, err
	}

	// StatefulSet.
	desiredSS, err := buildMySQLStatefulSet(wp)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := controllerutil.SetControllerReference(wp, desiredSS, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}

	existingSS := &appsv1.StatefulSet{}
	err = r.Get(ctx, client.ObjectKeyFromObject(desiredSS), existingSS)
	if apierrors.IsNotFound(err) {
		log.Info("creating MySQL StatefulSet", "name", desiredSS.Name)
		if err := r.Create(ctx, desiredSS); err != nil {
			return ctrl.Result{}, err
		}
	} else if err != nil {
		return ctrl.Result{}, err
	} else {
		// Update mutable fields. VolumeClaimTemplates are immutable in StatefulSet,
		// so we only touch the pod template + replicas.
		patch := existingSS.DeepCopy()
		patch.Spec.Replicas = desiredSS.Spec.Replicas
		patch.Spec.Template = desiredSS.Spec.Template
		if !reflect.DeepEqual(existingSS.Spec.Template, patch.Spec.Template) ||
			!equalInt32Ptr(existingSS.Spec.Replicas, patch.Spec.Replicas) {
			if err := r.Update(ctx, patch); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	// Database readiness.
	if existingSS.Status.ReadyReplicas >= 1 {
		meta.SetStatusCondition(&wp.Status.Conditions, metav1.Condition{
			Type:    appsv1alpha1.ConditionTypeDatabaseReady,
			Status:  metav1.ConditionTrue,
			Reason:  "MySQLReady",
			Message: "MySQL StatefulSet has at least one ready replica",
		})
		return ctrl.Result{}, nil
	}

	meta.SetStatusCondition(&wp.Status.Conditions, metav1.Condition{
		Type:    appsv1alpha1.ConditionTypeDatabaseReady,
		Status:  metav1.ConditionFalse,
		Reason:  "MySQLNotReady",
		Message: "Waiting for MySQL StatefulSet to become ready",
	})
	// Don't return early: still create WP resources so users can observe progress.
	return ctrl.Result{}, nil
}

func (r *WordPressReconciler) reconcilePVC(
	ctx context.Context, wp *appsv1alpha1.WordPress,
) error {
	if !wp.Spec.Persistence.Enabled {
		return nil
	}
	desired, err := buildWordPressPVC(wp)
	if err != nil {
		return err
	}
	if err := controllerutil.SetControllerReference(wp, desired, r.Scheme); err != nil {
		return err
	}
	existing := &corev1.PersistentVolumeClaim{}
	err = r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	// PVCs are largely immutable; we don't try to resize automatically.
	return err
}

func (r *WordPressReconciler) reconcileWordPressDeployment(
	ctx context.Context, wp *appsv1alpha1.WordPress,
) error {
	host, port := resolvedDBHost(wp)
	wp.Status.DatabaseHost = host

	var dbEnv []corev1.EnvVar
	if wp.Spec.Database.Mode == appsv1alpha1.DatabaseModeInternal {
		dbEnv = envFromInternalDBSecret(dbSecretName(wp))
	} else {
		dbEnv = envFromExternalDBSecret(wp.Spec.Database.External.PasswordSecret)
	}

	desired := buildWordPressDeployment(wp, host, port, dbEnv)
	if err := controllerutil.SetControllerReference(wp, desired, r.Scheme); err != nil {
		return err
	}

	existing := &appsv1.Deployment{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	patch := existing.DeepCopy()
	patch.Labels = desired.Labels
	patch.Spec.Replicas = desired.Spec.Replicas
	patch.Spec.Strategy = desired.Spec.Strategy
	patch.Spec.Template = desired.Spec.Template
	if !deploymentEqual(existing, patch) {
		return r.Update(ctx, patch)
	}
	return nil
}

func (r *WordPressReconciler) reconcileWordPressService(
	ctx context.Context, wp *appsv1alpha1.WordPress,
) error {
	desired := buildWordPressService(wp)
	if err := controllerutil.SetControllerReference(wp, desired, r.Scheme); err != nil {
		return err
	}
	return r.applyService(ctx, desired)
}

// applyService creates the service or updates the mutable fields, preserving
// the cluster-assigned ClusterIP.
func (r *WordPressReconciler) applyService(ctx context.Context, desired *corev1.Service) error {
	existing := &corev1.Service{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}
	patch := existing.DeepCopy()
	patch.Labels = desired.Labels
	patch.Annotations = desired.Annotations
	patch.Spec.Type = desired.Spec.Type
	patch.Spec.Selector = desired.Spec.Selector
	patch.Spec.SessionAffinity = desired.Spec.SessionAffinity
	// Preserve ports we don't manage; replace the named ports we own.
	patch.Spec.Ports = desired.Spec.Ports
	if existing.Spec.ClusterIP == corev1.ClusterIPNone {
		patch.Spec.ClusterIP = corev1.ClusterIPNone
	}
	if !reflect.DeepEqual(existing.Spec, patch.Spec) ||
		!reflect.DeepEqual(existing.Labels, patch.Labels) ||
		!reflect.DeepEqual(existing.Annotations, patch.Annotations) {
		return r.Update(ctx, patch)
	}
	return nil
}

func (r *WordPressReconciler) reconcileIngress(
	ctx context.Context, wp *appsv1alpha1.WordPress,
) error {
	existing := &networkingv1.Ingress{}
	key := types.NamespacedName{Namespace: wp.Namespace, Name: wpName(wp)}

	if !wp.Spec.Ingress.Enabled {
		err := r.Get(ctx, key, existing)
		if apierrors.IsNotFound(err) {
			meta.RemoveStatusCondition(&wp.Status.Conditions, appsv1alpha1.ConditionTypeIngressConfigured)
			return nil
		}
		if err != nil {
			return err
		}
		// Only delete if we own it.
		if metav1.IsControlledBy(existing, wp) {
			return r.Delete(ctx, existing)
		}
		return nil
	}

	desired := buildWordPressIngress(wp)
	if err := controllerutil.SetControllerReference(wp, desired, r.Scheme); err != nil {
		return err
	}
	err := r.Get(ctx, key, existing)
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, desired); err != nil {
			return err
		}
		meta.SetStatusCondition(&wp.Status.Conditions, metav1.Condition{
			Type:    appsv1alpha1.ConditionTypeIngressConfigured,
			Status:  metav1.ConditionTrue,
			Reason:  "Created",
			Message: "Ingress created",
		})
		return nil
	}
	if err != nil {
		return err
	}

	patch := existing.DeepCopy()
	patch.Labels = desired.Labels
	patch.Annotations = desired.Annotations
	patch.Spec = desired.Spec
	if !reflect.DeepEqual(existing.Spec, patch.Spec) ||
		!reflect.DeepEqual(existing.Labels, patch.Labels) ||
		!reflect.DeepEqual(existing.Annotations, patch.Annotations) {
		if err := r.Update(ctx, patch); err != nil {
			return err
		}
	}
	meta.SetStatusCondition(&wp.Status.Conditions, metav1.Condition{
		Type:    appsv1alpha1.ConditionTypeIngressConfigured,
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciled",
		Message: "Ingress is up-to-date",
	})
	return nil
}

func (r *WordPressReconciler) reconcileHPA(
	ctx context.Context, wp *appsv1alpha1.WordPress,
) error {
	existing := &autoscalingv2.HorizontalPodAutoscaler{}
	key := types.NamespacedName{Namespace: wp.Namespace, Name: hpaName(wp)}

	if !wp.Spec.Autoscaling.Enabled {
		err := r.Get(ctx, key, existing)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if metav1.IsControlledBy(existing, wp) {
			return r.Delete(ctx, existing)
		}
		return nil
	}
	desired := buildWordPressHPA(wp)
	if err := controllerutil.SetControllerReference(wp, desired, r.Scheme); err != nil {
		return err
	}
	err := r.Get(ctx, key, existing)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}
	patch := existing.DeepCopy()
	patch.Labels = desired.Labels
	patch.Spec = desired.Spec
	if !reflect.DeepEqual(existing.Spec, patch.Spec) ||
		!reflect.DeepEqual(existing.Labels, patch.Labels) {
		return r.Update(ctx, patch)
	}
	return nil
}

func (r *WordPressReconciler) updateStatus(
	ctx context.Context, wp *appsv1alpha1.WordPress,
) error {
	dep := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: wp.Namespace, Name: wpName(wp)}, dep); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
	}
	wp.Status.Replicas = dep.Status.Replicas
	wp.Status.ReadyReplicas = dep.Status.ReadyReplicas
	wp.Status.ObservedGeneration = wp.Generation

	depReady := dep.Status.ReadyReplicas > 0 && dep.Status.ReadyReplicas == dep.Status.Replicas
	if depReady {
		meta.SetStatusCondition(&wp.Status.Conditions, metav1.Condition{
			Type:    appsv1alpha1.ConditionTypeDeploymentReady,
			Status:  metav1.ConditionTrue,
			Reason:  "AllReplicasReady",
			Message: "All WordPress replicas are ready",
		})
	} else {
		meta.SetStatusCondition(&wp.Status.Conditions, metav1.Condition{
			Type:    appsv1alpha1.ConditionTypeDeploymentReady,
			Status:  metav1.ConditionFalse,
			Reason:  "Progressing",
			Message: fmt.Sprintf("%d/%d replicas ready", dep.Status.ReadyReplicas, dep.Status.Replicas),
		})
	}

	dbReady := meta.IsStatusConditionTrue(wp.Status.Conditions, appsv1alpha1.ConditionTypeDatabaseReady)
	switch {
	case dbReady && depReady:
		wp.Status.Phase = appsv1alpha1.PhaseReady
		meta.SetStatusCondition(&wp.Status.Conditions, metav1.Condition{
			Type:    appsv1alpha1.ConditionTypeReady,
			Status:  metav1.ConditionTrue,
			Reason:  "AllReady",
			Message: "WordPress and its database are ready",
		})
	default:
		wp.Status.Phase = appsv1alpha1.PhaseProvisioning
		meta.SetStatusCondition(&wp.Status.Conditions, metav1.Condition{
			Type:    appsv1alpha1.ConditionTypeReady,
			Status:  metav1.ConditionFalse,
			Reason:  "Progressing",
			Message: "Resources are still being provisioned",
		})
	}

	wp.Status.URL = computeURL(wp)
	return r.persistStatus(ctx, wp)
}

// handleDeletion runs cleanup before the WordPress CR is removed from the API server.
// StatefulSet VolumeClaimTemplates PVCs are not garbage collected by Kubernetes when
// the owning StatefulSet is deleted, so we delete them explicitly here.
// Annotate the CR with wordpresses.apps.kubesphere.ai/retain-data="true" to skip
// MySQL PVC deletion and keep the data for manual recovery.
func (r *WordPressReconciler) handleDeletion(ctx context.Context, wp *appsv1alpha1.WordPress) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(wp, WordPressFinalizer) {
		return ctrl.Result{}, nil
	}

	retain := wp.Annotations[RetainDataAnnotation] == "true"

	if wp.Spec.Database.Mode == appsv1alpha1.DatabaseModeInternal && !retain {
		pvcName := fmt.Sprintf("data-%s-0", dbStatefulSetName(wp))
		pvc := &corev1.PersistentVolumeClaim{}
		err := r.Get(ctx, types.NamespacedName{Namespace: wp.Namespace, Name: pvcName}, pvc)
		if err == nil {
			log.Info("deleting MySQL data PVC", "pvc", pvcName)
			r.event(wp, corev1.EventTypeNormal, "DeletingMySQLPVC", fmt.Sprintf("Deleting MySQL data PVC %q", pvcName))
			if delErr := r.Delete(ctx, pvc); delErr != nil && !apierrors.IsNotFound(delErr) {
				return ctrl.Result{}, delErr
			}
		} else if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	} else if retain {
		log.Info("skipping MySQL PVC deletion (retain-data annotation set)", "pvc", fmt.Sprintf("data-%s-0", dbStatefulSetName(wp)))
		r.event(wp, corev1.EventTypeNormal, "RetainingMySQLPVC", "MySQL data PVC retained due to retain-data annotation")
	}

	controllerutil.RemoveFinalizer(wp, WordPressFinalizer)
	if err := r.Update(ctx, wp); err != nil {
		return ctrl.Result{}, ignoreConflict(err)
	}
	return ctrl.Result{}, nil
}

func (r *WordPressReconciler) markFailed(ctx context.Context, wp *appsv1alpha1.WordPress, reason, msg string) error {
	wp.Status.Phase = appsv1alpha1.PhaseFailed
	meta.SetStatusCondition(&wp.Status.Conditions, metav1.Condition{
		Type:    appsv1alpha1.ConditionTypeReady,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: msg,
	})
	return r.persistStatus(ctx, wp)
}

// persistStatus writes the in-memory status to the API server, retrying on
// conflicts by re-fetching the latest version, copying our desired status
// onto it, and re-trying. This keeps reconcile idempotent under contention
// from the scale subresource and concurrent reconciles.
func (r *WordPressReconciler) persistStatus(ctx context.Context, wp *appsv1alpha1.WordPress) error {
	desired := wp.Status.DeepCopy()
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &appsv1alpha1.WordPress{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(wp), latest); err != nil {
			return err
		}
		latest.Status = *desired
		return r.Status().Update(ctx, latest)
	})
}

// ignoreConflict drops update-conflict errors. Such conflicts are normal
// under concurrent reconciles and the controller will requeue automatically;
// surfacing them as ERRORs only adds log noise without aiding debugging.
func ignoreConflict(err error) error {
	if apierrors.IsConflict(err) {
		return nil
	}
	return err
}

func (r *WordPressReconciler) event(wp *appsv1alpha1.WordPress, eventType, reason, msg string) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Event(wp, eventType, reason, msg)
}

func computeURL(wp *appsv1alpha1.WordPress) string {
	if wp.Spec.SiteURL != "" {
		return wp.Spec.SiteURL
	}
	if wp.Spec.Ingress.Enabled && wp.Spec.Ingress.Host != "" {
		scheme := "http"
		if wp.Spec.Ingress.TLSSecretName != "" {
			scheme = "https"
		}
		return fmt.Sprintf("%s://%s", scheme, wp.Spec.Ingress.Host)
	}
	return ""
}

// SetupWithManager sets up the controller with the Manager.
func (r *WordPressReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Recorder == nil {
		r.Recorder = mgr.GetEventRecorderFor("wordpress-controller")
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1alpha1.WordPress{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&appsv1.Deployment{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&networkingv1.Ingress{}).
		Owns(&autoscalingv2.HorizontalPodAutoscaler{}).
		Named("wordpress").
		Complete(r)
}

// equalInt32Ptr returns true when two int32 pointers represent the same value
// (including both being nil).
func equalInt32Ptr(a, b *int32) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// deploymentEqual returns true when the desired Deployment matches the existing
// one in the fields we manage. Status, ObjectMeta annotations like
// "deployment.kubernetes.io/revision" must not trigger updates.
func deploymentEqual(existing, desired *appsv1.Deployment) bool {
	if !reflect.DeepEqual(existing.Labels, desired.Labels) {
		return false
	}
	if !equalInt32Ptr(existing.Spec.Replicas, desired.Spec.Replicas) {
		return false
	}
	if !reflect.DeepEqual(existing.Spec.Strategy, desired.Spec.Strategy) {
		return false
	}
	if !reflect.DeepEqual(existing.Spec.Template, desired.Spec.Template) {
		return false
	}
	return true
}
