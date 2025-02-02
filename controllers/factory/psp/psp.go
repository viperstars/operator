package psp

import (
	"context"
	"fmt"

	v1beta12 "github.com/VictoriaMetrics/operator/api/v1beta1"

	"github.com/VictoriaMetrics/operator/controllers/factory/k8stools"
	v1 "k8s.io/api/core/v1"
	"k8s.io/api/policy/v1beta1"
	v12 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type CRDObject interface {
	Annotations() map[string]string
	Labels() map[string]string
	PrefixedName() string
	GetServiceAccountName() string
	GetPSPName() string
	GetNSName() string
}

// CreateOrUpdateServiceAccountWithPSP - creates psp for api object.
// ensure that ServiceAccount exists,
// PodSecurityPolicy exists, we only update it, if its our PSP.
// ClusterRole exists,
// ClusterRoleBinding exists.
func CreateOrUpdateServiceAccountWithPSP(ctx context.Context, cr CRDObject, rclient client.Client) error {

	if err := ensurePSPExists(ctx, cr, rclient); err != nil {
		return fmt.Errorf("failed check policy: %w", err)
	}
	if err := ensureClusterRoleExists(ctx, cr, rclient); err != nil {
		return fmt.Errorf("failed check clusterrole: %w", err)
	}
	if err := ensureClusterRoleBindingExists(ctx, cr, rclient); err != nil {
		return fmt.Errorf("failed check clusterrolebinding: %w", err)
	}
	return nil
}

func CreateServiceAccountForCRD(ctx context.Context, cr CRDObject, rclient client.Client) error {
	newSA := buildSA(cr)
	var existSA v1.ServiceAccount
	if err := rclient.Get(ctx, types.NamespacedName{Name: cr.GetServiceAccountName(), Namespace: cr.GetNSName()}, &existSA); err != nil {
		if errors.IsNotFound(err) {
			return rclient.Create(ctx, newSA)
		}
		return fmt.Errorf("cannot get ServiceAccount for given CRD Object=%q, err=%w", cr.PrefixedName(), err)
	}
	newSA.Finalizers = v1beta12.MergeFinalizers(&existSA, v1beta12.FinalizerName)
	newSA.Annotations = labels.Merge(newSA.Annotations, existSA.Annotations)
	newSA.Labels = labels.Merge(existSA.Labels, newSA.Labels)
	newSA.Secrets = existSA.Secrets
	return rclient.Update(ctx, newSA)
}

func ensurePSPExists(ctx context.Context, cr CRDObject, rclient client.Client) error {
	defaultPSP := BuildPSP(cr)
	var existPSP v1beta1.PodSecurityPolicy
	err := k8stools.ListClusterWideObjects(ctx, rclient, &v1beta1.PodSecurityPolicyList{}, func(r runtime.Object) {
		items := r.(*v1beta1.PodSecurityPolicyList)
		for _, i := range items.Items {
			if i.Name == defaultPSP.Name {
				existPSP = i
				return
			}
		}
	})
	if err != nil {
		return err
	}
	if existPSP.Name == "" {
		return rclient.Create(ctx, defaultPSP)
	}
	// check if its ours, if so, update it
	if cr.GetPSPName() != cr.PrefixedName() {
		return nil
	}
	defaultPSP.Annotations = labels.Merge(defaultPSP.Annotations, existPSP.Labels)
	defaultPSP.Labels = labels.Merge(existPSP.Labels, defaultPSP.Labels)
	defaultPSP.Finalizers = v1beta12.MergeFinalizers(&existPSP, v1beta12.FinalizerName)
	return rclient.Update(ctx, defaultPSP)
}

func ensureClusterRoleExists(ctx context.Context, cr CRDObject, rclient client.Client) error {
	clusterRole := buildClusterRoleForPSP(cr)
	var existsClusterRole v12.ClusterRole
	err := k8stools.ListClusterWideObjects(ctx, rclient, &v12.ClusterRoleList{}, func(r runtime.Object) {
		items := r.(*v12.ClusterRoleList)
		for _, i := range items.Items {
			if i.Name == clusterRole.Name {
				existsClusterRole = i
				return
			}
		}
	})
	if err != nil {
		return err
	}
	if existsClusterRole.Name == "" {
		return rclient.Create(ctx, clusterRole)
	}

	clusterRole.Annotations = labels.Merge(clusterRole.Annotations, existsClusterRole.Annotations)
	clusterRole.Labels = labels.Merge(existsClusterRole.Labels, clusterRole.Labels)
	clusterRole.Finalizers = v1beta12.MergeFinalizers(&existsClusterRole, v1beta12.FinalizerName)
	return rclient.Update(ctx, clusterRole)
}

func ensureClusterRoleBindingExists(ctx context.Context, cr CRDObject, rclient client.Client) error {
	clusterRoleBinding := buildClusterRoleBinding(cr)
	var existsClusterRoleBinding v12.ClusterRoleBinding
	err := k8stools.ListClusterWideObjects(ctx, rclient, &v12.ClusterRoleBindingList{}, func(r runtime.Object) {
		items := r.(*v12.ClusterRoleBindingList)
		for _, i := range items.Items {
			if i.Name == clusterRoleBinding.Name {
				existsClusterRoleBinding = i
				return
			}
		}
	})
	if err != nil {
		return err
	}
	if existsClusterRoleBinding.Name == "" {
		return rclient.Create(ctx, clusterRoleBinding)
	}

	clusterRoleBinding.Labels = labels.Merge(existsClusterRoleBinding.Labels, clusterRoleBinding.Labels)
	clusterRoleBinding.Annotations = labels.Merge(clusterRoleBinding.Annotations, existsClusterRoleBinding.Annotations)
	clusterRoleBinding.Finalizers = v1beta12.MergeFinalizers(&existsClusterRoleBinding, v1beta12.FinalizerName)
	return rclient.Update(ctx, clusterRoleBinding)
}

func buildSA(cr CRDObject) *v1.ServiceAccount {
	return &v1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:        cr.GetServiceAccountName(),
			Namespace:   cr.GetNSName(),
			Labels:      cr.Labels(),
			Annotations: cr.Annotations(),
			Finalizers:  []string{v1beta12.FinalizerName},
		},
	}
}

