package controller

import (
	"context"
	"log/slog"

	akvv1alpha1 "github.com/jeanhaley32/azure-keyvault-sync-controller/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// AzureKeyVaultSyncReconciler reconciles AzureKeyVaultSync CRD objects
type AzureKeyVaultSyncReconciler struct {
	client.Client
	Controller *Controller
}

// SetupWithManager registers the reconciler with the controller-runtime manager
func (r *AzureKeyVaultSyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	slog.Info("Registering AzureKeyVaultSync controller with manager")
	return ctrl.NewControllerManagedBy(mgr).
		For(&akvv1alpha1.AzureKeyVaultSync{}).
		Complete(r)
}

// Reconcile handles AzureKeyVaultSync CRD events
func (r *AzureKeyVaultSyncReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	slog.Info("Reconciling AzureKeyVaultSync",
		"namespace", req.Namespace,
		"name", req.Name)

	// Fetch the AzureKeyVaultSync resource
	akv := &akvv1alpha1.AzureKeyVaultSync{}
	if err := r.Get(ctx, req.NamespacedName, akv); err != nil {
		if errors.IsNotFound(err) {
			// Resource deleted - nothing to do (garbage collection handles SPC cleanup via OwnerReferences)
			slog.Info("AzureKeyVaultSync resource not found - may have been deleted",
				"namespace", req.Namespace,
				"name", req.Name)
			return reconcile.Result{}, nil
		}
		slog.Error("Failed to get AzureKeyVaultSync resource",
			"namespace", req.Namespace,
			"name", req.Name,
			"error", err)
		return reconcile.Result{}, err
	}

	// Delegate to the main controller's reconciliation logic
	if err := r.Controller.reconcileAzureKeyVaultSync(ctx, akv); err != nil {
		slog.Error("Failed to reconcile AzureKeyVaultSync",
			"namespace", req.Namespace,
			"name", req.Name,
			"error", err)
		return reconcile.Result{}, err
	}

	slog.Info("Successfully reconciled AzureKeyVaultSync",
		"namespace", req.Namespace,
		"name", req.Name)

	// Requeue after sync interval to keep SPC updated with vault changes
	return reconcile.Result{RequeueAfter: r.Controller.config.SyncInterval}, nil
}
