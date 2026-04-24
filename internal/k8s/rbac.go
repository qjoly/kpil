package k8s

import (
	"context"
	"fmt"
	"strings"

	rbacv1 "k8s.io/api/rbac/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RBACConfig holds the configuration for the RBAC resources.
type RBACConfig struct {
	Namespace string
	SAName    string
}

// ProvisionedResources tracks which resources were actually created so that
// partial cleanups can target only what was provisioned.
type ProvisionedResources struct {
	ServiceAccount     bool
	ClusterRole        bool
	ClusterRoleBinding bool
}

// AllProvisioned is a convenience value indicating all three resources exist.
var AllProvisioned = ProvisionedResources{
	ServiceAccount:     true,
	ClusterRole:        true,
	ClusterRoleBinding: true,
}

// EnsureRBAC creates-or-reuses the ServiceAccount, ClusterRole, and
// ClusterRoleBinding required for read-only access (secrets excluded).
// It returns a ProvisionedResources value that tracks what was created so
// that a partial cleanup can be performed if a later step fails.
func (c *Client) EnsureRBAC(ctx context.Context, cfg RBACConfig) (ProvisionedResources, error) {
	var provisioned ProvisionedResources

	// 1. ServiceAccount ---------------------------------------------------
	if err := c.ensureServiceAccount(ctx, cfg); err != nil {
		return provisioned, fmt.Errorf("ServiceAccount: %w", err)
	}
	provisioned.ServiceAccount = true
	fmt.Printf("  ServiceAccount %s/%s ready\n", cfg.Namespace, cfg.SAName)

	// 2. ClusterRole ------------------------------------------------------
	if err := c.ensureClusterRole(ctx, cfg); err != nil {
		return provisioned, fmt.Errorf("ClusterRole: %w", err)
	}
	provisioned.ClusterRole = true
	fmt.Printf("  ClusterRole %s ready\n", cfg.SAName)

	// 3. ClusterRoleBinding -----------------------------------------------
	if err := c.ensureClusterRoleBinding(ctx, cfg); err != nil {
		return provisioned, fmt.Errorf("ClusterRoleBinding: %w", err)
	}
	provisioned.ClusterRoleBinding = true
	fmt.Printf("  ClusterRoleBinding %s ready\n", cfg.SAName)

	return provisioned, nil
}

// DeleteRBAC removes RBAC resources from the cluster for the resources that
// were actually provisioned. Errors are collected and returned together so
// that a single failing deletion does not skip the others.
func (c *Client) DeleteRBAC(ctx context.Context, cfg RBACConfig, provisioned ProvisionedResources) error {
	var errs []string

	if provisioned.ClusterRoleBinding {
		if err := c.clientset.RbacV1().ClusterRoleBindings().Delete(ctx, cfg.SAName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Sprintf("ClusterRoleBinding %s: %v", cfg.SAName, err))
		} else {
			fmt.Printf("  Deleted ClusterRoleBinding %s\n", cfg.SAName)
		}
	}

	if provisioned.ClusterRole {
		if err := c.clientset.RbacV1().ClusterRoles().Delete(ctx, cfg.SAName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Sprintf("ClusterRole %s: %v", cfg.SAName, err))
		} else {
			fmt.Printf("  Deleted ClusterRole %s\n", cfg.SAName)
		}
	}

	if provisioned.ServiceAccount {
		if err := c.clientset.CoreV1().ServiceAccounts(cfg.Namespace).Delete(ctx, cfg.SAName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Sprintf("ServiceAccount %s/%s: %v", cfg.Namespace, cfg.SAName, err))
		} else {
			fmt.Printf("  Deleted ServiceAccount %s/%s\n", cfg.Namespace, cfg.SAName)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("cleanup errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func (c *Client) ensureServiceAccount(ctx context.Context, cfg RBACConfig) error {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.SAName,
			Namespace: cfg.Namespace,
			Labels:    managedLabels(),
		},
	}
	_, err := c.clientset.CoreV1().ServiceAccounts(cfg.Namespace).Create(ctx, sa, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		fmt.Printf("  ServiceAccount %s/%s already exists — reusing\n", cfg.Namespace, cfg.SAName)
		return nil
	}
	return err
}