func buildClusterRoleForPSP(cr CRDObject) *v12.ClusterRole {
	return &v12.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   cr.GetNSName(),
			Name:        cr.PrefixedName(),
			Labels:      cr.Labels(),
			Annotations: cr.Annotations(),
			Finalizers:  []string{v1beta12.FinalizerName},
		},
		Rules: []v12.PolicyRule{
			{
				Resources:     []string{"podsecuritypolicies"},
				Verbs:         []string{"use"},
				APIGroups:     []string{"policy"},
				ResourceNames: []string{cr.GetPSPName()},
			},
		},
	}
}

func buildClusterRoleBinding(cr CRDObject) *v12.ClusterRoleBinding {
	return &v12.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:        cr.PrefixedName(),
			Namespace:   cr.GetNSName(),
			Labels:      cr.Labels(),
			Annotations: cr.Annotations(),
			Finalizers:  []string{v1beta12.FinalizerName},
		},
		Subjects: []v12.Subject{
			{
				Kind:      v12.ServiceAccountKind,
				Name:      cr.GetServiceAccountName(),
				Namespace: cr.GetNSName(),
			},
		},
		RoleRef: v12.RoleRef{
			APIGroup: v12.GroupName,
			Name:     cr.PrefixedName(),
			Kind:     "ClusterRole",
		},
	}
}

func BuildPSP(cr CRDObject) *v1beta1.PodSecurityPolicy {
	return &v1beta1.PodSecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:        cr.GetPSPName(),
			Namespace:   cr.GetNSName(),
			Labels:      cr.Labels(),
			Annotations: cr.Annotations(),
			Finalizers:  []string{v1beta12.FinalizerName},
		},
		Spec: v1beta1.PodSecurityPolicySpec{
			ReadOnlyRootFilesystem: false,
			Volumes: []v1beta1.FSType{
				v1beta1.PersistentVolumeClaim,
				v1beta1.Secret,
				v1beta1.EmptyDir,
				v1beta1.ConfigMap,
				v1beta1.Projected,
				v1beta1.DownwardAPI,
				v1beta1.NFS,
			},
			AllowPrivilegeEscalation: pointer.BoolPtr(false),
			HostNetwork:              true,
			RunAsUser: v1beta1.RunAsUserStrategyOptions{
				Rule: v1beta1.RunAsUserStrategyRunAsAny,
			},
			SELinux:            v1beta1.SELinuxStrategyOptions{Rule: v1beta1.SELinuxStrategyRunAsAny},
			SupplementalGroups: v1beta1.SupplementalGroupsStrategyOptions{Rule: v1beta1.SupplementalGroupsStrategyRunAsAny},
			FSGroup:            v1beta1.FSGroupStrategyOptions{Rule: v1beta1.FSGroupStrategyRunAsAny},
			HostPID:            false,
			HostIPC:            false,
			RequiredDropCapabilities: []v1.Capability{
				"ALL",
			},
		}}
}
