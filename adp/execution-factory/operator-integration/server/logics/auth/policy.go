package auth

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

// CreateOwnerPolicy creates owner permissions.
func (s *authServiceImpl) CreateOwnerPolicy(ctx context.Context, accessor *interfaces.AuthAccessor, authResource *interfaces.AuthResource) error {
	return s.CreatePolicy(ctx, accessor, authResource,
		[]interfaces.AuthOperationType{
			interfaces.AuthOperationTypeCreate,
			interfaces.AuthOperationTypeModify,
			interfaces.AuthOperationTypeDelete,
			interfaces.AuthOperationTypeView,
			interfaces.AuthOperationTypePublish,
			interfaces.AuthOperationTypeUnpublish,
			interfaces.AuthOperationTypeAuthorize,
			interfaces.AuthOperationTypePublicAccess,
			interfaces.AuthOperationTypeExecute,
		},
		[]interfaces.AuthOperationType{})
}

// CreateIntCompPolicyForAllUsers creates an internal component permission policy that affects all users.
func (s *authServiceImpl) CreateIntCompPolicyForAllUsers(ctx context.Context, authResource *interfaces.AuthResource) error {
	// root department visitor.
	rootDepartmentAccessor := &interfaces.AuthAccessor{
		ID:   interfaces.AccessorRootDepartmentID,
		Type: interfaces.AccessorTypeDepartment,
	}
	// The default root department (everyone) of built-in components has public access and execution permissions.
	return s.CreatePolicy(ctx, rootDepartmentAccessor, authResource,
		[]interfaces.AuthOperationType{
			interfaces.AuthOperationTypePublicAccess,
			interfaces.AuthOperationTypeExecute,
		},
		[]interfaces.AuthOperationType{})
}

// CreatePolicy creates a policy.
func (s *authServiceImpl) CreatePolicy(
	ctx context.Context,
	accessor *interfaces.AuthAccessor,
	authResource *interfaces.AuthResource,
	allow []interfaces.AuthOperationType,
	deny []interfaces.AuthOperationType,
) error {
	authCreatePolicyRequest := &interfaces.AuthCreatePolicyRequest{
		Accessor: accessor,
		Resource: authResource,
	}
	policyOperation := &interfaces.PolicyOperation{
		Allow: []*interfaces.AuthOperation{},
		Deny:  []*interfaces.AuthOperation{},
	}
	for _, operation := range allow {
		policyOperation.Allow = append(policyOperation.Allow, &interfaces.AuthOperation{
			ID:   string(operation),
			Name: string(operation),
		})
	}
	for _, operation := range deny {
		policyOperation.Deny = append(policyOperation.Deny, &interfaces.AuthOperation{
			ID:   string(operation),
			Name: string(operation),
		})
	}
	authCreatePolicyRequest.Operation = policyOperation
	req := []*interfaces.AuthCreatePolicyRequest{authCreatePolicyRequest}
	return s.authorization.CreatePolicy(ctx, req)
}

// DeletePolicy delete policy.
func (s *authServiceImpl) DeletePolicy(ctx context.Context, resourceIDs []string, resourceType interfaces.AuthResourceType) error {
	authDeletePolicyRequest := &interfaces.AuthDeletePolicyRequest{
		Method:    interfaces.AuthMethodDelete,
		Resources: []*interfaces.AuthResource{},
	}
	for _, resourceID := range resourceIDs {
		authDeletePolicyRequest.Resources = append(authDeletePolicyRequest.Resources, &interfaces.AuthResource{
			ID:   resourceID,
			Type: string(resourceType),
		})
	}
	return s.authorization.DeletePolicy(ctx, authDeletePolicyRequest)
}