func (c *Client) ensureClusterRole(ctx context.Context, cfg RBACConfig) error {
	rules, err := c.buildReadOnlyRules(ctx)
	if err != nil {
		return fmt.Errorf("building policy rules: %w", err)
	}

	cr := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   cfg.SAName,
			Labels: managedLabels(),
		},
		Rules: rules,
	}

	_, err = c.clientset.RbacV1().ClusterRoles().Create(ctx, cr, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		fmt.Printf("  ClusterRole %s already exists — reusing\n", cfg.SAName)
		return nil
	}
	return err
}

func (c *Client) ensureClusterRoleBinding(ctx context.Context, cfg RBACConfig) error {
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:   cfg.SAName,
			Labels: managedLabels(),
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      cfg.SAName,
				Namespace: cfg.Namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     cfg.SAName,
		},
	}

	_, err := c.clientset.RbacV1().ClusterRoleBindings().Create(ctx, crb, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		fmt.Printf("  ClusterRoleBinding %s already exists — reusing\n", cfg.SAName)
		return nil
	}
	return err
}

// buildReadOnlyRules uses the discovery API to enumerate all API resources
// from the live cluster, then builds PolicyRules granting get/list/watch on
// every resource EXCEPT "secrets".
//
// Resources are grouped by (APIGroup, ResourceName) to keep the resulting
// ClusterRole as compact as possible.
func (c *Client) buildReadOnlyRules(ctx context.Context) ([]rbacv1.PolicyRule, error) {
	_, apiResourceLists, err := c.clientset.Discovery().ServerGroupsAndResources()
	if err != nil {
		// Partial errors are common when some API groups are unavailable (e.g.
		// metrics-server not installed). Log a warning but continue.
		fmt.Printf("  Warning: discovery returned partial results: %v\n", err)
	}

	// group → set of resource names
	type groupResource struct {
		group    string
		resource string
	}

	seen := make(map[groupResource]struct{})
	// apiGroup → []resources
	ruleMap := make(map[string][]string)

	for _, list := range apiResourceLists {
		// Parse the group from the GroupVersion string (e.g. "apps/v1" → "apps",
		// "" for core group "v1").
		group := groupFromGV(list.GroupVersion)

		for _, r := range list.APIResources {
			// Skip sub-resources (e.g. "pods/log", "pods/exec").
			if strings.Contains(r.Name, "/") {
				continue
			}
			// Skip secrets — this is the core requirement.
			if r.Name == "secrets" {
				continue
			}
			// Skip resources that do not support the verbs we need.
			if !supportsVerb(r.Verbs, "get") {
				continue
			}

			gr := groupResource{group: group, resource: r.Name}
			if _, dup := seen[gr]; dup {
				continue
			}
			seen[gr] = struct{}{}
			ruleMap[group] = append(ruleMap[group], r.Name)
		}
	}

	var rules []rbacv1.PolicyRule
	for group, resources := range ruleMap {
		rules = append(rules, rbacv1.PolicyRule{
			APIGroups: []string{group},
			Resources: resources,
			Verbs:     []string{"get", "list", "watch"},
		})
	}

	fmt.Printf("  Built ClusterRole with %d API groups (%d resource entries)\n",
		len(rules), totalResources(rules))
	return rules, nil
}

// ---------------------------------------------------------------------------
// Small utilities
// ---------------------------------------------------------------------------

// groupFromGV extracts the API group from a GroupVersion string.
//   - "v1"      → "" (core group)
//   - "apps/v1" → "apps"
func groupFromGV(gv string) string {
	parts := strings.SplitN(gv, "/", 2)
	if len(parts) == 1 {
		return "" // core group
	}
	return parts[0]
}

// supportsVerb reports whether the verb list advertised by the API resource
// contains the requested verb.
func supportsVerb(verbs metav1.Verbs, verb string) bool {
	for _, v := range verbs {
		if v == verb {
			return true
		}
	}
	return false
}

// managedLabels returns a label set that identifies resources owned by this tool.
func managedLabels() map[string]string {
	return map[string]string{
		"app.kubernetes.io/managed-by": "kpil",
	}
}

func totalResources(rules []rbacv1.PolicyRule) int {
	n := 0
	for _, r := range rules {
		n += len(r.Resources)
	}
	return n
}
